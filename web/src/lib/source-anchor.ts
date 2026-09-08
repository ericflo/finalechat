import type { ArtifactManifest } from "./artifacts";
import type { Message, Thread } from "./types";

export interface SourceAnchor { dataset_format: string; session_id: string; seq?: number; event_id?: string; file?: string; line?: number; message_id?: string }
export function validateAnchor(raw: unknown, manifest: ArtifactManifest): SourceAnchor {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("Invalid source anchor.");
  const a = raw as Record<string, unknown>;
  if (Object.keys(a).some(k => !["dataset_format", "session_id", "seq", "event_id", "file", "line", "message_id"].includes(k))) throw new Error("Unsupported source anchor.");
  const text = (v: unknown) => typeof v === "string" && v.length > 0 && new TextEncoder().encode(v).byteLength <= 200 && !/[\u0000-\u001f\u007f-\u009f]/.test(v);
  const positive = (v: unknown) => typeof v === "number" && Number.isSafeInteger(v) && v > 0;
  if (!text(a.dataset_format) || !text(a.session_id) || a.dataset_format !== manifest.dataset.format || a.session_id !== manifest.dataset.session_id) throw new Error("This event belongs to another dataset.");
  let selectors = 0;
  if (a.seq !== undefined) { if (!positive(a.seq)) throw new Error("Invalid event sequence."); selectors++; }
  for (const k of ["event_id", "message_id"]) if (a[k] !== undefined) { if (!text(a[k])) throw new Error("Invalid event identifier."); selectors++; }
  if (a.file !== undefined || a.line !== undefined) { const f = manifest.files.find(f => f.path === a.file && f.role === "source"); if (!f || !positive(a.line) || (a.line as number) > f.size) throw new Error("This line is outside the archived source files."); selectors++; }
  if (selectors !== 1) throw new Error("Choose one event sequence, identifier, message or source line.");
  return structuredClone(a) as unknown as SourceAnchor;
}

export function sourceForMessage(message: Message, thread: Thread, manifest: ArtifactManifest): SourceAnchor | null {
  let raw: unknown = message.meta.source_anchor;
  if (!raw) {
    const session = manifest.dataset.session_id, format = manifest.dataset.format;
    if (format === "eagent.session-jsonl/v1" && thread.external_id === `eagent:${session}` && typeof message.meta.seq === "number") raw = { dataset_format: format, session_id: session, seq: message.meta.seq };
    else if (format === "claude-code.native-jsonl/v1" && thread.external_id === `claude-code:${session}` && message.meta.transcript_uuid) raw = { dataset_format: format, session_id: session, event_id: message.meta.transcript_uuid };
    else raw = { dataset_format: format, session_id: session, message_id: message.id };
  }
  try { return validateAnchor(raw, manifest); } catch { return null; }
}
