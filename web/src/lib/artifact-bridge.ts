import { fileURL, type ArtifactRevision } from "./artifacts";

type Handler = (params: unknown) => unknown | Promise<unknown>;

/** The parent creates a fresh channel for one iframe document, so a page
 * cannot acquire a bridge merely by sending messages with origin "null". */
export function connectArtifactFrame(frame: HTMLIFrameElement, artifactID: string, revision: ArtifactRevision, handlers: Record<string, Handler> = {}, viewer?: string | null) {
  const target = frame.contentWindow;
  if (!target) throw new Error("Viewer is unavailable.");
  const channel = new MessageChannel();
  const nonce = crypto.randomUUID();
  const abort = new AbortController();
  let closed = false, active = 0, budget = 240, budgetAt = Date.now();
  const seen = new Set<string>();
  const builtins: Record<string, Handler> = {
    "artifact.manifest": () => structuredClone(revision.manifest),
    "artifact.read": async (raw) => {
      const p = raw as { path?: unknown; offset?: unknown; length?: unknown } | null;
      if (!p || typeof p.path !== "string") throw new Error("A file path is required.");
      const file = revision.manifest.files.find((f) => f.path === p.path);
      if (!file) throw new Error("File is outside this revision.");
      const offset = p.offset === undefined ? 0 : p.offset;
      const length = p.length === undefined ? file.size : p.length;
      if (typeof offset !== "number" || typeof length !== "number" || !Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 0 || length > 1048576 || offset + length > file.size) throw new Error("Read up to 1 MiB at a time within the file.");
      const source = fileURL(artifactID, revision.id, file.path, viewer);
      const res = await fetch(`${source}${viewer ? "&" : "?"}offset=${offset}&length=${length}`, { credentials: "same-origin", signal: abort.signal, cache: "no-store" });
      if (!res.ok) throw new Error(`Could not read the archived file (${res.status}).`);
      const bytes = await res.arrayBuffer();
      if (bytes.byteLength !== length) throw new Error("Incomplete archived file.");
      return bytes;
    },
    ...handlers,
  };
  channel.port1.onmessage = async (event) => {
    const m = event.data as { nonce?: unknown; id?: unknown; method?: unknown; params?: unknown } | null;
    if (closed || !m || m.nonce !== nonce || typeof m.id !== "string" || m.id.length > 80 || typeof m.method !== "string") return;
    if (Date.now() - budgetAt > 60000) { budget = 240; budgetAt = Date.now(); seen.clear(); }
    const id = m.id;
    const reply = (value: unknown, error?: string) => {
      if (closed) return;
      channel.port1.postMessage({ nonce, id, result: value, error }, value instanceof ArrayBuffer ? [value] : []);
    };
    if (seen.has(id)) return;
    let size: number;
    try { size = JSON.stringify(m).length; } catch { reply(null, "Invalid viewer request."); return; }
    if (active >= 4 || --budget < 0 || size > 70000) { reply(null, "Viewer request limit reached."); return; }
    seen.add(id); active++;
    try {
      const fn = Object.hasOwn(builtins, m.method) ? builtins[m.method] : undefined;
      if (!fn) throw new Error("This viewer has no permission for that operation.");
      reply(await fn(m.params));
    } catch (error) { reply(null, error instanceof Error ? error.message : "Viewer request failed."); }
    finally { active--; }
  };
  channel.port1.start();
  target.postMessage({ type: "finalechat.artifact.connect", version: 1, nonce, artifact_id: artifactID, revision_id: revision.id }, "*", [channel.port2]);
  return {
    notify: (name: string, detail: unknown) => { if (!closed) channel.port1.postMessage({ nonce, event: name, detail }); },
    close: () => { closed = true; abort.abort(); channel.port1.close(); channel.port2.close(); },
  };
}
