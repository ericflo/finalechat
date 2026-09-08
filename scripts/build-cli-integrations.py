#!/usr/bin/env python3
"""Embed maintainable integration sources/assets into the standalone CLI."""
import argparse
import ast
import base64
import gzip
import hashlib
import json
import os
from pathlib import Path
import tempfile

ROOT = Path(__file__).resolve().parents[1]
START = "# BEGIN GENERATED INTEGRATIONS (scripts/build-cli-integrations.py)"
END = "# END GENERATED INTEGRATIONS"


def build():
    assets = {"sdk": ROOT / "sdk/finale-artifact.js", "viewer": ROOT / "cli/assets/viewer.html",
              "settings": ROOT / "cli/assets/settings.html"}
    encoded = {name: base64.b64encode(gzip.compress(path.read_bytes(), mtime=0)).decode() for name, path in assets.items()}
    digest = hashlib.sha256(json.dumps(encoded, sort_keys=True).encode()).hexdigest()
    source = (ROOT / "cli/integrations.py").read_text()
    adapter_digest = hashlib.sha256(source.encode()).hexdigest()
    block = START + "\nINTEGRATION_ASSETS = " + repr(encoded) + "\nINTEGRATION_ASSETS_DIGEST = " + repr(digest) + "\nINTEGRATION_ADAPTER_DIGEST = " + repr(adapter_digest) + "\n" + source + "\n" + END
    path = ROOT / "cli/finalechat"
    current = path.read_text()
    if START in current:
        if in_sync(current, source, assets):
            # Compressed bytes differ between zlib and Python releases; when
            # the embedded content already matches the sources, keep the
            # committed encoding rather than churning it per toolchain.
            return path, current, current
        start, end = current.index(START), current.index(END) + len(END)
        result = current[:start] + block + current[end:]
    else:
        marker = "# --------------------------------------------------------------------------- #\n# Argument parsing"
        result = current.replace(marker, block + "\n\n\n" + marker)
    return path, current, result


def in_sync(current, source, assets):
    """Report whether the generated block in `current` already embeds exactly
    these sources, judged by decoded content rather than compressed bytes."""
    if START not in current or END not in current:
        return False
    block = current[current.index(START) + len(START) + 1:current.index(END)]
    parts = block.split("\n", 3)
    if len(parts) < 4:
        return False
    prefixes = ("INTEGRATION_ASSETS = ", "INTEGRATION_ASSETS_DIGEST = ", "INTEGRATION_ADAPTER_DIGEST = ")
    if not all(line.startswith(prefix) for line, prefix in zip(parts, prefixes)):
        return False
    try:
        encoded, digest, adapter_digest = (ast.literal_eval(line[len(prefix):]) for line, prefix in zip(parts, prefixes))
        if not isinstance(encoded, dict) or set(encoded) != set(assets):
            return False
        for name, asset in assets.items():
            if gzip.decompress(base64.b64decode(encoded[name])) != asset.read_bytes():
                return False
    except (ValueError, SyntaxError, OSError):
        return False
    if digest != hashlib.sha256(json.dumps(encoded, sort_keys=True).encode()).hexdigest():
        return False
    if adapter_digest != hashlib.sha256(source.encode()).hexdigest():
        return False
    return parts[3] == source + "\n"


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    path, current, result = build()
    if args.check:
        if current != result:
            raise SystemExit("Run python3 scripts/build-cli-integrations.py to synchronize the single-file CLI.")
    elif current != result:
        # Readers include the Go embed compiler and a running local server.
        # Never truncate their executable while publishing a generated build.
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".finalechat-build-", delete=False) as output:
            temporary = Path(output.name)
            try:
                os.fchmod(output.fileno(), path.stat().st_mode & 0o777)
                output.write(result.encode())
                output.flush()
                os.fsync(output.fileno())
            except BaseException:
                temporary.unlink(missing_ok=True)
                raise
        try:
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)
