import { useEffect, useRef, useState } from "react";
import { controlAPI, effectLabel, scopeLabel, validateProposal, validateShape, type ResourceView, type SettingEdit, type SettingField, type SettingsCommand, type SettingsProposal, type SettingsResource } from "../lib/artifacts";
import { toast } from "../lib/store";

export function useSettingsResource(id: string | null) {
  const [view, setView] = useState<ResourceView | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    setView(null); setError(""); if (!id) return;
    let alive = true;
    const refresh = () => controlAPI.resource(id).then((v) => { if (alive) { setView(v); setError(""); } }).catch((e: Error) => { if (alive) setError(e.message); });
    void refresh(); const timer = window.setInterval(() => { if (document.visibilityState === "visible") void refresh(); }, 10000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [id]);
  return { view, error };
}

function commandDescription(c: SettingsCommand): string {
  if (c.status === "queued") return "Waiting for the connector. Your settings have not changed yet.";
  if (c.status === "executing") return "The connector is processing this command.";
  if (c.status === "unknown") return "The outcome could not be established. Refresh the settings before deciding what to do next.";
  if (c.status === "conflicted") return "Settings changed elsewhere. Refresh and review your edits.";
  if (c.status === "expired") return "The command expired before a result was confirmed.";
  if (c.status === "cancelled") return "Cancelled before execution.";
  if (c.status === "rejected") return typeof c.result?.message === "string" ? c.result.message : "The connector rejected the command. See its result below.";
  const effects = Array.isArray(c.result?.effects) ? c.result.effects as { effective_when?: string; runtime_applied?: boolean }[] : [];
  const timings = [...new Set(effects.filter((e) => !e.runtime_applied).map((e) => effectLabel[e.effective_when || "unknown"] || "Runtime effect unconfirmed"))];
  if (c.proposal.operation !== "settings.apply") return "The connector completed the action.";
  return timings.length ? `Saved. Takes effect: ${timings.join(", ").toLowerCase()}.` : "Saved. See the connector’s result for runtime effects.";
}

/** This component lives outside the artifact iframe. An iframe can stage
 * edits; only the user clicking these controls can submit or save a draft. */
