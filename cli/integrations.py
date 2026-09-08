"""Source for the integrations embedded in the single-file CLI.

Run scripts/build-cli-integrations.py after editing this file or cli/assets.
No imports from this file are needed by an installed finalechat executable.
"""

import contextlib
import copy
import gzip
import queue
import stat
import tempfile
import threading
import zipfile

INTEGRATION_DIR = CONFIG_DIR / "integrations"
ARTIFACT_CHUNK = 1 << 20
ARTIFACT_MAX_FILE = 512 << 20
ARTIFACT_MAX_TOTAL = 2 << 30
INTEGRATION_SCHEMA = "finalechat-integrations/1"


def canonical_bytes(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")


def sha256(value):
    return hashlib.sha256(value).hexdigest()


def private_write(path, raw):
    """Durable replacement; never follow a destination symlink or share temp names."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink():
        raise CLIError(f"Refusing to replace a symlink: {path}")
    fd, name = tempfile.mkstemp(prefix=".finalechat-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(raw)
            out.flush()
            os.fsync(out.fileno())
        os.replace(name, path)
        if os.name == "posix":
            directory = os.open(str(path.parent), os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(name)


def private_json(path, value):
    private_write(path, canonical_bytes(value) + b"\n")


def read_object(path, default=None, max_bytes=1 << 20):
    try:
        with Path(path).open("rb") as source:
            raw = source.read(max_bytes + 1)
        if len(raw) > max_bytes:
            raise CLIError(f"Document is too large: {path}")
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise CLIError(f"Expected a JSON object: {path}")
        return value
    except FileNotFoundError:
        return {} if default is None else default


@contextlib.contextmanager
def integration_lock(path, timeout=10):
    if os.name != "posix":
        raise CLIError("Settings control requires POSIX advisory locks on this platform.")
    import fcntl
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(str(path), os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0), 0o600)
    deadline = time.monotonic() + timeout
    try:
        while True:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise CLIError("Another integration process owns this resource.") from None
                time.sleep(0.05)
        yield
    finally:
        os.close(fd)


def integration_key(provider, project):
    return provider + "-" + sha256(os.fsencode(str(Path(project).resolve())))[:24]


def integration_path(provider, project):
    return INTEGRATION_DIR / integration_key(provider, project)


def portable_path(name):
    if not isinstance(name, str) or not name or len(name.encode()) > 512:
        return False
    if any(c in name for c in "\\:%?#") or any(ord(c) < 32 or ord(c) == 127 for c in name):
        return False
    for part in name.split("/"):
        if part in ("", ".", "..") or part[-1] in " ." or len(part.encode()) > 255:
            return False
        if re.fullmatch(r"(?i)(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?", part):
            return False
    return True


@contextlib.contextmanager
def regular_source(path):
    path = Path(path)
    fd = os.open(str(path), os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > ARTIFACT_MAX_FILE:
            raise CLIError(f"Not a bounded regular file: {path}")
        with os.fdopen(fd, "rb", closefd=False) as source:
            yield source, info.st_size
    finally:
        os.close(fd)


def committed_jsonl(source, limit):
    """Copy only complete native records, with a fixed initial EOF boundary."""
    remaining = limit
    line = 0
    while remaining:
        raw = source.readline(min(remaining, (16 << 20) + 1))
        if not raw:
            break
        remaining -= len(raw)
        if len(raw) > 16 << 20:
            raise CLIError("A native JSONL record exceeds 16 MiB.")
        if not raw.endswith(b"\n"):
            if remaining:
                raise CLIError("A native JSONL file changed during capture.")
            break
        line += 1
        try:
            value = json.loads(raw)
        except (ValueError, UnicodeError):
            raise CLIError(f"Invalid native JSONL record at line {line}.") from None
        if not isinstance(value, dict):
            raise CLIError(f"Expected native event object at line {line}.")
        yield raw


class WebsiteExport:
    def __init__(self, directory, provider, session_id, project):
        self.directory = Path(directory)
        self.files = []
        self.total = 0
        self.manifest = {
            "format": "finalechat.website/v1", "producer": {"name": provider, "version": VERSION},
            "entrypoint": "index.html", "settings_entrypoint": "settings/index.html",
            "captured_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            "dataset": {"format": provider + ".native-jsonl/v1", "session_id": session_id,
                        "project": str(project), "files": []},
            "viewer": {"format": "finalechat.native-session-viewer/v1", "version": VERSION}, "files": self.files,
        }

    def add(self, name, role, content_type, parts):
        if not portable_path(name) or any(f["path"].casefold() == name.casefold() for f in self.files):
            raise CLIError("Invalid or duplicate archive path.")
        target = self.directory / name
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        record = {"path": name, "role": role, "content_type": content_type, "size": 0, "chunks": []}
        digest = hashlib.sha256()
        pending = bytearray()
        with target.open("xb") as out:
            os.chmod(target, 0o600)
            for raw in parts:
                self.total += len(raw)
                record["size"] += len(raw)
                if record["size"] > ARTIFACT_MAX_FILE or self.total > ARTIFACT_MAX_TOTAL:
                    raise CLIError("Archive exceeds the storage limits.")
                digest.update(raw)
                out.write(raw)
                pending.extend(raw)
                while len(pending) >= ARTIFACT_CHUNK:
                    chunk = bytes(pending[:ARTIFACT_CHUNK])
                    del pending[:ARTIFACT_CHUNK]
                    record["chunks"].append({"sha256": sha256(chunk), "size": len(chunk)})
            if pending:
                record["chunks"].append({"sha256": sha256(pending), "size": len(pending)})
            out.flush()
            os.fsync(out.fileno())
        record["sha256"] = digest.hexdigest()
        self.files.append(record)
        if len(self.files) > 4096 or sum(len(f["chunks"]) for f in self.files) > 16384:
            raise CLIError("Archive contains too many files or chunks.")

    def json(self, name, role, value):
        self.add(name, role, "application/json", [canonical_bytes(value) + b"\n"])

    def finish(self):
        self.files.sort(key=lambda f: f["path"])
        private_json(self.directory / "manifest.json", self.manifest)
        return self.manifest


def artifact_asset(name):
    return gzip.decompress(base64.b64decode(INTEGRATION_ASSETS[name])).decode("utf-8")


def build_native_export(directory, registration, settings=None, audit_path=None):
    provider, project = registration["provider"], Path(registration["project"])
    native = Path(registration["transcript"])
    export = WebsiteExport(directory, provider, registration["session_id"], project)
    sources = [(native, "sessions/" + native.name)]
    if provider == "claude-code":
        # Claude keeps full subagent records beside the parent, in <session>/subagents.
        root = native.parent / native.stem / "subagents"
        if root.exists():
            if root.is_symlink() or not root.is_dir():
                raise CLIError("Subagent directory must be a real directory.")
            for child in sorted(root.glob("*.jsonl")):
                sources.append((child, "sessions/" + native.stem + "/subagents/" + child.name))
    for source, name in sources:
        with regular_source(source) as (stream, size):
            export.add(name, "source", "application/x-ndjson", committed_jsonl(stream, size))
        export.manifest["dataset"]["files"].append(name)
    export.json("context/capture.json", "context", {
        "provider": provider, "project": str(project), "session_id": registration["session_id"],
        "native_file": native.name, "native_version": registration.get("native_version", "unknown"),
        "capture": "Complete newline-terminated native records; an incomplete final record is omitted.",
        "restoration": "Verified source recovery only. Runtime state, credentials and external tool effects are not restored.",
    })
    if settings is None:
        settings = {"label": provider + " settings", "descriptor": {
            "format": "finalechat.settings/v1", "schema_version": INTEGRATION_SCHEMA,
            "adapter_version": VERSION, "fields": []}, "snapshot": {
                "version": "unavailable", "context": "unavailable", "saved": {}, "effective": {},
                "runtime_known": False, "details": {"reason": "No settings snapshot was available at capture time."}}}
    export.json("settings/state.json", "context", settings)
    export.manifest["dataset"]["settings_version"] = settings["snapshot"]["version"]
    if audit_path and Path(audit_path).exists():
        with regular_source(audit_path) as (stream, size):
            export.add("context/settings-audit.jsonl", "context", "application/x-ndjson", committed_jsonl(stream, size))
    sdk = artifact_asset("sdk")
    for asset, name in (("viewer", "index.html"), ("settings", "settings/index.html")):
        raw = artifact_asset(asset).replace("/* FINALE_ARTIFACT_SDK */", sdk).encode("utf-8")
        export.add(name, "viewer", "text/html", [raw])
    return export.finish()


def verify_website(directory):
    """Verify all source bytes before publication or recovery; never execute HTML."""
    directory = Path(directory).resolve()
    manifest = read_object(directory / "manifest.json")
    files = manifest.get("files", [])
    if manifest.get("format") != "finalechat.website/v1" or not isinstance(files, list) or not 1 <= len(files) <= 4096:
        raise CLIError("Unsupported or invalid artifact manifest.")
    producer = manifest.get("producer", {})
    if not isinstance(producer, dict) or not isinstance(producer.get("name"), str) or not 1 <= len(producer["name"].encode()) <= 120 or not isinstance(producer.get("version"), str) or len(producer["version"].encode()) > 200:
        raise CLIError("Invalid artifact producer name or version.")
    if not isinstance(manifest.get("dataset"), dict):
        raise CLIError("Artifact dataset metadata must be an object.")
    try:
        captured = datetime.fromisoformat(manifest["captured_at"].replace("Z", "+00:00"))
        if captured.tzinfo is None:
            raise ValueError()
    except (KeyError, AttributeError, TypeError, ValueError):
        raise CLIError("Artifact capture time must include a timezone.") from None
    seen, total, chunk_count = set(), 0, 0
    for record in files:
        if not isinstance(record, dict):
            raise CLIError("Invalid artifact file record.")
        name = record.get("path")
        if not portable_path(name) or name.casefold() in seen or name.casefold() == "manifest.json":
            raise CLIError("Unsafe or duplicate archive filename.")
        seen.add(name.casefold())
        content_type = record.get("content_type")
        if record.get("role") not in ("viewer", "source", "asset", "derived", "context") or not isinstance(content_type, str) or not 1 <= len(content_type.encode()) <= 200 or "\r" in content_type or "\n" in content_type:
            raise CLIError("Invalid artifact file role or content type.")
        if type(record.get("size")) is not int or not 0 <= record["size"] <= ARTIFACT_MAX_FILE or not isinstance(record.get("chunks"), list):
            raise CLIError("Invalid artifact file size or chunks.")
        path = directory / name
        for part in [path, *path.parents]:
            if part == directory:
                break
            if part.is_symlink():
                raise CLIError("Archives cannot contain symlinks.")
        digest, count = hashlib.sha256(), 0
        with regular_source(path) as (source, size):
            if size != record.get("size"):
                raise CLIError(f"Wrong file size: {name}")
            for chunk in record.get("chunks", []):
                if not isinstance(chunk, dict):
                    raise CLIError("Invalid artifact chunk record.")
                length = chunk.get("size")
                if type(length) is not int or not 1 <= length <= ARTIFACT_CHUNK:
                    raise CLIError("Invalid chunk size.")
                raw = source.read(length)
                if len(raw) != length or sha256(raw) != chunk.get("sha256"):
                    raise CLIError(f"Corrupt archive chunk: {name}")
                digest.update(raw)
                count += length
                chunk_count += 1
            if count != size or source.read(1) or digest.hexdigest() != record.get("sha256"):
                raise CLIError(f"Corrupt archive file: {name}")
        total += count
        if total > ARTIFACT_MAX_TOTAL or chunk_count > 16384:
            raise CLIError("Archive exceeds storage limits.")
    for name in seen:
        if any(str(parent) in seen for parent in Path(name).parents if str(parent) != "."):
            raise CLIError("Archive contains a file/directory collision.")
    for index, entry in enumerate((manifest.get("entrypoint"), manifest.get("settings_entrypoint"))):
        if index == 1 and not entry:
            continue
        record = next((f for f in files if f["path"] == entry), None)
        if record is None or record["role"] != "viewer" or record["content_type"].split(";", 1)[0] != "text/html" or record["size"] > 8 << 20:
            raise CLIError("Archive entrypoints must name HTML viewer files of at most 8 MiB.")
    return manifest


def recover_source_file(directory, record, destination):
    """Check bytes again while copying, so verification is not a stale promise."""
    digest, count = hashlib.sha256(), 0
    with regular_source(Path(directory) / record["path"]) as (source, size):
        with Path(destination).open("xb") as out:
            os.chmod(destination, 0o600)
            for chunk in record["chunks"]:
                raw = source.read(chunk["size"])
                if len(raw) != chunk["size"] or sha256(raw) != chunk["sha256"]:
                    raise CLIError("Archive source changed during recovery.")
                digest.update(raw)
                count += len(raw)
                out.write(raw)
            if count != size or count != record["size"] or source.read(1) or digest.hexdigest() != record["sha256"]:
                raise CLIError("Archive source changed during recovery.")
            out.flush()
            os.fsync(out.fileno())


class PublicationDeleted(CLIError):
    def __init__(self, artifact_id=None):
        self.artifact_id = artifact_id
        super().__init__("The remote artifact was deleted. Automatic publication is paused; explicitly publish with --recreate to create a new record.")


class PublicationConflict(CLIError):
    pass


def publication_head(client, artifact_id):
    try:
        return client.request("GET", "/api/v1/artifacts/" + Client.ref_path(artifact_id))
    except APIError as error:
        if error.status == 404:
            raise PublicationDeleted(artifact_id) from None
        raise


def require_source_extension(directory, manifest, previous):
    """A native publisher may advance only when every old source is a prefix."""
    old_dataset, new_dataset = previous.get("dataset", {}), manifest.get("dataset", {})
    if any(old_dataset.get(k) != new_dataset.get(k) for k in ("format", "schema", "session_id")):
        raise PublicationConflict("The remote artifact belongs to a different native dataset.")
    local = {record["path"]: record for record in manifest["files"] if record["role"] == "source"}
    for old in previous["files"]:
        if old["role"] != "source":
            continue
        record = local.get(old["path"])
        if record is None or type(old["size"]) is not int or not 0 <= old["size"] <= record["size"]:
            raise PublicationConflict("The local capture is missing published native records. Restore or extend the local sources before publishing again.")
        digest, remaining = hashlib.sha256(), old["size"]
        with regular_source(Path(directory) / record["path"]) as (source, _size):
            while remaining:
                raw = source.read(min(remaining, ARTIFACT_CHUNK))
                if not raw:
                    raise PublicationConflict("The local capture no longer contains the published source prefix.")
                digest.update(raw)
                remaining -= len(raw)
        if digest.hexdigest() != old["sha256"]:
            raise PublicationConflict("Local and published source histories diverged. The published record was left unchanged.")


def publication_intent(client, directory, manifest, thread_ref, title, artifact_id=None, signature="", check_sources=False, key="session"):
    if artifact_id:
        head = publication_head(client, artifact_id)
    else:
        artifact = client.request("PUT", "/api/v1/threads/" + Client.ref_path(thread_ref) + "/artifacts/" + urllib.parse.quote(key, safe=""),
                                  body={"title": title}, retries=2)["artifact"]
        head = publication_head(client, artifact["id"])
    artifact = head["artifact"]
    if check_sources and head.get("revision"):
        require_source_extension(directory, manifest, head["revision"]["manifest"])
    return {"manifest_digest": sha256(canonical_bytes(manifest)), "artifact_id": artifact["id"],
            "thread_id": artifact["thread_id"], "previous_revision_id": artifact.get("current_revision_id"),
            "client_key": "native-" + secrets.token_hex(24), "directory": str(directory), "signature": signature}


def upload_website(client, directory, journal_path, thread_ref, title, artifact_id=None, signature="", check_sources=False, key="session"):
    """Journal immutable capture identity before the first remote write."""
    manifest = verify_website(directory)
    journal = read_object(journal_path)
    digest = sha256(canonical_bytes(manifest))
    if journal and journal.get("manifest_digest") != digest:
        raise CLIError("A pending publication has different staged content; retain it and retry first.")
    if not journal:
        journal = publication_intent(client, directory, manifest, thread_ref, title, artifact_id, signature, check_sources, key)
        private_json(journal_path, journal)
    if journal.get("revision_id"):
        return journal
    base = "/api/v1/artifacts/" + Client.ref_path(journal["artifact_id"])
    hashes = sorted({c["sha256"] for f in manifest["files"] for c in f["chunks"]})
    try:
        missing = set(client.request("POST", base + "/blobs/check", body={"hashes": hashes})["missing"])
    except APIError as error:
        if error.status == 404:
            publication_head(client, journal["artifact_id"])
        raise
    for record in manifest["files"]:
        with regular_source(Path(directory) / record["path"]) as (source, _size):
            for chunk in record["chunks"]:
                raw = source.read(chunk["size"])
                if sha256(raw) != chunk["sha256"]:
                    raise CLIError("Staged artifact changed during upload.")
                if chunk["sha256"] in missing:
                    client.request_bytes("PUT", base + "/blobs/" + chunk["sha256"], data=raw,
                                         content_type="application/octet-stream", timeout=60)
                    missing.remove(chunk["sha256"])
    try:
        revision = client.request("POST", base + "/revisions", body={
            "client_key": journal["client_key"], "previous_revision_id": journal["previous_revision_id"],
            "manifest": manifest}, retries=2)["revision"]
    except APIError as error:
        if error.status in (404, 409):
            head = publication_head(client, journal["artifact_id"])
            if error.status == 409 and head["artifact"].get("current_revision_id") != journal["previous_revision_id"]:
                raise PublicationConflict("Another publisher advanced this artifact. A fresh capture is required.") from None
        raise
    journal["revision_id"] = revision["id"]
    private_json(journal_path, journal)
    return journal


class NativeRPCError(CLIError):
    def __init__(self, method, error):
        # Native error data can contain configuration values. Keep it local;
        # command results use bounded adapter messages, never this raw object.
        super().__init__(f"Native API rejected {method}: {error.get('message', 'unknown error')}")
        self.error = error


class CodexRPC:
    """One owned App Server connection; it never resumes unrelated sessions."""
    def __init__(self, project, executable="codex", environment=None):
        self.process = subprocess.Popen([executable, "app-server", "--stdio"], cwd=str(project),
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.DEVNULL, env=environment)
        self.messages = queue.Queue(maxsize=256)
        self.sequence = 0
        self.closed = threading.Event()
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        try:
            self.info = self.call("initialize", {"clientInfo": {"name": "finalechat", "version": VERSION},
                                                 "capabilities": {"experimentalApi": True}})
            self._send({"method": "initialized"})
        except BaseException:
            self.close()
            raise

    def _read(self):
        try:
            while not self.closed.is_set():
                raw = self.process.stdout.readline((16 << 20) + 1)
                if not raw:
                    raise CLIError("Codex App Server disconnected.")
                if len(raw) > 16 << 20 or not raw.endswith(b"\n"):
                    raise CLIError("Oversized native API response.")
                self.messages.put(json.loads(raw), timeout=1)
        except Exception as err:
            with contextlib.suppress(queue.Full):
                self.messages.put(err, timeout=1)

    def _send(self, value):
        self.process.stdin.write(canonical_bytes(value) + b"\n")
        self.process.stdin.flush()

    def call(self, method, params, timeout=20):
        self.sequence += 1
        request_id = self.sequence
        self._send({"id": request_id, "method": method, "params": params})
        deadline = time.monotonic() + timeout
        while True:
            try:
                value = self.messages.get(timeout=max(0, deadline - time.monotonic()))
            except queue.Empty:
                raise CLIError(f"Native API timed out: {method}") from None
            if isinstance(value, Exception):
                raise CLIError("Could not read the native API response.") from value
            if "method" in value and "id" in value:
                # The settings/import adapter has no approval delegation. It
                # cannot answer an approval on behalf of the terminal user.
                self._send({"id": value["id"], "error": {"code": -32601, "message": "This connection only supports settings and read-only imports."}})
                continue
            if value.get("id") != request_id:
                continue
            if "error" in value:
                raise NativeRPCError(method, value["error"])
            return value.get("result", {})

    def close(self):
        self.closed.set()
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        for stream in (self.process.stdin, self.process.stdout):
            with contextlib.suppress(OSError):
                stream.close()

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()


def value_at(document, pointer, missing=None):
    value = document
    for part in pointer.strip("/").split("/"):
        if not isinstance(value, dict) or part not in value:
            return missing
        value = value[part]
    return value


def edit_at(document, pointer, edit):
    parts = pointer.strip("/").split("/")
    current = document
    parents = []
    for part in parts[:-1]:
        if part not in current:
            if edit["op"] == "unset":
                return
            current[part] = {}
        if not isinstance(current[part], dict):
            raise CLIError("A parent setting has an incompatible type; refresh and edit locally.")
        parents.append((current, part))
        current = current[part]
    if edit["op"] == "set":
        current[parts[-1]] = copy.deepcopy(edit["value"])
    else:
        current.pop(parts[-1], None)
        for parent, part in reversed(parents):
            if not parent[part]:
                parent.pop(part)


def setting_field(key, kind="string", choices=None, category="preference", timing="new_or_resumed_session", **extra):
    shape = {"type": kind}
    if kind == "string":
        shape["max_length"] = 32768
    if kind == "array":
        shape["items"] = {"type": "string", "max_length": 4096}
    if choices is not None:
        shape["enum"] = choices
    field = {"key": "/" + key, "label": key.replace("/", " · "), "schema": shape,
             "writable": True, "unset": True, "class": category, "effective_when": timing}
    field.update(extra)
    return field


def claude_catalog():
    result = [setting_field("model"), setting_field("effortLevel", choices=["low", "medium", "high", "max"]),
              setting_field("outputStyle", timing="restart_required"), setting_field("language"),
              setting_field("agent"), setting_field("attribution/commit"), setting_field("attribution/pr")]
    for name in ("alwaysThinkingEnabled", "fastMode", "fastModePerSessionOptIn", "autoMemoryEnabled",
                 "includeCoAuthoredBy", "includeGitInstructions", "respectGitignore", "spinnerTipsEnabled",
                 "terminalProgressBarEnabled"):
        result.append(setting_field(name, "boolean"))
    days = setting_field("cleanupPeriodDays", "integer")
    days["schema"].update(minimum=0, maximum=36500)
    result.append(days)
    for name in ("allow", "deny", "ask", "additionalDirectories"):
        result.append(setting_field("permissions/" + name, "array", category="permissions"))
    result.append(setting_field("permissions/defaultMode", choices=["default", "acceptEdits", "plan", "dontAsk", "bypassPermissions", "auto"], category="permissions"))
    result.append(setting_field("permissions/disableBypassPermissionsMode", choices=["disable"], category="permissions"))
    for name in ("enabled", "autoAllowBashIfSandboxed", "allowUnsandboxedCommands", "enableWeakerNestedSandbox", "enableWeakerNetworkIsolation"):
        result.append(setting_field("sandbox/" + name, "boolean", category="permissions", timing="restart_required"))
    for name in ("excludedCommands", "network/allowedDomains", "network/allowUnixSockets", "filesystem/allowWrite", "filesystem/denyRead", "filesystem/denyWrite"):
        result.append(setting_field("sandbox/" + name, "array", category="permissions", timing="restart_required"))
    return result


def codex_catalog():
    result = [setting_field("model"), setting_field("review_model"), setting_field("model_provider", category="credential_reference"),
              setting_field("model_reasoning_effort", choices=["none", "minimal", "low", "medium", "high", "xhigh"]),
              setting_field("plan_mode_reasoning_effort", choices=["none", "minimal", "low", "medium", "high", "xhigh"]),
              setting_field("model_reasoning_summary", choices=["auto", "concise", "detailed", "none"]),
              setting_field("model_verbosity", choices=["low", "medium", "high"]),
              setting_field("personality", choices=["none", "friendly", "pragmatic"]),
              setting_field("service_tier", choices=["fast", "flex"]),
              setting_field("approval_policy", choices=["untrusted", "on-failure", "on-request", "never"], category="permissions"),
              setting_field("sandbox_mode", choices=["read-only", "workspace-write", "danger-full-access"], category="permissions"),
              setting_field("web_search", choices=["disabled", "cached", "live"], category="permissions"),
              setting_field("developer_instructions"), setting_field("compact_prompt"),
              setting_field("history/persistence", choices=["save-all", "none"])]
    for name in ("hide_agent_reasoning", "show_raw_agent_reasoning", "check_for_update_on_startup",
                 "disable_paste_burst", "suppress_unstable_features_warning"):
        result.append(setting_field(name, "boolean"))
    for name in ("model_context_window", "model_auto_compact_token_limit", "tool_output_token_limit",
                 "project_doc_max_bytes", "background_terminal_max_timeout"):
        field = setting_field(name, "integer")
        field["schema"].update(minimum=1, maximum=2147483647)
        result.append(field)
    for name in ("network_access", "exclude_tmpdir_env_var", "exclude_slash_tmp"):
        result.append(setting_field("sandbox_workspace_write/" + name, "boolean", category="permissions"))
    result.append(setting_field("sandbox_workspace_write/writable_roots", "array", category="permissions"))
    result.append(setting_field("project_doc_fallback_filenames", "array"))
    result.append(setting_field("allow_login_shell", "boolean", category="executable"))
    return result


def shape_accepts(shape, value):
    kind = shape["type"]
    if "enum" in shape and not any(type(value) is type(choice) and value == choice for choice in shape["enum"]):
        return False
    if kind == "string":
        return isinstance(value, str) and len(value.encode()) <= shape.get("max_length", 32768)
    if kind == "boolean":
        return type(value) is bool
    if kind in ("integer", "number"):
        return type(value) in ((int,) if kind == "integer" else (int, float)) and shape.get("minimum", -float("inf")) <= value <= shape.get("maximum", float("inf"))
    if kind == "array":
        return isinstance(value, list) and len(value) <= 1024 and all(shape_accepts(shape["items"], item) for item in value)
    return False


def supported_snapshot_values(fields, saved, effective):
    """Do not export arbitrary native objects under otherwise known keys."""
    unsupported = []
    for field in fields:
        key = field["key"]
        if any(key in values and not shape_accepts(field["schema"], values[key]) for values in (saved, effective)):
            saved.pop(key, None)
            effective.pop(key, None)
            unsupported.append(key)
            field.update(writable=False, locked_reason="The native value is outside this adapter's supported schema. Inspect it locally.")
    return unsupported


def validate_local_proposal(view, proposal, grant):
    if len(canonical_bytes(proposal)) > 64 << 10:
        raise CLIError("Settings proposal exceeds 64 KiB.")
    if proposal.get("operation") not in grant["operations"]:
        raise CLIError("This operation is not authorized locally.")
    if proposal.get("schema_version") != view["descriptor"]["schema_version"]:
        raise CLIError("Settings schema changed; refresh before saving.")
    if proposal.get("generation", "") != view.get("generation", ""):
        raise CLIError("Settings runtime generation changed.")
    if proposal.get("expected_version") != view["snapshot"]["version"]:
        raise SettingsConflict("Settings changed; refresh before saving.")
    if proposal["operation"] == "settings.refresh":
        if proposal.get("edits") or proposal.get("parameters"):
            raise CLIError("Refresh accepts no payload.")
        return
    if proposal["operation"] != "settings.apply":
        raise CLIError("Unsupported settings operation.")
    edits = proposal.get("edits")
    if not isinstance(edits, list) or not 1 <= len(edits) <= 256 or proposal.get("parameters"):
        raise CLIError("Provide 1–256 settings edits.")
    fields = {field["key"]: field for field in view["descriptor"]["fields"]}
    seen = set()
    for edit in edits:
        if not isinstance(edit, dict) or not isinstance(edit.get("key"), str):
            raise CLIError("Invalid settings edit.")
        key, op = edit.get("key"), edit.get("op")
        field = fields.get(key)
        if key in seen or any(key.startswith(old + "/") or old.startswith(key + "/") for old in seen):
            raise CLIError("Overlapping settings edits are not allowed.")
        seen.add(key)
        if not field or not field["writable"] or field["class"] not in grant["classes"]:
            raise CLIError("This field is unavailable or not authorized locally.")
        if op == "unset" and field["unset"] and edit.get("value") is None:
            continue
        if op != "set" or not shape_accepts(field["schema"], edit.get("value")):
            raise CLIError("Invalid setting value: " + key)


class SettingsConflict(CLIError):
    pass


class ClaudeSettings:
    provider = "claude-code"

    def __init__(self, project, scope="project", user_directory=None):
        self.project = Path(project).resolve()
        self.scope = scope
        self.user_directory = Path(user_directory or os.environ.get("CLAUDE_CONFIG_DIR", "~/.claude")).expanduser().resolve()
        paths = {"user": self.user_directory / "settings.json", "project": self.project / ".claude/settings.json",
                 "project_local": self.project / ".claude/settings.local.json"}
        if scope not in paths:
            raise CLIError("Claude Code supports user, project and project_local scopes.")
        self.paths, self.path = paths, paths[scope]
        self.key = "claude-" + scope + "-" + sha256(os.fsencode(str(self.path)))[:24]
        self.native_version = native_version("claude")

    def grant(self):
        return {"key": self.key, "label": "Claude Code · " + ("User settings" if self.scope == "user" else self.project.name),
                "scope": self.scope, "operations": ["settings.apply", "settings.refresh"],
                "classes": ["preference", "permissions"]}

    def managed_paths(self):
        root = Path("/Library/Application Support/ClaudeCode" if sys.platform == "darwin" else "/etc/claude-code")
        paths = [root / "managed-settings.json"]
        fragments = root / "managed-settings.d"
        if fragments.is_dir():
            paths.extend(sorted(fragments.glob("*.json")))
        return paths

    def snapshot(self, grant=None):
        grant = grant or self.grant()
        layers = [(scope, path, read_object(path)) for scope, path in self.paths.items()]
        layers.extend(("managed", path, read_object(path)) for path in self.managed_paths())
        saved = next(data for scope, _path, data in layers if scope == self.scope)
        fields, saved_values, effective, provenance = claude_catalog(), {}, {}, {}
        for field in fields:
            key = field["key"]
            marker = object()
            for scope, path, data in layers:
                value = value_at(data, key, marker)
                if value is marker:
                    continue
                if scope == self.scope:
                    saved_values[key] = value
                # Permission arrays merge across scopes. A managed policy
                # remains authoritative even when its selected value is empty.
                if field["schema"]["type"] == "array" and isinstance(value, list) and scope != "managed":
                    previous = effective.get(key, [])
                    effective[key] = list(dict.fromkeys(previous + value)) if isinstance(previous, list) and all(isinstance(x, str) for x in previous + value) else value
                else:
                    effective[key] = value
                provenance[key] = scope
                if scope == "managed" or (list(self.paths).index(scope) > list(self.paths).index(self.scope) if scope in self.paths else False):
                    field.update(writable=False, locked_reason="Overridden by " + scope + " settings.")
            field["source"] = provenance.get(key, "default")
            if field["class"] not in grant["classes"]:
                field.update(writable=False, locked_reason="This settings class was not paired.")
            if field["class"] == "permissions":
                # Claude has no supported effective-policy read API. Cloud or
                # MDM requirements can be absent from these files; never claim
                # a remote permission change bypasses that policy boundary.
                field["description"] = "Saved file preference. Claude Code revalidates managed policy when loading it; current runtime adoption is unconfirmed."
        env_overrides = {"ANTHROPIC_MODEL": "/model", "CLAUDE_CODE_EFFORT_LEVEL": "/effortLevel"}
        environment = {}
        for name, key in env_overrides.items():
            if name in os.environ:
                environment[name] = sha256(os.fsencode(os.environ[name]))
                for field in fields:
                    if field["key"] == key:
                        field.update(writable=False, locked_reason="Connector environment overrides this field: " + name)
        context = [{"scope": scope, "path": str(path), "sha256": sha256(canonical_bytes(data))} for scope, path, data in layers]
        unsupported_values = supported_snapshot_values(fields, saved_values, effective)
        version = sha256(canonical_bytes([context, environment, INTEGRATION_SCHEMA, self.native_version, INTEGRATION_ADAPTER_DIGEST]))
        descriptor = {"format": "finalechat.settings/v1", "schema_version": INTEGRATION_SCHEMA + "/claude",
                      "adapter_version": VERSION, "fields": fields}
        known = {field["key"].split("/")[1] for field in fields}
        return {"key": self.key, "label": self.grant()["label"], "scope": self.scope, "generation": "", "descriptor": descriptor,
                "snapshot": {"version": version, "context": version, "saved": saved_values, "effective": effective,
                             "runtime_known": False, "details": {"layers": context, "sources": provenance,
                                 "native_version": self.native_version, "managed_policy": "Local managed files observed; cloud/MDM and another process's environment are not observable.",
                                 "unsupported_fields": sorted(k for k in saved if k not in known),
                                 "unsupported_values": unsupported_values,
                                 "credential_values": "Excluded; environment values and helper commands are not exported."}}}

    def prepare(self, proposal, grant):
        view = self.snapshot(grant)
        validate_local_proposal(view, proposal, grant)
        with contextlib.suppress(FileNotFoundError):
            if self.path.is_symlink():
                raise CLIError("Settings symlinks must be managed locally.")
        before = self.path.read_bytes() if self.path.exists() else b""
        data = json.loads(before) if before.strip() else {}
        for edit in proposal.get("edits", []):
            edit_at(data, edit["key"], edit)
        desired = json.dumps(data, ensure_ascii=False, indent=2, allow_nan=False).encode() + b"\n"
        if len(desired) > 1 << 20:
            raise CLIError("Resulting settings document exceeds 1 MiB.")
        return {"view": view, "before_hash": sha256(before), "desired_hash": sha256(desired),
                "desired_bytes": base64.b64encode(desired).decode(), "path": str(self.path)}

    def apply_prepared(self, prepared):
        before = self.path.read_bytes() if self.path.exists() else b""
        if sha256(before) != prepared["before_hash"]:
            raise SettingsConflict("Settings file changed immediately before saving.")
        private_write(self.path.with_name(self.path.name + ".finalechat-backup"), before)
        private_write(self.path, base64.b64decode(prepared["desired_bytes"]))

    def matches_prepared(self, prepared):
        raw = self.path.read_bytes() if self.path.exists() else b""
        return sha256(raw) == prepared["desired_hash"]

    def close(self):
        pass


def native_version(executable):
    try:
        result = subprocess.run([executable, "--version"], capture_output=True, text=True, timeout=5)
        return result.stdout.strip()[:200] if result.returncode == 0 else "unavailable"
    except (OSError, subprocess.SubprocessError):
        return "unavailable"


class CodexSettings:
    provider = "codex"

    def __init__(self, project, scope="user", rpc=None):
        if scope != "user":
            raise CLIError("This native Codex adapter writes user config. Project/profile layers remain visible through native provenance; direct writes to them are not advertised by this adapter.")
        self.project, self.scope = Path(project).resolve(), scope
        self.rpc = rpc or CodexRPC(self.project)
        self.native_version = str(self.rpc.info.get("userAgent", "unknown"))[:200]
        self.path = Path(self.rpc.info["codexHome"]) / "config.toml"
        self.key = "codex-user-" + sha256(os.fsencode(str(self.path)))[:24]

    def grant(self):
        return {"key": self.key, "label": "Codex · User defaults", "scope": "user",
                "operations": ["settings.apply", "settings.refresh"],
                "classes": ["preference", "permissions", "credential_reference", "executable"]}

    def read(self):
        result = self.rpc.call("config/read", {"cwd": str(self.project), "includeLayers": True})
        requirements = self.rpc.call("configRequirements/read", {}).get("requirements") or {}
        layer = next((layer for layer in result.get("layers") or [] if layer["name"]["type"] == "user" and not layer["name"].get("profile")), None)
        if layer is None or Path(layer["name"]["file"]).resolve() != self.path.resolve():
            raise CLIError("The native API did not identify the authorized user config layer.")
        return result, requirements, layer

    def snapshot(self, grant=None):
        grant = grant or self.grant()
        native, requirements, layer = self.read()
        fields, saved, effective = codex_catalog(), {}, {}
        requirements_map = {"/approval_policy": "allowedApprovalPolicies", "/sandbox_mode": "allowedSandboxModes", "/web_search": "allowedWebSearchModes"}
        for field in fields:
            key = field["key"]
            value = value_at(layer["config"], key)
            if value is not None:
                saved[key] = value
            value = value_at(native["config"], key)
            if value is not None:
                effective[key] = value
            origin = native["origins"].get(key[1:].replace("/", "."), {}).get("name", {})
            field["source"] = origin.get("type", "default")
            if origin.get("type") in ("mdm", "enterpriseManaged", "legacyManagedConfigTomlFromFile", "legacyManagedConfigTomlFromMdm", "project", "sessionFlags") or origin.get("profile"):
                field.update(writable=False, locked_reason="Overridden by a native " + field["source"] + " layer.")
            allowed = requirements.get(requirements_map.get(key, ""))
            if allowed is not None:
                field["schema"]["enum"] = [v for v in allowed if isinstance(v, str)]
                if not field["schema"]["enum"]:
                    field.update(writable=False, locked_reason="Managed policy requires a configuration this adapter cannot express.")
            if key == "/model_provider":
                names = list((native["config"].get("model_providers") or {}).keys())
                if isinstance(effective.get(key), str) and effective[key] not in names:
                    names.append(effective[key])
                if names:
                    field["schema"]["enum"] = sorted(names)
                else:
                    field.update(writable=False, locked_reason="Provider endpoints must be established locally first.")
            # Native null removes a value. Verified against the installed
            # config/batchWrite API; no handwritten TOML round trip.
            if field["class"] not in grant["classes"]:
                field.update(writable=False, locked_reason="This settings class was not paired.")
            if requirements.get("allowRemoteControl") is False:
                field.update(writable=False, locked_reason="Managed policy disables remote control.")
            if key == "/allow_login_shell" and requirements.get("allowLoginShell") is False:
                field.update(writable=False, locked_reason="Managed policy disables login shells.")
        context = [{"name": x["name"], "version": x["version"], "disabled_reason": x.get("disabledReason")} for x in native.get("layers") or []]
        unsupported_values = supported_snapshot_values(fields, saved, effective)
        version = sha256(canonical_bytes([context, requirements, INTEGRATION_SCHEMA, self.native_version, INTEGRATION_ADAPTER_DIGEST]))
        known = {f["key"].split("/")[1] for f in fields}
        # Never publish whole native config/read results: provider/MCP/env
        # dictionaries can contain credentials and executable helpers.
        return {"key": self.key, "label": self.grant()["label"], "scope": self.scope, "generation": "",
                "descriptor": {"format": "finalechat.settings/v1", "schema_version": INTEGRATION_SCHEMA + "/codex", "adapter_version": VERSION, "fields": fields},
                "snapshot": {"version": version, "context": version, "saved": saved, "effective": effective, "runtime_known": False,
                             "details": {"layers": context, "native_user_version": layer["version"], "native_version": self.native_version,
                                         "unsupported_values": unsupported_values,
                                         "managed_requirements_digest": sha256(canonical_bytes(requirements)),
                                         "unsupported_fields": sorted(k for k in native["config"] if k not in known),
                                         "runtime": "This connection owns configuration reads and writes. It does not control another Codex session."}}}

    def prepare(self, proposal, grant):
        view = self.snapshot(grant)
        validate_local_proposal(view, proposal, grant)
        edits = [{"keyPath": edit["key"][1:].replace("/", "."), "mergeStrategy": "replace",
                  "value": edit.get("value") if edit["op"] == "set" else None} for edit in proposal.get("edits", [])]
        return {"view": view, "native_expected_version": view["snapshot"]["details"]["native_user_version"],
                "edits": edits, "path": str(self.path), "before_hash": sha256(self.path.read_bytes() if self.path.exists() else b"")}

    def apply_prepared(self, prepared):
        self.rpc.call("config/batchWrite", {"edits": prepared["edits"], "filePath": str(self.path),
                                           "expectedVersion": prepared["native_expected_version"], "reloadUserConfig": False})

    def matches_prepared(self, prepared):
        _native, _requirements, layer = self.read()
        for edit in prepared["edits"]:
            if value_at(layer["config"], "/" + edit["keyPath"].replace(".", "/")) != edit["value"]:
                return False
        return True

    def close(self):
        self.rpc.close()


def make_settings_adapter(provider, project, scope=None):
    if provider == "claude-code":
        return ClaudeSettings(project, scope or "project")
    if provider == "codex":
        return CodexSettings(project, scope or "user")
    raise CLIError("Unsupported integration provider.")


def settings_lock_path(adapter):
    return INTEGRATION_DIR / "locks" / (sha256(os.fsencode(str(adapter.path.resolve()))) + ".lock")


def append_local_audit(directory, command, status, result):
    event = {"event_id": command["id"], "event": "settings." + status,
             "command_id": command["id"], "user_id": command.get("user_id"),
             "resource_id": command["resource_id"], "proposal_digest": command["proposal_sha256"],
             "proposal": command["proposal"], "result": result,
             "recorded_at": datetime.now(timezone.utc).isoformat()}
    path = Path(directory) / "settings-audit.jsonl"
    # The per-resource execution lock serializes writers. Different resources
    # share this append lock, and each record is flushed before acknowledging.
    with integration_lock(Path(directory) / "audit.lock"):
        fd = os.open(str(path), os.O_APPEND | os.O_WRONLY | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
        with os.fdopen(fd, "ab") as out:
            out.write(canonical_bytes(event) + b"\n")
            out.flush()
            os.fsync(out.fileno())


def execute_settings_command(adapter, grant, command, directory, before_write=lambda: None):
    """At-least-once execution with a durable intent, reconciliation and audit."""
    command_id = command.get("id", "")
    if not re.fullmatch(r"[a-f0-9-]{36}", command_id):
        raise CLIError("Invalid command identity.")
    path = Path(directory) / "commands" / (command_id + ".json")
    identity = {"command_id": command_id, "proposal_digest": command["proposal_sha256"], "resource_key": adapter.key}
    with integration_lock(settings_lock_path(adapter)):
        journal = read_object(path, max_bytes=4 << 20)
        if journal and journal.get("identity") != identity:
            raise CLIError("Command journal identity mismatch.")

        def finish(status, result):
            journal.update(identity=identity, status=status, result=result)
            private_json(path, journal)
            if not journal.get("audit_recorded"):
                append_local_audit(directory, command, status, result)
                journal["audit_recorded"] = True
                private_json(path, journal)
            return status, result

        if journal.get("status"):
            return finish(journal["status"], journal["result"])
        proposal = command["proposal"]
        prepared = journal.get("prepared")
        if not prepared and command.get("attempts", 1) > 1 and proposal.get("operation") != "settings.refresh":
            return finish("unknown", {"message": "The earlier delivery has no local journal. No mutation was repeated."})
        if not prepared:
            try:
                prepared = adapter.prepare(proposal, grant)
            except SettingsConflict:
                return finish("conflicted", {"message": "Settings or their resolution context changed. Refresh and review again."})
            except (CLIError, ValueError, OSError):
                return finish("rejected", {"message": "The local adapter rejected this proposal. Refresh to inspect supported fields and policy."})
            journal.update(identity=identity, prepared=prepared)
            private_json(path, journal)
        if proposal["operation"] == "settings.refresh":
            return finish("succeeded", {"message": "Read current saved settings.", "version": prepared["view"]["snapshot"]["version"], "effects": [], "snapshot_publication": "pending"})
        reconciled = bool(journal.get("write_started"))
        try:
            if not adapter.matches_prepared(prepared):
                if reconciled or command.get("attempts", 1) > 1:
                    return finish("unknown", {"message": "The earlier write cannot be established from current settings. It was not repeated."})
                # Renew against the current connector instance immediately
                # before crossing into the actual file/native writer.
                before_write()
                current = adapter.snapshot(grant)
                if current["snapshot"]["version"] != prepared["view"]["snapshot"]["version"]:
                    return finish("conflicted", {"message": "Settings changed immediately before execution."})
                before_write()
                journal["write_started"] = True
                private_json(path, journal)
                adapter.apply_prepared(prepared)
            after = adapter.snapshot(grant)
        except NativeRPCError as err:
            code = (err.error.get("data") or {}).get("config_write_error_code")
            if code == "configVersionConflict":
                return finish("conflicted", {"message": "Codex rejected a stale native configuration version."})
            if code in ("configLayerReadonly", "configValidationError", "configPathNotFound"):
                return finish("rejected", {"message": "The native configuration API rejected the write. Inspect current native requirements locally."})
            return finish("unknown", {"message": "The native API reported an error with an uncertain write outcome. No mutation will be repeated automatically."})
        except SettingsConflict:
            return finish("conflicted", {"message": "The local configuration file changed before writing."})
        except (CLIError, OSError):
            # Keep intent unresolved: a subsequent claim reconciles without
            # automatically repeating a possibly completed native write.
            raise
        fields = {field["key"]: field for field in prepared["view"]["descriptor"]["fields"]}
        effects = [{"key": edit["key"], "saved": True, "runtime_applied": False,
                    "effective_when": fields[edit["key"]]["effective_when"]} for edit in proposal["edits"]]
        return finish("succeeded", {"previous_version": prepared["view"]["snapshot"]["version"],
                                     "version": after["snapshot"]["version"], "effects": effects,
                                     "reconciled": reconciled, "snapshot_publication": "pending"})


def validate_registration(provider, project, session_id, transcript):
    project = Path(project).resolve()
    transcript = Path(transcript).expanduser()
    if transcript.is_symlink():
        raise CLIError("Transcript symlinks are not captured.")
    transcript = transcript.resolve()
    if not session_id or len(session_id) > 200 or any(ord(c) < 32 for c in session_id):
        raise CLIError("Invalid native session identity.")
    with regular_source(transcript) as (source, size):
        first = next(committed_jsonl(source, min(size, 16 << 20)), None)
    if not first:
        raise CLIError("The native session has no complete records yet.")
    record = json.loads(first)
    if provider == "codex":
        payload = record.get("payload") or {}
        if record.get("type") != "session_meta" or payload.get("id") != session_id:
            raise CLIError("Codex rollout identity does not match the requested session.")
        if Path(payload.get("cwd", "")).resolve() != project:
            raise CLIError("Codex rollout belongs to a different project.")
        version = str(payload.get("cli_version") or "unknown")
    else:
        version = str(record.get("version") or "unknown")
        # Some Claude transcripts start with file-history metadata without a
        # session ID. When the record has one, it must agree with the hook.
        if record.get("sessionId") and record["sessionId"] != session_id:
            raise CLIError("Claude transcript identity does not match its hook.")
    return {"provider": provider, "project": str(project), "session_id": session_id,
            "transcript": str(transcript), "native_version": version}


def register_native_session(provider, project, session_id, transcript):
    registration = validate_registration(provider, project, session_id, transcript)
    directory = integration_path(provider, project)
    key = sha256(session_id.encode())
    private_json(directory / "sessions" / (key + ".json"), registration)
    return registration


def notify_claude_artifact(payload):
    """Hooks only register/wake. HTTP and archival never block model hooks."""
    project = str(Path(payload.get("cwd") or os.getcwd()).resolve())
    directory = integration_path("claude-code", project)
    config = read_object(directory / "integration.json")
    if not config.get("artifacts") or not config.get("enabled", True):
        return
    transcript, session_id = payload.get("transcript_path"), payload.get("session_id")
    if transcript and session_id and Path(transcript).exists():
        register_native_session("claude-code", project, str(session_id), str(transcript))
    # ConfigChange is an observation, not an acknowledgement that an unrelated
    # Claude process adopted a particular model or effort setting.
    if payload.get("hook_event_name") == "ConfigChange":
        private_json(directory / "last-config-observation.json", {
            "event": "ConfigChange", "session_id": session_id,
            "observed_at": datetime.now(timezone.utc).isoformat()})
    start_integration_companion("claude-code", project)


def discover_codex_sessions(project, directory, native_root):
    """Opted-in project scan, matching the native header before registration."""
    project = Path(project).resolve()
    root = Path(native_root).resolve()
    known = read_object(Path(directory) / "discovery.json")
    changed = False
    for folder in (root / "sessions", root / "archived_sessions"):
        if not folder.is_dir() or folder.is_symlink():
            continue
        count = 0
        for current, dirs, files in os.walk(folder, followlinks=False):
            dirs[:] = [d for d in dirs if not (Path(current) / d).is_symlink()]
            for name in files:
                if not name.endswith(".jsonl"):
                    continue
                count += 1
                if count > 50000:
                    raise CLIError("Too many native rollouts to scan; register a session explicitly.")
                path = Path(current) / name
                if path.is_symlink():
                    continue
                # An unmatched existing session cannot later change projects;
                # record its inode, not its growing transcript contents.
                info = path.stat()
                identity = str(info.st_ino) + ":" + str(info.st_dev)
                if known.get(str(path)) == identity:
                    continue
                with path.open("rb") as source:
                    raw = source.readline((1 << 20) + 1)
                if len(raw) > 1 << 20 or not raw.endswith(b"\n"):
                    continue
                try:
                    event = json.loads(raw)
                    payload = event.get("payload") or {}
                    if event.get("type") == "session_meta" and payload.get("cwd") and Path(payload["cwd"]).resolve() == project:
                        register_native_session("codex", project, str(payload["id"]), path)
                except (ValueError, KeyError, CLIError):
                    continue
                known[str(path)] = identity
                changed = True
    if changed:
        # Avoid an unbounded local registry on machines with years of history.
        if len(known) > 20000:
            known = dict(list(known.items())[-20000:])
        private_json(Path(directory) / "discovery.json", known)


def registration_signature(registration, settings, directory):
    native = Path(registration["transcript"])
    sources = [native]
    subagents = native.parent / native.stem / "subagents"
    if registration["provider"] == "claude-code" and subagents.is_dir():
        sources.extend(sorted(subagents.glob("*.jsonl")))
    audit = Path(directory) / "settings-audit.jsonl"
    if audit.exists():
        sources.append(audit)
    stats = [(str(p), p.stat().st_size, p.stat().st_mtime_ns) for p in sources]
    return sha256(canonical_bytes([stats, settings["snapshot"]["version"] if settings else "unavailable", VERSION, INTEGRATION_ASSETS_DIGEST, INTEGRATION_ADAPTER_DIGEST]))


def publish_registered_session(client, registration, directory, settings, recreate=False):
    session_dir = Path(directory) / "publications" / sha256(registration["session_id"].encode())
    return publish_capture(client, session_dir, "ext:" + registration["provider"] + ":" + registration["session_id"],
                           "Session explorer", "session",
                           lambda stage: build_native_export(stage, registration, settings, Path(directory) / "settings-audit.jsonl"),
                           lambda: registration_signature(registration, settings, directory), True, recreate)


def publish_capture(client, session_dir, thread_ref, title, key, capture, current_signature, check_sources, recreate=False):
    """Shared durable publication lifecycle for native and third-party websites."""
    session_dir = Path(session_dir)
    session_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    with integration_lock(session_dir / "publisher.lock", timeout=0):
        state_path = session_dir / "state.json"
        state = read_object(state_path)
        pending = session_dir / "pending.json"
        intent = read_object(pending)
        if recreate:
            old_id = intent.get("artifact_id") or state.get("artifact_id")
            if not old_id:
                raise CLIError("This session has no deleted publication to recreate.")
            try:
                publication_head(client, old_id)
            except PublicationDeleted:
                # Keep the old identity and any unfinished capture as evidence.
                private_json(session_dir / ("retired-" + sha256(old_id.encode()) + ".json"), {"state": state, "pending": intent})
                private_json(state_path, {})
                private_json(pending, {})
                state, intent = {}, {}
            else:
                raise CLIError("The existing artifact still exists; --recreate cannot bypass a history conflict.")
        if state.get("remote_deleted"):
            raise PublicationDeleted()

        def complete(stage, signature):
            result = upload_website(client, stage, pending, thread_ref, title, artifact_id=state.get("artifact_id"), signature=signature, check_sources=check_sources, key=key)
            record = {"signature": signature, "artifact_id": result["artifact_id"], "revision_id": result["revision_id"],
                      "thread_id": result.get("thread_id"), "settings_version": verify_website(stage)["dataset"].get("settings_version")}
            private_json(state_path, record)
            pending.unlink()
            shutil.rmtree(stage)
            private_json(session_dir / "status.json", {"state": "published", "revision_id": record["revision_id"]})
            return record

        try:
            old_stage = None
            if intent:
                old_stage = Path(intent["directory"])
                if old_stage.parent.resolve() != session_dir.resolve() or old_stage.is_symlink():
                    raise CLIError("Publication journal points outside its private staging directory.")
                if not intent.get("needs_recapture"):
                    try:
                        state = complete(old_stage, intent.get("signature", ""))
                        intent, old_stage = {}, None
                    except PublicationConflict:
                        intent["needs_recapture"] = True
                        private_json(pending, intent)
            signature = current_signature()
            if not intent and state.get("signature") == signature:
                return state
            stage = Path(tempfile.mkdtemp(prefix="capture-", dir=str(session_dir)))
            try:
                manifest = capture(stage)
                if intent:
                    # The replacement must preserve both the pending capture
                    # and the published source frontier. Never just rebase an
                    # old manifest onto the newer server head.
                    if check_sources:
                        require_source_extension(stage, manifest, verify_website(old_stage))
                    replacement = publication_intent(client, stage, manifest, thread_ref, title, intent["artifact_id"], signature, check_sources, key)
                    private_json(pending, replacement)
                    # New immutable source bytes and intent are durable first.
                    shutil.rmtree(old_stage)
                return complete(stage, signature)
            except BaseException:
                saved = read_object(pending)
                if saved.get("directory") != str(stage):
                    shutil.rmtree(stage)
                raise
        except PublicationDeleted as error:
            latest = read_object(pending)
            state.update(artifact_id=latest.get("artifact_id") or state.get("artifact_id") or error.artifact_id, remote_deleted=True)
            private_json(state_path, state)
            private_json(session_dir / "status.json", {"state": "remote_deleted", "message": "Explicit --recreate is required to resume this session's publication."})
            raise
        except PublicationConflict as error:
            private_json(session_dir / "status.json", {"state": "conflicted", "message": str(error)})
            raise


def publish_integration_pass(provider, project, client, directory):
    config = read_object(Path(directory) / "integration.json")
    if not config.get("artifacts"):
        return
    adapter = None
    try:
        settings = None
        native_root = os.environ.get("CODEX_HOME") or str(Path.home() / ".codex")
        try:
            adapter = make_settings_adapter(provider, project, config.get("primary_scope"))
            settings = adapter.snapshot()
            if provider == "codex":
                native_root = adapter.rpc.info["codexHome"]
        except (CLIError, OSError, ValueError):
            hook_log(f"{provider} settings snapshot unavailable; preserving native session sources independently.")
        if provider == "codex":
            discover_codex_sessions(project, directory, native_root)
        for path in sorted((Path(directory) / "sessions").glob("*.json")):
            registration = read_object(path)
            try:
                publish_registered_session(client, registration, directory, settings)
            except (CLIError, OSError, ValueError) as err:
                hook_log(f"artifact publication deferred ({provider}, {registration.get('session_id')}): {type(err).__name__}")
    finally:
        if adapter:
            adapter.close()


def start_integration_companion(provider, project, client=None):
    directory = integration_path(provider, project)
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    # The child acquires the process lock itself, so simultaneous hooks do
    # not elect multiple owners. Do not kill PIDs from stale state files.
    try:
        with integration_lock(directory / "companion.lock", timeout=0):
            pass
    except CLIError:
        return False
    env = os.environ.copy()
    if client:
        env["FINALECHAT_URL"] = client.base_url
        if client.token:
            env["FINALECHAT_TOKEN"] = client.token
    with (directory / "companion.log").open("ab") as log:
        os.chmod(directory / "companion.log", 0o600)
        subprocess.Popen([sys.executable, cli_path(), "connector", "run", provider, "--project", str(project)],
                         stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True,
                         close_fds=True, env=env)
    return True


def run_integration_companion(provider, project, client, once=False):
    directory = integration_path(provider, project)
    stop = threading.Event()
    instance = secrets.token_hex(24)
    with integration_lock(directory / "companion.lock", timeout=0):
        def publish_loop():
            while not stop.is_set():
                config = read_object(directory / "integration.json")
                if not config.get("enabled", True):
                    return
                try:
                    publish_integration_pass(provider, project, client, directory)
                except (CLIError, OSError, ValueError):
                    hook_log(f"{provider} artifact publisher will retry.")
                if once or stop.wait(30):
                    return

        publisher = threading.Thread(target=publish_loop, daemon=True)
        publisher.start()
        adapters = {}
        try:
            while not stop.is_set():
                config = read_object(directory / "integration.json")
                if not config.get("enabled", True):
                    break
                pairing = read_object(directory / "connector.json")
                if not pairing:
                    if once:
                        break
                    stop.wait(5)
                    continue
                control_client = Client(pairing["base_url"], pairing["secret"])
                base = "/api/v1/connectors/" + Client.ref_path(pairing["connector"]["id"])
                try:
                    connector = control_client.request("GET", base)["connector"]
                    if connector["state"] != "active":
                        if once:
                            break
                        stop.wait(5)
                        continue
                    control_client.request("POST", base + "/heartbeat", body={"instance": instance})
                    resources, grants = {}, {}
                    for grant in connector["grants"]:
                        if grant["key"] not in {g["key"] for g in pairing["connector"]["requested_grants"]}:
                            raise CLIError("Server resource does not match the local pairing.")
                        adapter = adapters.get(grant["key"])
                        if adapter is None:
                            adapter = make_settings_adapter(provider, project, grant["scope"])
                            if adapter.key != grant["key"]:
                                adapter.close()
                                raise CLIError("Local resource identity changed; pair it again.")
                            adapters[grant["key"]] = adapter
                        view = adapter.snapshot(grant)
                        resource = control_client.request("PUT", base + "/resources/" + Client.ref_path(adapter.key), body={
                            "instance": instance, "generation": "", "descriptor": view["descriptor"], "snapshot": view["snapshot"]})["resource"]
                        resources[resource["id"]] = adapter
                        grants[resource["id"]] = grant
                        if grant["scope"] == config.get("primary_scope", "project" if provider == "claude-code" else "user"):
                            for state_path in (directory / "publications").glob("*/state.json"):
                                publication = read_object(state_path)
                                try:
                                    control_client.request("PUT", base + "/bindings/" + publication["artifact_id"], body={
                                        "resource_id": resource["id"], "revision_id": publication["revision_id"]})
                                except APIError as err:
                                    if err.status not in (404, 409):
                                        raise
                    response = control_client.request("POST", base + "/commands/claim", body={"instance": instance},
                                                      params={"wait": 0 if once else 20}, timeout=30, retries=0)
                    command = response.get("command")
                    if command:
                        adapter = resources.get(command["resource_id"])
                        if adapter is None:
                            raise CLIError("Claimed command has no locally registered resource.")
                        claim = {"instance": instance, "claim_token": command["claim_token"]}

                        def renew():
                            control_client.request("POST", base + "/heartbeat", body={"instance": instance}, retries=0)
                            control_client.request("POST", "/api/v1/commands/" + command["id"] + "/renew", body=claim, retries=0)

                        renew()
                        lease_stop, lease_lost = threading.Event(), threading.Event()

                        def keep_lease():
                            while not lease_stop.wait(15):
                                try:
                                    renew()
                                except (CLIError, OSError):
                                    lease_lost.set()
                                    return

                        def check_lease():
                            if lease_lost.is_set():
                                raise CLIError("The command lease was lost before writing.")
                            renew()

                        keeper = threading.Thread(target=keep_lease, daemon=True)
                        keeper.start()
                        try:
                            status, result = execute_settings_command(adapter, grants[command["resource_id"]], command, directory, before_write=check_lease)
                            if lease_lost.is_set():
                                raise CLIError("The command lease was lost; its durable result awaits reconciliation.")
                            control_client.request("POST", "/api/v1/commands/" + command["id"] + "/result", body=dict(claim, status=status, result=result), retries=0)
                        finally:
                            lease_stop.set()
                            keeper.join(timeout=35)
                except (CLIError, OSError, ValueError):
                    # Reconnect native transports after failures. Keep this
                    # elected instance stable across transient HTTP outages.
                    for adapter in adapters.values():
                        adapter.close()
                    adapters.clear()
                    hook_log(f"{provider} settings connector will retry; any unresolved intent remains journaled.")
                    if not once:
                        stop.wait(5)
                if once:
                    break
        finally:
            if once:
                publisher.join(timeout=60)
            stop.set()
            publisher.join(timeout=5)
            for adapter in adapters.values():
                adapter.close()


def configure_integration(provider, project, **changes):
    directory = integration_path(provider, project)
    with integration_lock(directory / "configuration.lock"):
        path = directory / "integration.json"
        config = read_object(path)
        config.update(provider=provider, project=str(Path(project).resolve()))
        config.update(changes)
        private_json(path, config)
    return config


def directory_inventory(directory):
    """Bounded inventory of an explicitly prepared website, without symlinks."""
    root = Path(directory)
    if root.is_symlink() or not root.is_dir():
        raise CLIError("The website must be a real directory.")
    records = []
    for parent, directories, files in os.walk(root, followlinks=False):
        for name in sorted(directories + files):
            path = Path(parent) / name
            relative = path.relative_to(root).as_posix()
            info = path.lstat()
            if not portable_path(relative) or stat.S_ISLNK(info.st_mode):
                raise CLIError(f"Unsafe website path: {relative}")
            if stat.S_ISREG(info.st_mode):
                if info.st_size > ARTIFACT_MAX_FILE:
                    raise CLIError(f"Website file is too large: {relative}")
                records.append((relative, info.st_size, info.st_mtime_ns, info.st_ctime_ns, info.st_ino))
            elif not stat.S_ISDIR(info.st_mode):
                raise CLIError(f"Website contains a non-regular file: {relative}")
            if len(records) > 4097:
                raise CLIError("Website contains too many files.")
    return sorted(records)


def capture_directory(source, target, options):
    """Capture bytes into private staging before any account upload begins."""
    root = Path(source).resolve()
    before = directory_inventory(source)
    if (root / "manifest.json").exists():
        if options.get("source") or options.get("context") or options.get("dataset") is not None or options.get("settings_entrypoint") or options.get("entrypoint", "index.html") != "index.html" or options.get("producer", "custom-integration") != "custom-integration" or options.get("producer_version", "1") != "1":
            raise CLIError("A manifested archive already defines its files, roles, dataset and entrypoints; omit packaging overrides.")
        manifest = verify_website(root)
        for record in manifest["files"]:
            destination = Path(target) / record["path"]
            destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            recover_source_file(root, record, destination)
        private_json(Path(target) / "manifest.json", manifest)
    else:
        export = WebsiteExport(target, options.get("producer", "custom-integration"), "", "")
        dataset = options.get("dataset", {"format": "custom.website/v1"})
        if not isinstance(dataset, dict):
            raise CLIError("Website dataset metadata must be a JSON object.")
        entrypoint = options.get("entrypoint", "index.html")
        settings_entrypoint = options.get("settings_entrypoint")
        export.manifest.update(dataset=dataset, entrypoint=entrypoint, viewer={})
        export.manifest["producer"]["version"] = options.get("producer_version", "1")
        if settings_entrypoint:
            export.manifest["settings_entrypoint"] = settings_entrypoint
        else:
            export.manifest.pop("settings_entrypoint", None)
        source_paths, context_paths = set(options.get("source") or []), set(options.get("context") or [])
        entries = {p for p in (entrypoint, settings_entrypoint) if p}
        names = {record[0] for record in before}
        if source_paths & context_paths or (source_paths | context_paths) & entries:
            raise CLIError("Each website file must have one role; entrypoints are viewers.")
        if not (source_paths | context_paths | entries) <= names:
            raise CLIError("A declared source, context file or entrypoint is missing.")
        for name, size, mtime, ctime, inode in before:
            role = "source" if name in source_paths else "context" if name in context_paths else "viewer" if name in entries else "asset"
            content_type = "text/html" if name in entries else ("application/x-ndjson" if name.endswith(".jsonl") else mimetypes.guess_type(name)[0] or "application/octet-stream")
            with regular_source(root / name) as (stream, opened_size):
                def parts():
                    remaining = opened_size
                    while remaining:
                        raw = stream.read(min(remaining, ARTIFACT_CHUNK))
                        if not raw:
                            raise CLIError("Website changed during capture; retry after the producer finishes writing.")
                        remaining -= len(raw)
                        yield raw
                export.add(name, role, content_type, parts())
                info = os.fstat(stream.fileno())
                if (info.st_size, info.st_mtime_ns, info.st_ctime_ns, info.st_ino) != (size, mtime, ctime, inode):
                    raise CLIError("Website changed during capture; retry after the producer finishes writing.")
        manifest = export.finish()
    if directory_inventory(source) != before:
        raise CLIError("Website changed during capture; retry after the producer finishes writing.")
    return verify_website(target)


def cmd_website(args):
    source = Path(args.directory).expanduser()
    options = {name: getattr(args, name) for name in ("entrypoint", "settings_entrypoint", "producer", "producer_version", "source", "context")}
    if args.dataset:
        dataset_path = Path(args.dataset).expanduser()
        if not dataset_path.is_file():
            raise CLIError("Dataset metadata file does not exist.")
        options["dataset"] = read_object(dataset_path)
    if args.artifact_command == "pack":
        target = Path(args.output).expanduser()
        if target.resolve().is_relative_to(source.resolve()):
            raise CLIError("The output directory must be outside the website being captured.")
        target.mkdir(parents=True, exist_ok=False, mode=0o700)
        try:
            manifest = capture_directory(source, target, options)
        except BaseException:
            shutil.rmtree(target)
            raise
        print(f"Packed {len(manifest['files'])} files into {target}. Verify or upload this directory with finalechat artifact.")
        return EXIT_OK
    client = client_from_args(args)
    thread_ref = args.thread or os.environ.get("FINALECHAT_THREAD")
    if not thread_ref:
        raise CLIError("Website upload requires --thread or FINALECHAT_THREAD; choose the integration's exact session thread.")
    identity = sha256(canonical_bytes([client.base_url, thread_ref, args.key]))
    session_dir = INTEGRATION_DIR / "websites" / identity
    if session_dir.resolve().is_relative_to(source.resolve()):
        raise CLIError("The website cannot contain FinaleChat's private publication state.")
    def capture(stage):
        manifest = capture_directory(source, stage, options)
        if args.append_only_sources and not any(f["role"] == "source" for f in manifest["files"]):
            raise CLIError("--append-only-sources requires at least one source-role file.")
        return manifest
    result = publish_capture(client, session_dir, thread_ref, args.title, args.key, capture,
                             lambda: sha256(canonical_bytes([str(source.resolve()), directory_inventory(source), options, args.append_only_sources])),
                             args.append_only_sources, args.recreate)
    if args.json:
        print(json.dumps(result))
    else:
        print(f"Published {args.title} · artifact {result['artifact_id']} · revision {result['revision_id']}")
    return EXIT_OK


def cmd_artifact(args):
    if args.artifact_command in ("verify", "restore"):
        manifest = verify_website(args.directory)
        if args.artifact_command == "verify":
            print(f"Verified {len(manifest['files'])} files and their SHA-256 chunks.")
            return EXIT_OK
        target = Path(args.output).expanduser()
        target.mkdir(parents=True, exist_ok=False, mode=0o700)
        try:
            # Recover into a fresh directory. Do not install executable
            # settings, process files, credentials, or a live command binding.
            selected = [f for f in manifest["files"] if f["role"] == "source"]
            for record in selected:
                destination = target / record["path"]
                destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                recover_source_file(args.directory, record, destination)
            private_json(target / "recovery.json", {"format": "finalechat.source-recovery/v1", "dataset": manifest["dataset"],
                                                      "files": selected, "runtime_restored": False})
        except BaseException:
            shutil.rmtree(target)
            raise
        print(f"Recovered {len(selected)} native source files into {target}. No agent was started.")
        return EXIT_OK
    provider, project = args.provider, str(Path(args.project).resolve())
    directory = integration_path(provider, project)
    if args.artifact_command in ("enable", "disable"):
        config = configure_integration(provider, project, artifacts=args.artifact_command == "enable", enabled=True)
        if config["artifacts"]:
            start_integration_companion(provider, project, client_from_args(args))
        print(f"{provider} artifact publication {args.artifact_command}d for {project}.")
        return EXIT_OK
    if args.artifact_command == "publish":
        # Explicit one-shot publication is allowed without persistent opt-in.
        if args.recreate and not args.session:
            raise CLIError("--recreate requires one explicit native session ID.")
        client = client_from_args(args)
        adapter = None
        try:
            settings = None
            try:
                adapter = make_settings_adapter(provider, project)
                settings = adapter.snapshot()
            except (CLIError, OSError, ValueError):
                print("Settings are unavailable; publishing the native session sources independently.", file=sys.stderr)
            registrations = [read_object(p) for p in (directory / "sessions").glob("*.json")]
            if args.session:
                registrations = [r for r in registrations if r["session_id"] == args.session]
            if not registrations:
                raise CLIError("No tracked sessions. Use artifact track first.")
            for registration in registrations:
                result = publish_registered_session(client, registration, directory, settings, recreate=args.recreate)
                print(f"Published {registration['session_id']} · revision {result['revision_id']}")
        finally:
            if adapter:
                adapter.close()
        return EXIT_OK
    transcript = args.transcript
    if not transcript and provider == "codex":
        with CodexRPC(project) as rpc:
            thread = rpc.call("thread/read", {"threadId": args.session, "includeTurns": False})["thread"]
            transcript = thread.get("path")
            if not transcript:
                raise CLIError("This Codex thread has no native rollout path. Supply a supported JSONL capture with --transcript.")
            if thread.get("cwd") and Path(thread["cwd"]).resolve() != Path(project):
                raise CLIError("Native thread belongs to a different project; select that project explicitly.")
    if not transcript:
        raise CLIError("Claude Code export/track requires --transcript with the native session JSONL path.")
    registration = validate_registration(provider, project, args.session, transcript)
    if args.artifact_command == "track":
        register_native_session(provider, project, args.session, transcript)
        print(f"Tracking {provider}:{args.session}. Proactive upload is controlled by artifact enable/disable.")
        return EXIT_OK
    target = Path(args.output).expanduser()
    target.mkdir(parents=True, exist_ok=False, mode=0o700)
    adapter = None
    try:
        settings = None
        try:
            adapter = make_settings_adapter(provider, project)
            settings = adapter.snapshot()
        except (CLIError, OSError, ValueError):
            print("Settings are unavailable; exporting the native session sources independently.", file=sys.stderr)
        manifest = build_native_export(target, registration, settings, directory / "settings-audit.jsonl")
        verify_website(target)
    except BaseException:
        shutil.rmtree(target)
        raise
    finally:
        if adapter:
            adapter.close()
    print(f"Exported {len(manifest['files'])} files to {target}. Open index.html and choose this extracted directory.")
    return EXIT_OK


def cmd_connector(args):
    provider, project = args.provider, str(Path(args.project).resolve())
    directory = integration_path(provider, project)
    client = client_from_args(args)
    action = args.connector_command
    if action == "pair":
        scopes = list(dict.fromkeys(args.scope or (["project"] if provider == "claude-code" else ["user"])))
        adapters = []
        try:
            for scope in scopes:
                adapters.append(make_settings_adapter(provider, project, scope))
            grants = [adapter.grant() for adapter in adapters]
            # Validate local capabilities before creating a pending pairing.
            for adapter in adapters:
                adapter.snapshot()
            existing = read_object(directory / "connector.json")
            if existing:
                scoped = Client(existing["base_url"], existing["secret"])
                try:
                    prior = scoped.request("GET", "/api/v1/connectors/" + existing["connector"]["id"])["connector"]
                except APIError as err:
                    if err.status not in (401, 403, 404):
                        raise
                    prior = None
                if prior and prior["state"] in ("active", "pending"):
                    print("Existing pairing: " + existing["approval_url"])
                    print("Revoke it in FinaleChat before changing this installation's scopes.")
                    return EXIT_OK
            pairing = client.request("POST", "/api/v1/connectors", body={"name": provider + " · " + Path(project).name + " · " + hostname(),
                                      "provider": provider, "requested_grants": grants})
            pairing["base_url"] = client.base_url
            private_json(directory / "connector.json", pairing)
            configure_integration(provider, project, enabled=True, primary_scope=scopes[0])
            print("Approve the exact scopes and settings classes in FinaleChat:")
            print(pairing["approval_url"])
            if not args.foreground:
                start_integration_companion(provider, project, client)
        finally:
            for adapter in adapters:
                adapter.close()
        return EXIT_OK
    if action == "status":
        config = read_object(directory / "integration.json")
        pairing = read_object(directory / "connector.json")
        value = {"provider": provider, "project": project, "enabled": config.get("enabled", False), "artifacts": config.get("artifacts", False), "paired": bool(pairing)}
        value["publications"] = [dict(read_object(path), local_capture=path.parent.name) for path in sorted(directory.glob("publications/*/status.json"))]
        if pairing:
            scoped = Client(pairing["base_url"], pairing["secret"])
            remote = scoped.request("GET", "/api/v1/connectors/" + pairing["connector"]["id"])
            value.update(connector=remote["connector"], online=remote.get("online", False))
        print_json(value)
        return EXIT_OK
    if action == "stop":
        configure_integration(provider, project, enabled=False)
        print("Companion will stop after its current operation. Revoke its scopes in FinaleChat to unpair.")
        return EXIT_OK
    if action == "start":
        configure_integration(provider, project, enabled=True)
        started = start_integration_companion(provider, project, client)
        print("Companion started." if started else "Companion is already running.")
        return EXIT_OK
    run_integration_companion(provider, project, client, once=args.once)
    return EXIT_OK


def install_codex_integration(args, remove=False):
    executable = shutil.which("codex")
    if not executable:
        raise CLIError("Install Codex before registering its integration.")
    if not args.no_mcp:
        command = [executable, "mcp", "remove", "finalechat"] if remove else [executable, "mcp", "add", "finalechat", "--", sys.executable, cli_path(), "mcp"]
        result = subprocess.run(command, capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            raise CLIError("Codex MCP registration failed; run codex mcp list to inspect the local configuration.")
    if remove:
        configure_integration("codex", args.project, enabled=False, artifacts=False)
        print("Removed Codex MCP registration and stopped this project's companion.")
    else:
        print("Codex MCP integration installed. It provides chat tools; native rollouts provide the archive.")
        if args.artifacts:
            configure_integration("codex", args.project, enabled=True, artifacts=True)
            start_integration_companion("codex", args.project, client_from_args(args))
            print("Proactive native rollout publication enabled for " + str(Path(args.project).resolve()))
        print("Pair settings separately with: finalechat connector pair codex --project " + shlex.quote(str(Path(args.project).resolve())))
    return EXIT_OK


def add_integration_parsers(sub):
    def provider_project(parser):
        parser.add_argument("provider", choices=["claude-code", "codex"])
        parser.add_argument("--project", default=".", help="canonical project scope (default current directory)")

    root = sub.add_parser("artifact", help="export, verify, recover and publish permanent session websites")
    commands = root.add_subparsers(dest="artifact_command", required=True)
    for name in ("pack", "upload"):
        parser = commands.add_parser(name, help="package or publish an integration's prepared website directory")
        parser.add_argument("directory", help="prepared website directory, or an existing manifested archive")
        parser.add_argument("--entrypoint", default="index.html", help="self-contained HTML entrypoint")
        parser.add_argument("--settings-entrypoint", help="optional self-contained settings HTML")
        parser.add_argument("--producer", default="custom-integration")
        parser.add_argument("--producer-version", default="1")
        parser.add_argument("--dataset", help="JSON file containing dataset identity and provenance metadata")
        parser.add_argument("--source", action="append", help="relative file to preserve as a recoverable source; repeat as needed")
        parser.add_argument("--context", action="append", help="relative context data file; repeat as needed")
        if name == "pack":
            parser.add_argument("-o", "--output", required=True, help="new portable archive directory outside the input")
        else:
            parser.add_argument("-t", "--thread", help="exact thread UUID or ext:session reference; defaults only to FINALECHAT_THREAD")
            parser.add_argument("--key", default="session", help="stable artifact key within this thread")
            parser.add_argument("--title", default="Session explorer")
            parser.add_argument("--append-only-sources", action="store_true", help="require every published source file to remain a byte prefix of the next capture")
            parser.add_argument("--recreate", action="store_true", help="explicitly create a new record after remote deletion")
            parser.add_argument("--json", action="store_true", help="print durable artifact, revision and thread identifiers as JSON")
        parser.set_defaults(func=cmd_website)
    for name in ("enable", "disable", "track", "export", "publish"):
        parser = commands.add_parser(name)
        provider_project(parser)
        if name in ("track", "export"):
            parser.add_argument("session", help="native session ID")
            parser.add_argument("--transcript", help="native JSONL path; Codex can resolve its own thread ID")
        if name == "publish":
            parser.add_argument("session", nargs="?", help="tracked native session; omit for all tracked sessions here")
            parser.add_argument("--recreate", action="store_true", help="explicitly create a new remote record after deletion; requires a session ID")
        if name == "export":
            parser.add_argument("-o", "--output", required=True, help="new export directory")
        parser.set_defaults(func=cmd_artifact)
    for name in ("verify", "restore"):
        parser = commands.add_parser(name)
        parser.add_argument("directory", help="extracted archive directory containing manifest.json")
        if name == "restore":
            parser.add_argument("-o", "--output", required=True, help="new directory for verified native source recovery")
        parser.set_defaults(func=cmd_artifact)
    root = sub.add_parser("connector", help="pair and supervise scoped settings control and artifact publication")
    commands = root.add_subparsers(dest="connector_command", required=True)
    for name in ("pair", "start", "run", "stop", "status"):
        parser = commands.add_parser(name)
        provider_project(parser)
        if name == "pair":
            parser.add_argument("--scope", choices=["user", "project", "project_local"], action="append", help="repeat for multiple resources; defaults to Claude project / Codex user")
            parser.add_argument("--foreground", action="store_true", help="only pair; run the companion under your supervisor separately")
        if name == "run":
            parser.add_argument("--once", action="store_true", help="one publication/control pass, useful for supervision checks")
        parser.set_defaults(func=cmd_connector)
