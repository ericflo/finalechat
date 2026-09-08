import { request } from "./api";

export interface ArtifactFile { path: string; role: "viewer" | "source" | "asset" | "derived" | "context"; content_type: string; size: number; sha256: string; chunks: { sha256: string; size: number }[] }
export interface ArtifactManifest { format: string; producer: { name: string; version: string }; entrypoint: string; settings_entrypoint?: string; captured_at: string; dataset: Record<string, unknown>; viewer?: Record<string, unknown>; files: ArtifactFile[] }
export interface Artifact { id: string; thread_id: string; key: string; title: string; current_revision_id: string | null; updated_at: string; created_at: string }
export interface ArtifactRevision { id: string; artifact_id: string; manifest: ArtifactManifest; manifest_sha256: string; created_at: string }
export interface RevisionInfo { id: string; created_at: string; captured_at: string; producer: ArtifactManifest["producer"]; dataset: Record<string, unknown> }
export interface Grant { key: string; label: string; scope: string; operations: string[]; classes: string[] }
export interface Connector { id: string; name: string; provider: string; state: "pending" | "active" | "revoked"; requested_grants: Grant[]; grants: Grant[]; last_seen_at: string | null; expires_at: string }
export interface Shape { type: "string" | "boolean" | "integer" | "number" | "array" | "object"; enum?: unknown[]; minimum?: number; maximum?: number; max_length?: number; items?: Shape; properties?: Record<string, Shape>; required?: string[] }
export interface SettingField { key: string; label: string; description?: string; schema: Shape; writable: boolean; unset: boolean; locked_reason?: string; class: string; effective_when: string; source?: string }
export interface SettingsDescriptor { format: string; schema_version: string; adapter_version: string; fields: SettingField[]; actions?: { operation: string; label: string; class: string; parameters: Shape }[] }
export interface SettingsSnapshot { version: string; context: string; saved: Record<string, unknown>; effective: Record<string, unknown>; runtime_known: boolean; runtime_version?: string; details?: Record<string, unknown> }
export interface SettingsResource { id: string; connector_id: string; key: string; label: string; scope: string; generation: string; descriptor: SettingsDescriptor; snapshot: SettingsSnapshot; updated_at: string }
export interface ResourceView { resource: SettingsResource; connector: Connector; online: boolean }
export interface SettingsBinding { artifact_id: string; resource_id: string; revision_id: string; generation: string }
export interface SettingsSurfaceLease { id: string; resource_id: string; generation: string; expires_at: string }
export interface SettingEdit { op: "set" | "unset"; key: string; value?: unknown }
export interface SettingsProposal { operation: string; schema_version: string; expected_version: string; generation?: string; edits?: SettingEdit[]; parameters?: Record<string, unknown> }
export interface SettingsCommand { id: string; resource_id: string; status: "queued" | "executing" | "succeeded" | "rejected" | "conflicted" | "expired" | "cancelled" | "unknown"; proposal: SettingsProposal; proposal_sha256: string; result: Record<string, unknown> | null; expires_at: string; attempts: number }

const b = "/api/v1";
export const artifactAPI = {
  list: (thread: string) => request<{ artifacts: Artifact[] }>("GET", `${b}/threads/${thread}/artifacts`),
  get: (id: string) => request<{ artifact: Artifact; revision?: ArtifactRevision }>("GET", `${b}/artifacts/${id}`),
  revision: (id: string, revision: string, viewer?: string | null) => request<{ revision: ArtifactRevision }>("GET", `${b}/artifacts/${id}/revisions/${revision}${viewer ? `?viewer=${encodeURIComponent(viewer)}` : ""}`),
  revisions: (id: string, before?: string) => request<{ revisions: RevisionInfo[]; next_before?: string }>("GET", `${b}/artifacts/${id}/revisions${before ? `?before=${encodeURIComponent(before)}` : ""}`),
  binding: (id: string) => request<{ binding: SettingsBinding }>("GET", `${b}/artifacts/${id}/settings-binding`),
  openSettings: (id: string, revision: string) => request<{ lease: SettingsSurfaceLease }>("POST", `${b}/artifacts/${id}/settings-surface`, { revision_id: revision }),
  remove: (id: string) => request<{ ok: true }>("DELETE", `${b}/artifacts/${id}`),
};
export const controlAPI = {
  connectors: () => request<{ connectors: Connector[] }>("GET", `${b}/connectors`),
  connector: (id: string) => request<{ connector: Connector; online: boolean; resources: SettingsResource[] }>("GET", `${b}/connectors/${id}`),
  approve: (id: string, grants: Grant[]) => request<{ connector: Connector }>("POST", `${b}/connectors/${id}/approve`, { grants }),
  revoke: (id: string) => request<{ ok: true }>("DELETE", `${b}/connectors/${id}`),
  resource: (id: string) => request<ResourceView>("GET", `${b}/settings-resources/${id}`),
  createCommand: (id: string, input: { client_key: string; proposal: SettingsProposal; artifact_id?: string; revision_id?: string; surface_lease_id?: string; send_when_connected?: boolean; ttl_seconds?: number }) => request<{ command: SettingsCommand; created: boolean }>("POST", `${b}/settings-resources/${id}/commands`, input),
  command: (id: string) => request<{ command: SettingsCommand }>("GET", `${b}/commands/${id}`),
  cancel: (id: string) => request<{ command: SettingsCommand }>("POST", `${b}/commands/${id}/cancel`, {}),
  draft: (id: string) => request<{ proposal: SettingsProposal | null }>("GET", `${b}/settings-resources/${id}/draft`),
  saveDraft: (id: string, p: SettingsProposal) => request<{ ok: true }>("PUT", `${b}/settings-resources/${id}/draft`, p),
  deleteDraft: (id: string) => request<{ ok: true }>("DELETE", `${b}/settings-resources/${id}/draft`),
  audit: (id: string, before?: string) => request<{ audit: { id: string; command_id: string; event: string; detail: Record<string, unknown>; created_at: string }[]; next_before?: string }>("GET", `${b}/settings-resources/${id}/audit${before ? `?before=${before}` : ""}`),
};