export function SettingsControls({ view, proposal, onProposal, surface, surfaceExpiresAt, surfaceGeneration, archivedSettingsVersion, onResult }: {
  view: ResourceView; proposal: SettingsProposal | null; onProposal: (p: SettingsProposal | null) => void;
  surface?: { artifact_id: string; revision_id: string; surface_lease_id?: string }; onResult?: (c: SettingsCommand) => void;
  surfaceExpiresAt?: string;
  surfaceGeneration?: string;
  archivedSettingsVersion?: string | null;
}) {
  const [command, setCommand] = useState<SettingsCommand | null>(null);
  const [busy, setBusy] = useState(false);
  const [sendLater, setSendLater] = useState(false);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState<SettingsProposal | null>(null);
  const retry = useRef<{ serialized: string; key: string } | null>(null);
  const resultCallback = useRef(onResult); resultCallback.current = onResult;
  const latestProposal = useRef(proposal); latestProposal.current = proposal;
  const submittedProposal = useRef<SettingsProposal | null>(null);
  const proposalCallback = useRef(onProposal); proposalCallback.current = onProposal;
  const deliverResult = (c: SettingsCommand) => {
    if (c.status === "succeeded" && latestProposal.current === submittedProposal.current) proposalCallback.current(null);
    resultCallback.current?.(c);
  };
  const delivery = useRef(deliverResult); delivery.current = deliverResult;
  const resource = view.resource;
  useEffect(() => { let alive = true; void controlAPI.draft(resource.id).then((d) => { if (alive) setDraft(d.proposal); }).catch(() => {}); return () => { alive = false; }; }, [resource.id]);
  useEffect(() => {
    if (!command || !["queued", "executing"].includes(command.status)) return;
    let alive = true;
    const timer = window.setInterval(() => { void controlAPI.command(command.id).then(({ command: c }) => { if (alive) { setCommand(c); if (!["queued", "executing"].includes(c.status)) delivery.current(c); } }).catch((e: Error) => { if (alive) setError(e.message); }); }, 1500);
    return () => { alive = false; window.clearInterval(timer); };
  }, [command?.id, command?.status]);

  const submit = async (p: SettingsProposal) => {
    setBusy(true); setError("");
    try {
      // Freeze exactly the proposal rendered by this React commit. New
      // iframe messages cannot alter this command while the POST is pending.
      const frozen = validateProposal(p, resource);
      const serialized = JSON.stringify({ frozen, surface, sendLater });
      if (retry.current?.serialized !== serialized) retry.current = { serialized, key: crypto.randomUUID() };
      const r = await controlAPI.createCommand(resource.id, { client_key: retry.current.key, proposal: frozen, ...surface, send_when_connected: sendLater, ttl_seconds: sendLater ? 3600 : 300 });
      submittedProposal.current = p; setCommand(r.command); retry.current = null;
      if (!["queued", "executing"].includes(r.command.status)) delivery.current(r.command);
    } catch (e) { setError(e instanceof Error ? e.message : "Could not submit settings."); }
    finally { setBusy(false); }
  };
  const saveDraft = async () => { if (!proposal) return; try { const p = validateProposal(proposal, resource); await controlAPI.saveDraft(resource.id, p); setDraft(p); toast("Draft saved. It will not execute automatically."); } catch (e) { setError(e instanceof Error ? e.message : "Could not save draft."); } };
  let validation = "";
  if (proposal) { try { validateProposal(proposal, resource); } catch (e) { validation = e instanceof Error ? e.message : "Invalid proposal."; } }
  const expiredSurface = !!surfaceExpiresAt && Date.parse(surfaceExpiresAt) <= Date.now();
  const replacedSurface = surfaceGeneration !== undefined && surfaceGeneration !== resource.generation;
  if (expiredSurface || replacedSurface) validation = replacedSurface ? "The target runtime was replaced. Reopen the latest settings page." : "This editing session expired. Save a draft and reopen the latest settings page.";
  const pending = busy || !!command && ["queued", "executing"].includes(command.status);
  const action = resource.descriptor.actions?.find((a) => a.operation === proposal?.operation);
  const refresh: SettingsProposal = { operation: "settings.refresh", schema_version: resource.descriptor.schema_version, expected_version: resource.snapshot.version, generation: resource.generation };
  return <section className="settings-control" aria-label="Settings controls">
    <div className="settings-target"><strong>{resource.label}</strong><span>{scopeLabel[resource.scope] || resource.scope} · {view.connector.name} · {view.online ? "Connected" : "Offline"}</span></div>
    {draft && <div className="artifact-notice">A saved draft is available. <button className="btn small" onClick={() => { onProposal(structuredClone(draft)); setDraft(null); }}>Review draft</button><button className="btn small" onClick={() => { void controlAPI.deleteDraft(resource.id).then(() => setDraft(null)).catch((e: Error) => setError(e.message)); }}>Discard draft</button></div>}
    {proposal && <div className="proposal-review">
      <strong>{action?.label || (proposal.operation === "settings.refresh" ? "Refresh settings" : "Review changes")}</strong>
      {proposal.edits?.map((e) => { const f = resource.descriptor.fields.find((f) => f.key === e.key); return <div className="proposal-edit" key={e.key}><span>{f?.label || e.key}</span><small>Currently saved: {Object.hasOwn(resource.snapshot.saved, e.key) ? displayValue(resource.snapshot.saved[e.key]) : "Inherited"}</small><code>{e.op === "unset" ? "Inherit default" : inputValue(e.value)}</code><small>{effectLabel[f?.effective_when || "unknown"]}{f?.class !== "preference" ? ` · ${f?.class}` : ""}</small></div>; })}
      {action && <><p>{action.class === "cost" ? "This action can make a paid provider request." : `Capability: ${action.class}`}</p><pre>{JSON.stringify(proposal.parameters || {}, null, 2)}</pre></>}
      {validation && <p role="alert" className="artifact-error">{validation}</p>}
      {proposal.operation === "settings.apply" && proposal.expected_version !== resource.snapshot.version && proposal.schema_version === resource.descriptor.schema_version && (proposal.generation || "") === resource.generation && resource.scope !== "session" && <button className="btn small" disabled={pending} onClick={() => { try { onProposal(validateProposal({ ...proposal, expected_version: resource.snapshot.version }, resource)); setError(""); } catch (e) { setError(e instanceof Error ? e.message : "These edits cannot apply to the current settings."); } }}>Review against current settings</button>}
      {!view.online && resource.scope !== "session" && proposal.operation === "settings.apply" && <label className="offline-choice"><input type="checkbox" checked={sendLater} onChange={(e) => setSendLater(e.target.checked)} /> Send when connected, expiring after one hour</label>}
      <div className="artifact-actions"><button className="btn primary" disabled={pending || !!validation || view.connector.state !== "active" || !view.online && !sendLater} onClick={() => void submit(proposal)}>{busy ? "Submitting…" : action?.label || "Save"}</button><button className="btn" disabled={busy || !!validation && !expiredSurface} onClick={() => void saveDraft()}>Save draft</button><button className="btn" disabled={busy} onClick={() => onProposal(null)}>Discard changes</button></div>
    </div>}
    <div className="artifact-actions"><button className="btn small" disabled={pending || !view.online || expiredSurface || replacedSurface} onClick={() => void submit(refresh)}>Refresh from connector</button></div>
    {command && <div className="command-result" role="status"><strong>{commandDescription(command)}</strong>{command.result?.snapshot_publication === "pending" && <p>{archivedSettingsVersion && archivedSettingsVersion === command.result.version ? "Updated settings are preserved in the latest archive." : "The command result is saved. Archive publication is not yet confirmed here."}</p>}{command.status === "queued" && <button className="btn small" onClick={() => { void controlAPI.cancel(command.id).then((r) => setCommand(r.command)).catch((e: Error) => setError(e.message)); }}>Cancel queued command</button>}{command.result && <details><summary>Command details</summary><pre>{JSON.stringify(command.result, null, 2)}</pre></details>}</div>}
    {error && <p role="alert" className="artifact-error">{error}</p>}
  </section>;
}

