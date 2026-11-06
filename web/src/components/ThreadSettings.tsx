import { useEffect, useRef, useState } from "react";
import { Sheet } from "./Common";
import { IconClose, IconEdit } from "./Icons";
import { toast, updateThread, useStore } from "../lib/store";
import { SettingInput } from "./IntegrationSettings";
import { artifactAPI, controlAPI, effectLabel, validateProposal, type ArtifactRevision, type ResourceView, type SettingsProposal, type SettingsSurfaceLease, type ThreadSettingsLink } from "../lib/artifacts";
import { connectArtifactFrame } from "../lib/artifact-bridge";
import "./thread-settings.css";

/** A conversation's agent settings. "This session" is the running session's
 * own settings, published by its agent as a session-scoped resource; it is
 * what the panel opens on. "Project defaults" are what new or resumed
 * sessions start with, and only ever shown when chosen. Both are rendered by
 * FinaleChat from the resource the agent published; an agent's own editor
 * page is an option, never required. */
export function ThreadSettingsPanel({ thread, onClose }: { thread: string; onClose: () => void }) {
  const [links, setLinks] = useState<ThreadSettingsLink[] | null>(null);
  const [scope, setScope] = useState<"session" | "project">("session");
  const [error, setError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [closing, setClosing] = useState(false);
  useEffect(() => {
    let alive = true;
    const refresh = () => controlAPI.threadSettings(thread).then(r => { if (alive) { setLinks(r.resources); setError(""); } }).catch((e: Error) => { if (alive) setError(e.message); });
    void refresh(); const timer = window.setInterval(refresh, 10000);
    return () => { alive = false; clearInterval(timer); };
  }, [thread]);
  const session = links?.find(r => r.scope === "session");
  const project = links?.find(r => r.scope !== "session");
  const link = scope === "session" ? session : project;
  const close = () => dirty ? setClosing(true) : onClose();
  return <Sheet className="thread-settings-sheet" label="Agent settings" onClose={close}>
    <header className="ts-header"><div><span className="ts-eyebrow">IN THIS CONVERSATION</span><h2>Agent settings</h2></div><button className="icon-btn" aria-label="Close settings" onClick={close}><IconClose /></button></header>
    <ThreadSummaryBlock id={thread} />
    {closing && <div className="ts-discard">You have unsaved changes.<button className="btn small" onClick={() => setClosing(false)}>Keep editing</button><button className="btn small" onClick={onClose}>Discard changes</button></div>}
    {error && <p className="ts-message" role="alert">{error}</p>}
    {links === null && !error && <p className="ts-message">Loading your agent’s settings…</p>}
    {links?.length === 0 && <div className="ts-empty"><h3>Settings will appear here</h3><p>When your agent connects, you can change its models and behavior right here in the conversation.</p><p>Keep eagent’s server or your Claude Code / Codex companion running. This panel updates automatically.</p></div>}
    {!!links?.length && project && <nav className="ts-scope" aria-label="Which settings">
      <button aria-pressed={scope === "session"} disabled={dirty} onClick={() => setScope("session")}>This session</button>
      <button aria-pressed={scope === "project"} disabled={dirty} onClick={() => setScope("project")}>Project defaults</button>
    </nav>}
    {link ? <CurrentSettings key={link.id} link={link} onDirty={setDirty} /> : !!links?.length && <div className="ts-empty"><h3>This session isn’t running</h3><p>A session’s own settings — its models and how it works — can be changed while it runs. Reply in the conversation to resume it, and they appear here.</p>{project && <><p>New and resumed sessions start from the project defaults.</p><button className="btn small" onClick={() => setScope("project")}>See project defaults</button></>}</div>}
  </Sheet>;
}

/** The same editor for a settings resource on its own, before any
 * conversation exists (a new-session draft): Save edits the project's
 * defaults. */
export function ResourceSettingsSheet({ resourceId, onClose }: { resourceId: string; onClose: () => void }) {
  const [link, setLink] = useState<ThreadSettingsLink | null>(null);
  const [error, setError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [closing, setClosing] = useState(false);
  useEffect(() => {
    let alive = true;
    controlAPI.resource(resourceId).then(v => { if (alive) setLink({ id: v.resource.id, label: v.resource.label, scope: v.resource.scope, provider: v.connector.provider, available: v.online, artifact_id: v.website?.artifact_id, revision_id: v.website?.revision_id }); }).catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, [resourceId]);
  const close = () => dirty ? setClosing(true) : onClose();
  return <Sheet className="thread-settings-sheet" label="Project settings" onClose={close}>
    <header className="ts-header"><div><span className="ts-eyebrow">FOR THIS PROJECT</span><h2>Agent settings</h2></div><button className="icon-btn" aria-label="Close settings" onClick={close}><IconClose /></button></header>
    {closing && <div className="ts-discard">You have unsaved changes.<button className="btn small" onClick={() => setClosing(false)}>Keep editing</button><button className="btn small" onClick={onClose}>Discard changes</button></div>}
    {error && <p className="ts-message" role="alert">{error}</p>}
    {link === null && !error && <p className="ts-message">Loading your agent’s settings…</p>}
    {link && <CurrentSettings key={link.id} link={link} onDirty={setDirty} />}
  </Sheet>;
}

/** The conversation's own title + summary, editable here as well as in the
 * thread header. PATCHes the thread; `summary` is a read alias of `description`. */
function ThreadSummaryBlock({ id }: { id: string }) {
  const thread = useStore((s) => s.threads[id]);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  if (!thread) return null;
  const desc = (thread.description || thread.summary || "").trim();
  if (editing) {
    const save = async () => {
      if (saving) return;
      setSaving(true);
      try {
        await updateThread(id, { description: draft.trim() });
        setEditing(false);
      } catch (e) {
        toast(e instanceof Error ? e.message : "Could not save", "error");
      } finally {
        setSaving(false);
      }
    };
    return (
      <div className="ts-fields ts-summary">
        <div className="field">
          <label htmlFor="ts-summary">Conversation summary</label>
          <textarea
            id="ts-summary"
            autoFocus
            rows={3}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            maxLength={2000}
            placeholder="What is this conversation about?"
          />
          <div className="hint">1–2 sentences, so the inbox stays scannable. {draft.length}/2000</div>
        </div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button type="button" className="btn small" disabled={saving} onClick={() => setEditing(false)}>Cancel</button>
          <button type="button" className="btn small primary" disabled={saving} onClick={() => void save()}>{saving ? "Saving…" : "Save"}</button>
        </div>
      </div>
    );
  }
  return (
    <div className="ts-summary" style={{ display: "flex", alignItems: "flex-start", gap: 4 }}>
      {desc ? (
        <button
          type="button"
          onClick={() => {
            setDraft(thread.description || thread.summary || "");
            setEditing(true);
          }}
          title="Edit summary"
          aria-label="Edit conversation summary"
          style={{ flex: 1, minWidth: 0, background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", fontSize: 13, lineHeight: 1.4, color: "var(--text-2)" }}
        >
          <span>{desc}</span>{" "}
          <IconEdit style={{ width: 12, height: 12, color: "var(--text-3)", verticalAlign: -1 }} aria-hidden="true" />
        </button>
      ) : (
        <button
          type="button"
          onClick={() => {
            setDraft("");
            setEditing(true);
          }}
          style={{ flex: 1, background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", fontSize: 13, color: "var(--text-3)", fontStyle: "italic" }}
        >
          Add a summary… <IconEdit style={{ width: 12, height: 12, verticalAlign: -1 }} aria-hidden="true" />
        </button>
      )}
    </div>
  );
}

const providerName = (p: string) => p === "eagent" ? "eagent" : p === "claude-code" ? "Claude Code" : p === "codex" ? "Codex" : p;

/** One resource's settings in FinaleChat's own form. When the agent also
 * published an editor page for the resource, it can be opened instead; it
 * only stages a proposal, and this panel's Save is what submits it. */
function CurrentSettings({ link, onDirty }: { link: ThreadSettingsLink; onDirty: (v: boolean) => void }) {
  const [view, setView] = useState<ResourceView | null>(null);
  const [useSite, setUseSite] = useState(false);
  const [site, setSite] = useState<{ revision: ArtifactRevision; lease: SettingsSurfaceLease } | null>(null);
  const [proposal, setProposal] = useState<SettingsProposal | null>(null);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const frame = useRef<HTMLIFrameElement>(null);
  const bridge = useRef<ReturnType<typeof connectArtifactFrame> | null>(null);
  const current = useRef({ view, proposal }); current.current = { view, proposal };
  const alive = useRef(true);
  const retry = useRef<{ json: string; key: string } | null>(null);
  const hasSite = !!link.artifact_id && !!link.revision_id;
  useEffect(() => { onDirty(!!proposal || busy); return () => onDirty(false); }, [proposal, busy, onDirty]);
  useEffect(() => {
    alive.current = true;
    const refresh = () => controlAPI.resource(link.id).then(v => { if (alive.current) { setView(v); setError(""); } return v; });
    refresh().catch((e: Error) => { if (alive.current) setError(e.message || "Could not load settings."); });
    const timer = window.setInterval(() => void refresh().catch(() => {}), 10000);
    return () => { alive.current = false; clearInterval(timer); bridge.current?.close(); };
  }, [link.id]);
  // The agent's page is pinned to the revision it opened with, so background
  // publication cannot erase a proposal staged in it.
  useEffect(() => {
    if (!useSite || site || !link.artifact_id || !link.revision_id) return;
    const [artifact, revision] = [link.artifact_id, link.revision_id];
    void (async () => {
      try {
        const r = await artifactAPI.revision(artifact, revision);
        const opened = await artifactAPI.openSettings(artifact, revision);
        if (alive.current) setSite({ revision: r.revision, lease: opened.lease });
      } catch (e) { if (alive.current) { setError(e instanceof Error ? e.message : "Could not open the agent’s editor."); setUseSite(false); } }
    })();
  }, [useSite, site, link.artifact_id, link.revision_id]);
  const stage = (p: SettingsProposal | null) => { setProposal(p); if (p) setStatus(""); setError(""); };
  const mount = () => {
    if (!frame.current || !site || !link.artifact_id) return;
    bridge.current?.close();
    bridge.current = connectArtifactFrame(frame.current, link.artifact_id, site.revision, {
      "settings.read": async () => { const v = await controlAPI.resource(link.id); if (alive.current) setView(v); return { ...v, editable: v.connector.state === "active", presentation: "thread" }; },
      "settings.propose": raw => { const v = current.current.view; if (!v) throw new Error("Settings are loading."); stage(validateProposal(raw, v.resource)); return { staged: true }; },
      "settings.clear": () => { stage(null); return { ok: true }; },
    });
  };
  const inSite = useSite && !!site;
  const save = async () => {
    if (!view || !proposal || busy) return;
    setBusy(true); setError(""); setStatus("Saving…");
    const submitted = proposal;
    try {
      const frozen = validateProposal(submitted, view.resource);
      const json = JSON.stringify(frozen);
      if (retry.current?.json !== json) retry.current = { json, key: crypto.randomUUID() };
      const surface = inSite && site ? { artifact_id: link.artifact_id, revision_id: site.revision.id, surface_lease_id: site.lease.id } : {};
      let { command } = await controlAPI.createCommand(link.id, { client_key: retry.current.key, proposal: frozen, ...surface });
      retry.current = null;
      while (alive.current && ["queued", "executing"].includes(command.status)) {
        await new Promise(resolve => setTimeout(resolve, 1000));
        try { command = (await controlAPI.command(command.id)).command; setError(""); } catch { if (alive.current) setStatus("Waiting for connection…"); }
      }
      if (!alive.current) return;
      bridge.current?.notify("settings.result", command);
      if (command.status !== "succeeded") throw new Error(command.status === "conflicted" ? "These settings changed elsewhere. Reopen settings to load the latest values." : typeof command.result?.message === "string" ? command.result.message : `Could not confirm this change (${command.status}).`);
      setProposal(p => p === submitted ? null : p);
      const effects = Array.isArray(command.result?.effects) ? command.result.effects as { effective_when?: string }[] : [];
      const timing = [...new Set(effects.map(e => effectLabel[e.effective_when || ""]?.toLowerCase()).filter(Boolean))];
      setStatus(timing.length ? `Saved · ${timing.join(", ")}` : "Saved");
      setView(await controlAPI.resource(link.id));
    } catch (e) { if (alive.current) { setError(e instanceof Error ? e.message : "Could not save settings."); setStatus(""); } }
    finally { if (alive.current) setBusy(false); }
  };
  const session = link.scope === "session";
  const unavailable = !view?.online || session && !view.resource.snapshot.runtime_known;
  const action = view?.resource.descriptor.actions?.find(a => a.operation === proposal?.operation);
  return <>
    <div className="ts-context"><span className={`ts-dot ${unavailable ? "offline" : ""}`} /><strong>{providerName(link.provider)}</strong><span>{session ? "This session" : link.scope === "user" ? "User defaults" : `${link.label} · project defaults`}</span><span className="ts-connection">{!view ? "" : unavailable ? (session ? "Not running" : "Offline") : "Connected"}</span></div>
    <div className="ts-body">
      {!view && !error && <p className="ts-message">Opening settings…</p>}
      {view && (inSite ? <iframe ref={frame} title={`${providerName(link.provider)} settings`} className="ts-frame" sandbox="allow-scripts" referrerPolicy="no-referrer" src={`/api/v1/artifacts/${link.artifact_id}/revisions/${site!.revision.id}/preview?surface=settings`} onLoad={mount} /> : useSite ? <p className="ts-message">Opening {providerName(link.provider)}’s editor…</p> : <FocusedSettings view={view} proposal={proposal} onProposal={stage} />)}
    </div>
    <footer className="ts-footer">
      {error && <p role="alert" className="ts-error">{error}</p>}
      {action && <p>{action.label}{action.class === "cost" ? " · Makes a paid provider request" : ""}</p>}
      <div className="ts-save-row"><div aria-live="polite"><strong>{status || (proposal ? "Unsaved changes" : "You’re up to date")}</strong><small>{unavailable ? (session ? "Resume the session to change its settings." : "Reconnect the agent to save changes.") : session ? "Changes apply to this running session." : "Defaults for new or resumed sessions."}</small></div><button className="btn primary" disabled={!proposal || busy || unavailable} onClick={() => void save()}>{busy ? "Saving…" : action ? action.label : "Save"}</button></div>
      {hasSite && <button className="ts-fallback" disabled={!!proposal || busy} onClick={() => { bridge.current?.close(); setUseSite(v => !v); }}>{useSite ? "Back to FinaleChat’s editor" : `Open ${providerName(link.provider)}’s own editor`}</button>}
    </footer>
  </>;
}

const groupOf = (key: string) => /model|reasoning|effort|route/i.test(key) ? "Models" : /permission|sandbox|approval|budget|turn|concurr|narrator|verbosity/i.test(key) ? "Behavior" : "Advanced";
const groupTitle: Record<string, string> = { Models: "Choose how your agent thinks", Behavior: "Make it work your way", Advanced: "Fine-tune your setup" };

/** The resource's fields, grouped into tabs; a tab with no fields is not shown. */
function FocusedSettings({ view, proposal, onProposal }: { view: ResourceView; proposal: SettingsProposal | null; onProposal: (p: SettingsProposal | null) => void }) {
  const { resource: r } = view;
  const groups = ["Models", "Behavior", "Advanced"].filter(g => r.descriptor.fields.some(f => groupOf(f.key) === g));
  const [picked, setTab] = useState("Models");
  const [query, setQuery] = useState("");
  const tab = groups.includes(picked) ? picked : groups[0] ?? "Models";
  const inTab = r.descriptor.fields.filter(f => groupOf(f.key) === tab);
  const fields = inTab.filter(f => `${f.key} ${f.label} ${f.description}`.toLowerCase().includes(query.toLowerCase()));
  return <div className="ts-focused">{groups.length > 1 && <nav className="ts-tabs" aria-label="Settings categories">{groups.map(name => <button key={name} aria-pressed={tab === name} onClick={() => { setTab(name); setQuery(""); }}>{name}</button>)}</nav>}<div className="ts-fields"><h3>{groupTitle[tab]}</h3>{inTab.length > 12 && <input aria-label={`Search ${tab.toLowerCase()} settings`} placeholder="Find a setting…" value={query} onChange={e => setQuery(e.target.value)} />}{!r.descriptor.fields.length && <p>No settings are published here.</p>}{fields.map(f => <SettingInput key={f.key} field={f} saved={r.snapshot.saved[f.key] ?? r.snapshot.effective[f.key]} effective={undefined} edit={proposal?.edits?.find(e => e.key === f.key)} onEdit={change => {
    const edits = (proposal?.edits || []).filter(e => e.key !== change.key); edits.push(change);
    onProposal({ operation: "settings.apply", schema_version: proposal?.schema_version || r.descriptor.schema_version, expected_version: proposal?.expected_version || r.snapshot.version, generation: proposal?.generation ?? r.generation, edits });
  }} />)}</div></div>;
}