export const effectLabel: Record<string, string> = { immediate: "Immediately", next_turn: "Next turn", next_task: "Next task", new_or_resumed_session: "New or resumed sessions", restart_required: "After restart", unknown: "Runtime effect unconfirmed" };
export const scopeLabel: Record<string, string> = { project: "Project defaults", project_local: "Local project defaults", user: "User defaults", profile: "Profile", session: "This runtime session" };
export function fileURL(id: string, revision: string, path: string, viewer?: string | null) { return `${b}/artifacts/${id}/revisions/${revision}/files/${path.split("/").map(encodeURIComponent).join("/")}${viewer ? `?viewer=${encodeURIComponent(viewer)}` : ""}`; }

export function validateShape(shape: Shape, value: unknown): string | null {
  if (shape.enum && !shape.enum.some((x) => JSON.stringify(x) === JSON.stringify(value))) return "Choose one of the available values.";
  if (shape.type === "string") return typeof value === "string" && new TextEncoder().encode(value).byteLength <= (shape.max_length || 32768) ? null : "Enter valid text within the byte limit.";
  if (shape.type === "boolean") return typeof value === "boolean" ? null : "Choose on or off.";
  if (shape.type === "number" || shape.type === "integer") return typeof value === "number" && Number.isFinite(value) && (shape.type !== "integer" || Number.isInteger(value)) && (shape.minimum === undefined || value >= shape.minimum) && (shape.maximum === undefined || value <= shape.maximum) ? null : "Enter a number in the allowed range.";
  if (shape.type === "array") return Array.isArray(value) && value.length <= 1024 && shape.items && value.every((x) => !validateShape(shape.items!, x)) ? null : "Enter a valid array.";
  if (shape.type === "object") {
    if (!value || typeof value !== "object" || Array.isArray(value)) return "Enter an object.";
    const obj = value as Record<string, unknown>;
    if ((shape.required || []).some((key) => !Object.hasOwn(obj, key))) return "A required value is missing.";
    for (const [key, v] of Object.entries(obj)) { const prop = shape.properties && Object.hasOwn(shape.properties, key) ? shape.properties[key] : undefined; if (!prop || validateShape(prop, v)) return `Invalid value for ${key}.`; }
    return null;
  }
  return "Unsupported value.";
}

/** Validate untrusted iframe proposals before they appear beside the trusted
 * Save button. The server and local adapter repeat their own validation. */
export function validateProposal(raw: unknown, resource: SettingsResource): SettingsProposal {
  if (!raw || typeof raw !== "object" || new TextEncoder().encode(JSON.stringify(raw)).byteLength > 65536) throw new Error("Invalid settings proposal.");
  const p = structuredClone(raw) as SettingsProposal;
  if (p.schema_version !== resource.descriptor.schema_version || p.expected_version !== resource.snapshot.version || (p.generation || "") !== resource.generation) throw new Error("Settings changed. Refresh and review your edits.");
  if (p.operation === "settings.apply") {
    if (!Array.isArray(p.edits) || !p.edits.length || p.edits.length > 512 || p.parameters && Object.keys(p.parameters).length) throw new Error("Invalid edits.");
    const seen = new Set<string>();
    for (const e of p.edits) {
      const f = resource.descriptor.fields.find((x) => x.key === e.key);
      if (!f?.writable || seen.has(e.key)) throw new Error("This field is not writable.");
      if ([...seen].some((key) => e.key.startsWith(key + "/") || key.startsWith(e.key + "/"))) throw new Error("Settings edits must not overlap.");
      seen.add(e.key);
      if (e.op === "unset") { if (!f.unset || e.value !== undefined) throw new Error("This override cannot be reset."); }
      else if (e.op !== "set" || validateShape(f.schema, e.value)) throw new Error(`${f.label}: invalid value.`);
    }
  } else if (p.operation === "settings.refresh") {
    if (p.edits?.length || p.parameters && Object.keys(p.parameters).length) throw new Error("Refresh accepts no edits.");
  } else {
    const a = resource.descriptor.actions?.find((x) => x.operation === p.operation);
    if (!a || p.edits?.length || validateShape(a.parameters, p.parameters || {})) throw new Error("Invalid or unavailable action.");
  }
  return p;
}