function inputValue(v: unknown): string { return (typeof v === "string" ? v : JSON.stringify(v)) || ""; }
function displayValue(v: unknown): string { const s = inputValue(v); return s.length > 2000 ? s.slice(0, 2000) + "…" : s; }

export function GenericSettingsForm({ resource, proposal, onProposal }: { resource: SettingsResource; proposal: SettingsProposal | null; onProposal: (p: SettingsProposal | null) => void }) {
  const [query, setQuery] = useState("");
  const edit = (change: SettingEdit) => {
    const previous = proposal?.operation === "settings.apply" ? proposal : null;
    const edits = (previous?.edits || []).filter((e) => e.key !== change.key);
    edits.push(change);
    // Polling can replace the resource while a user is typing. Preserve the
    // version the whole edit set began against until it is explicitly reviewed.
    onProposal({ operation: "settings.apply", schema_version: previous?.schema_version || resource.descriptor.schema_version, expected_version: previous?.expected_version || resource.snapshot.version, generation: previous?.generation ?? resource.generation, edits });
  };
  return <div className="generic-settings"><input aria-label="Search settings" placeholder="Search settings…" value={query} onChange={(e) => setQuery(e.target.value)} />{resource.descriptor.fields.filter((f) => `${f.key} ${f.label} ${f.description || ""}`.toLowerCase().includes(query.toLowerCase())).map((field) => <SettingInput key={`${resource.snapshot.version}:${field.key}`} field={field} saved={resource.snapshot.saved[field.key]} effective={resource.snapshot.effective[field.key]} edit={proposal?.edits?.find((e) => e.key === field.key)} onEdit={edit} />)}<GenericSettingsActions resource={resource} proposal={proposal} onProposal={onProposal} /></div>;
}

function GenericSettingsActions({ resource, proposal, onProposal }: { resource: SettingsResource; proposal: SettingsProposal | null; onProposal: (p: SettingsProposal | null) => void }) {
  const actions = resource.descriptor.actions || [];
  if (!actions.length) return null;
  const selected = actions.find((a) => a.operation === proposal?.operation);
  const select = (operation: string) => onProposal(operation ? { operation, schema_version: resource.descriptor.schema_version, expected_version: resource.snapshot.version, generation: resource.generation, parameters: {} } : null);
  return <details className="settings-actions" open={!!selected}><summary>Integration actions</summary><p>Choose an action, fill its parameters, then review and submit it below.</p>
    <label htmlFor="settings-action">Action</label><select id="settings-action" value={selected?.operation || ""} onChange={(e) => select(e.target.value)}><option value="">Choose an action…</option>{actions.map((a) => <option key={a.operation} value={a.operation}>{a.label}</option>)}</select>
    {selected && proposal && Object.entries(selected.parameters.properties || {}).map(([name, shape]) => <SettingInput key={`${selected.operation}:${name}`} field={{ key: name, label: name.replaceAll("_", " ") + (selected.parameters.required?.includes(name) ? " (required)" : " (optional)"), schema: shape, writable: true, unset: !selected.parameters.required?.includes(name), class: selected.class, effective_when: "unknown" }} saved={proposal.parameters?.[name]} effective={undefined} resetLabel="Omit parameter" onEdit={(edit) => { const parameters = { ...proposal.parameters }; if (edit.op === "unset") delete parameters[name]; else parameters[name] = edit.value; onProposal({ ...proposal, parameters }); }} />)}
  </details>;
}

function SettingInput({ field: f, saved, effective, edit, onEdit, resetLabel = "Inherit default" }: { field: SettingField; saved: unknown; effective: unknown; edit?: SettingEdit; onEdit: (e: SettingEdit) => void; resetLabel?: string }) {
  const value = edit?.op === "set" ? edit.value : edit?.op === "unset" ? undefined : saved;
  const [text, setText] = useState(inputValue(value));
  const [error, setError] = useState("");
  useEffect(() => setText(f.schema.type === "object" || f.schema.type === "array" ? JSON.stringify(value, null, 2) || "" : inputValue(value)), [value, f.schema.type]);
  const changed = (v: unknown) => { const error = validateShape(f.schema, v); setError(error || ""); onEdit({ op: "set", key: f.key, value: v }); };
  const id = `setting-${f.key}`;
  return <div className="field settings-field"><label htmlFor={id}>{f.label}</label>{f.description && <p>{f.description}</p>}
    {f.schema.enum ? <select id={id} disabled={!f.writable} value={JSON.stringify(value) ?? ""} onChange={(e) => changed(JSON.parse(e.target.value))}><option value="" disabled>Inherited / unset</option>{f.schema.enum.map((v, i) => <option key={i} value={JSON.stringify(v)}>{displayValue(v)}</option>)}</select>
      : f.schema.type === "boolean" ? <select id={id} disabled={!f.writable} value={value === undefined ? "" : String(value)} onChange={(e) => changed(e.target.value === "true")}><option value="" disabled>Inherited / unset</option><option value="true">On</option><option value="false">Off</option></select>
        : f.schema.type === "array" || f.schema.type === "object" ? <textarea id={id} disabled={!f.writable} value={text} rows={4} onChange={(e) => { setText(e.target.value); try { changed(JSON.parse(e.target.value)); } catch { setError("Enter valid JSON."); onEdit({ op: "set", key: f.key, value: null }); } }} />
          : f.schema.type === "string" && (f.schema.max_length || 0) > 8192 ? <textarea id={id} disabled={!f.writable} value={text} rows={6} onChange={(e) => { setText(e.target.value); changed(e.target.value); }} />
          : <input id={id} disabled={!f.writable} type={f.schema.type === "string" ? "text" : "number"} min={f.schema.minimum} max={f.schema.maximum} value={text} onChange={(e) => { setText(e.target.value); changed(f.schema.type === "string" ? e.target.value : e.target.value === "" ? null : Number(e.target.value)); }} />}
    <small>{f.writable ? effectLabel[f.effective_when] : f.locked_reason || "Read only"}{f.source ? ` · ${f.source}` : ""}{effective !== undefined ? ` · Effective: ${displayValue(effective)}` : ""}</small>
    {f.writable && f.unset && <button className="btn small" onClick={() => { setError(""); onEdit({ op: "unset", key: f.key }); }}>{resetLabel}</button>}
    {error && <p role="alert" className="artifact-error">{error}</p>}
  </div>;
}
