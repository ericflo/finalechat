import { useEffect, useRef, useState } from "react";
import { ConfirmSheet, Link } from "../components/Common";
import { GenericSettingsForm, SettingsControls, useSettingsResource } from "../components/IntegrationSettings";
import { TopBar } from "../components/TopBar";
import { APIError } from "../lib/api";
import { artifactAPI, controlAPI, fileURL, validateProposal, type Artifact, type ArtifactRevision, type RevisionInfo, type SessionSettingsLink, type SettingsBinding, type SettingsCommand, type SettingsProposal, type SettingsSurfaceLease } from "../lib/artifacts";
import { connectArtifactFrame } from "../lib/artifact-bridge";
import { navigate } from "../lib/router";
import { fullDateTime } from "../lib/time";
import { validateAnchor } from "../lib/source-anchor";

export function ThreadArtifacts({ thread, onAvailable }: { thread: string; onAvailable?: (available: boolean) => void }) {
  const [items, setItems] = useState<Artifact[]>([]);
  const [resources, setResources] = useState<SessionSettingsLink[]>([]);
  useEffect(() => {
    let alive = true;
    setItems([]); setResources([]); onAvailable?.(false);
    const refresh = () => {
      void artifactAPI.list(thread).then((r) => { if (alive) { setItems(r.artifacts.filter(a => a.key !== "agent-settings")); onAvailable?.(r.artifacts.some(a => a.key !== "agent-settings" && !!a.current_revision_id)); } }).catch(() => {});
      void controlAPI.forThread(thread).then((r) => { if (alive) setResources(r.resources); }).catch(() => { if (alive) setResources([]); });
    };
    refresh(); const timer = window.setInterval(refresh, 15000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [thread, onAvailable]);
  if (!items.length && !resources.length) return null;
  return <div className="thread-artifacts" aria-label="Session artifacts and settings">{items.map((a) => <Link key={a.id} className="btn small" href={`/t/${thread}/artifacts/${a.id}`}>{a.title}{!a.current_revision_id ? " · Uploading…" : ""}</Link>)}{resources.map((r) => <Link key={r.id} className="btn small" href={`/t/${thread}?panel=settings`} title={r.label}>Live session settings{r.available ? "" : " · Unavailable"}</Link>)}</div>;
}

function compatibleUpdate(a: ArtifactRevision, b: ArtifactRevision) {
  const identity = (r: ArtifactRevision) => JSON.stringify([r.manifest.dataset.format, r.manifest.dataset.schema, r.manifest.dataset.session_id, r.manifest.producer.name, r.manifest.entrypoint, r.manifest.files.filter(f => f.role === "viewer").map(f => [f.path, f.sha256]).sort()]);
  return identity(a) === identity(b);
}

export function ArtifactScreen({ id, thread, selectedRevision, selectedViewer = null, initialAnchor = null, surface }: { id: string; thread: string; selectedRevision: string | null; selectedViewer?: string | null; initialAnchor?: string | null; surface: string | null }) {
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [revision, setRevision] = useState<ArtifactRevision | null>(null);
  const [history, setHistory] = useState<RevisionInfo[]>([]);
  const [before, setBefore] = useState<string | undefined>();
  const [binding, setBinding] = useState<SettingsBinding | null>(null);
  const [editing, setEditing] = useState(false);
  const [opening, setOpening] = useState(false);
  const [lease, setLease] = useState<SettingsSurfaceLease | null>(null);
  const editAttempt = useRef(0);
  const [generic, setGeneric] = useState(false);
  const [proposal, setProposal] = useState<SettingsProposal | null>(null);
  const [archivedSettingsVersion, setArchivedSettingsVersion] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [remove, setRemove] = useState(false);
  const [follow, setFollow] = useState(false);
  const [viewerNotice, setViewerNotice] = useState("");
  const [chatMessage, setChatMessage] = useState<string | null>(null);
  const navigationAttempt = useRef(0);
  const followAttempt = useRef(0);
  const inspection = useRef<{ artifact: string; value: unknown }>({ artifact: id, value: null });
  const frame = useRef<HTMLIFrameElement>(null);
  const bridge = useRef<ReturnType<typeof connectArtifactFrame> | null>(null);
  const loadedFrame = useRef<HTMLIFrameElement | null>(null);
  const settings = surface === "settings";
  const { view, error: settingsError } = useSettingsResource(editing ? lease?.resource_id || null : null);
  const currentState = useRef({ view, editing, proposal, lease }); currentState.current = { view, editing, proposal, lease };
  const followState = useRef({ follow, revision, settings, selectedViewer }); followState.current = { follow, revision, settings, selectedViewer };

  useEffect(() => {
    let alive = true; setArtifact(null); setRevision(null); setEditing(false); setProposal(null); setError(""); setBinding(null);
    void artifactAPI.get(id).then(async (r) => {
      if (r.artifact.thread_id !== thread) throw new Error("This artifact belongs to another thread.");
      const source = selectedRevision || r.revision?.id;
      const rev = source && (selectedRevision || selectedViewer) ? (await artifactAPI.revision(id, source, selectedViewer)).revision : r.revision;
      if (alive) { setArtifact(r.artifact); setRevision(rev || null); const v = r.revision?.manifest.dataset.settings_version; setArchivedSettingsVersion(typeof v === "string" ? v : null); }
    }).catch((e: Error) => { if (alive) setError(e.message); });
    void artifactAPI.revisions(id).then((r) => { if (alive) { setHistory(r.revisions); setBefore(r.next_before); } }).catch(() => {});
    void artifactAPI.binding(id).then((r) => { if (alive) setBinding(r.binding); }).catch(() => {});
    return () => { alive = false; editAttempt.current++; navigationAttempt.current++; bridge.current?.close(); bridge.current = null; };
  }, [id, thread, selectedRevision, selectedViewer, initialAnchor]);
  useEffect(() => {
    let alive = true;
    const timer = window.setInterval(() => {
      void artifactAPI.get(id).then((r) => { if (alive) {
        setArtifact(r.artifact); const v = r.revision?.manifest.dataset.settings_version; setArchivedSettingsVersion(typeof v === "string" ? v : null);
        if (r.revision) { const rev = r.revision; setHistory(h => h.some(item => item.id === rev.id) ? h : [{ id: rev.id, created_at: rev.created_at, captured_at: rev.manifest.captured_at, producer: rev.manifest.producer, dataset: rev.manifest.dataset }, ...h]); }
        const current = followState.current;
        if (current.follow && !current.settings && !current.selectedViewer && current.revision && r.revision && current.revision.id !== r.revision.id) {
          if (compatibleUpdate(current.revision, r.revision)) { bridge.current?.close(); setRevision(r.revision); }
          else { setFollow(false); setViewerNotice("The newer archive uses a different viewer or dataset. Open it explicitly to switch."); }
        }
      } }).catch(() => {});
      void artifactAPI.binding(id).then((r) => { if (alive) setBinding(r.binding); }).catch(() => {});
    }, 15000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [id]);
  useEffect(() => { editAttempt.current++; setOpening(false); setLease(null); setEditing(false); setProposal(null); bridge.current?.close(); }, [id, selectedRevision, selectedViewer, surface]);
  useEffect(() => { followAttempt.current++; if (settings || selectedViewer || selectedRevision) setFollow(false); }, [id, settings, selectedViewer, selectedRevision]);
  useEffect(() => { if (!editing) bridge.current?.close(); }, [editing]);
  useEffect(() => () => { bridge.current?.close(); }, [generic, editing, surface]);

  const url = (rev: string | null, settings: boolean, viewer: string | null = selectedViewer) => {
    const params = new URLSearchParams({ ...(rev ? { revision: rev } : {}), ...(settings ? { surface: "settings" } : { ...(viewer ? { viewer } : {}), ...(initialAnchor ? { anchor: initialAnchor } : {}) }) });
    return `/t/${thread}/artifacts/${id}${params.size ? "?" + params : ""}`;
  };
  const viewerQuery = selectedViewer ? `?viewer=${encodeURIComponent(selectedViewer)}` : "";
  const mount = () => {
    const epoch = ++navigationAttempt.current; setChatMessage(null);
    bridge.current?.close(); if (!frame.current || !revision) return;
    if (loadedFrame.current === frame.current) { setError("The viewer navigated away. Reopen this saved revision to reconnect it."); return; }
    loadedFrame.current = frame.current;
    bridge.current = connectArtifactFrame(frame.current, id, revision, {
      ...(!settings ? {
        "artifact.anchor": () => { if (!initialAnchor) return null; if (new TextEncoder().encode(initialAnchor).byteLength > 2048) throw new Error("Source anchor is too large."); return validateAnchor(JSON.parse(initialAnchor) as unknown, revision.manifest); },
        "thread.reveal": async (raw: unknown) => { const anchor = validateAnchor(raw, revision.manifest); try { const found = await artifactAPI.message(id, revision.id, anchor, selectedViewer); if (found.thread_id !== thread) throw new Error("Message belongs to another thread."); if (navigationAttempt.current === epoch) setChatMessage(found.message_id); return { available: true }; } catch (error) { if (error instanceof APIError && error.status === 404) throw new Error("No matching chat message was recorded for this event."); throw error; } },
      } : {}),
      "artifact.view-state.read": () => inspection.current.artifact === id ? structuredClone(inspection.current.value) : null,
      "artifact.view-state.write": raw => { const encoded = JSON.stringify(raw); if (encoded === undefined || new TextEncoder().encode(encoded).byteLength > 16384) throw new Error("Viewer state must be JSON within 16 KiB."); inspection.current = { artifact: id, value: JSON.parse(encoded) as unknown }; return { saved: true }; },
      ...(settings && editing && !selectedViewer ? {
      "settings.read": () => { const s = currentState.current; if (!s.editing || !s.view) throw new Error("Current settings are not ready."); return { ...s.view, editable: s.view.connector.state === "active" && !!s.lease && Date.parse(s.lease.expires_at) > Date.now() && s.lease.generation === s.view.resource.generation }; },
      "settings.propose": (raw) => { const s = currentState.current; setProposal(null); if (!s.editing || !s.view || s.view.connector.state !== "active" || !s.lease || Date.parse(s.lease.expires_at) <= Date.now() || s.lease.generation !== s.view.resource.generation) throw new Error("This surface is read only."); if (raw === null) return { staged: false }; const p = validateProposal(raw, s.view.resource); setProposal(p); return { staged: true }; },
      } : {}),
    }, selectedViewer);
  };
  const result = (command: SettingsCommand) => { bridge.current?.notify("settings.result", command); };
  const isCurrent = !!revision && artifact?.current_revision_id === revision.id;
  const canEdit = !selectedViewer && isCurrent && binding?.revision_id === revision?.id;
  const beginFollow = async (enabled: boolean) => {
    const attempt = ++followAttempt.current;
    setFollow(enabled); setViewerNotice("");
    if (!enabled || !revision) return;
    try {
      const latest = (await artifactAPI.get(id)).revision;
      if (attempt !== followAttempt.current) return;
      if (latest && compatibleUpdate(revision, latest)) { if (latest.id !== revision.id) { bridge.current?.close(); setRevision(latest); } if (selectedRevision) navigate(url(null, false, null)); }
      else { setFollow(false); setViewerNotice("The newer archive uses a different viewer or dataset. Open it explicitly to switch."); }
    } catch (e) { if (attempt === followAttempt.current) { setFollow(false); setError(e instanceof Error ? e.message : "Could not follow the latest archive."); } }
  };
  const beginEdit = async () => {
    if (!revision) return;
    const attempt = ++editAttempt.current;
    setOpening(true); setError("");
    try { const result = await artifactAPI.openSettings(id, revision.id); if (editAttempt.current === attempt) { setLease(result.lease); setEditing(true); setProposal(null); } }
    catch (e) { if (editAttempt.current === attempt) setError(e instanceof Error ? e.message : "Could not open current settings."); }
    finally { if (editAttempt.current === attempt) setOpening(false); }
  };
  const more = async () => { try { const r = await artifactAPI.revisions(id, before); setHistory((h) => [...h, ...r.revisions]); setBefore(r.next_before); } catch (e) { setError(e instanceof Error ? e.message : "Could not load history."); } };

  return <div className="page artifact-page"><TopBar title={artifact?.title || "Saved session"} backTo={`/t/${thread}`} />
    <div className="artifact-toolbar">
      {revision && <><span>Captured {fullDateTime(revision.manifest.captured_at)} · {revision.manifest.producer.name}</span><div className="artifact-actions"><button className={`btn small ${!settings ? "primary" : ""}`} onClick={() => navigate(url(selectedRevision, false))}>Explore session</button>{revision.manifest.settings_entrypoint && <button className={`btn small ${settings ? "primary" : ""}`} onClick={() => navigate(url(selectedRevision, true))}>Settings</button>}<a className="btn small" href={`/api/v1/artifacts/${id}/revisions/${revision.id}/download${viewerQuery}`}>{selectedViewer ? "Download with selected viewer" : "Download archive"}</a>{selectedViewer && <a className="btn small" href={`/api/v1/artifacts/${id}/revisions/${revision.id}/download`}>Download original archive</a>}</div></>}
      {revision && !settings && <div className="artifact-actions"><label><input type="checkbox" checked={follow} disabled={!!selectedViewer} onChange={event => void beginFollow(event.target.checked)} /> Follow latest</label>{typeof revision.manifest.dataset.format === "string" && <label>Viewer <select aria-label="Viewer for this dataset" value={selectedViewer || ""} onChange={event => { setFollow(false); navigate(url(revision.id, false, event.target.value || null)); }}><option value="">Original viewer for this data</option>{history.filter(r => r.dataset.format === revision.manifest.dataset.format && JSON.stringify(r.dataset.schema) === JSON.stringify(revision.manifest.dataset.schema)).map(r => <option key={r.id} value={r.id}>{r.producer.name} {r.producer.version} · {fullDateTime(r.captured_at)}</option>)}</select></label>}</div>}
      {selectedViewer && <p className="artifact-notice">This keeps the selected dataset and uses viewer files from another saved revision. Current settings control is available through the original viewer.</p>}
      {viewerNotice && <p className="artifact-notice">{viewerNotice}</p>}
      {chatMessage && <p className="artifact-notice"><Link className="btn small" href={`/t/${thread}?m=${encodeURIComponent(chatMessage)}`}>Show matching chat message</Link><button className="btn small" onClick={() => setChatMessage(null)}>Dismiss</button></p>}
      <details><summary>Saved revisions and files</summary><div className="artifact-history">{history.map((r) => <button key={r.id} className="btn small" onClick={() => { setFollow(false); navigate(url(r.id, settings)); }}>{fullDateTime(r.captured_at)}{r.id === artifact?.current_revision_id ? " · Latest" : ""}</button>)}{before && <button className="btn small" onClick={() => void more()}>Earlier revisions</button>}</div>{revision && <div className="artifact-files">{revision.manifest.files.map((f) => <a key={f.path} href={fileURL(id, revision.id, f.path, selectedViewer)}>{f.path} <small>{f.size.toLocaleString()} bytes · {f.role}</small></a>)}</div>}<button className="btn small danger" onClick={() => setRemove(true)}>Delete artifact and history</button></details>
      {revision && !isCurrent && <div className="artifact-notice">{editing ? "A newer archive is available. This editor remains connected to the same settings resource until its editing session expires." : "You are viewing a historical revision."} <button className="btn small" onClick={() => navigate(url(artifact?.current_revision_id || null, settings))}>Open latest revision</button></div>}
      {settings && !editing && <div className="artifact-notice">This is a saved settings snapshot.{canEdit ? <button className="btn primary" disabled={opening} onClick={() => void beginEdit()}>{opening ? "Opening…" : "Edit current settings"}</button> : " Current control is unavailable for this revision."}</div>}
      {settings && editing && <div className="artifact-actions"><button className="btn small" onClick={() => setGeneric((v) => !v)}>{generic ? "Integration settings page" : "Standard settings form"}</button><button className="btn small" onClick={() => { setEditing(false); setProposal(null); }}>Return to snapshot</button></div>}
    </div>
    {(error || settingsError) && <p role="alert" className="artifact-error">{error || settingsError}</p>}
    {!revision && !error && <p className="artifact-notice">{artifact ? "The integration has not finished publishing its first revision." : "Loading saved session…"}</p>}
    {revision && !(editing && generic) && (!editing || view) && <iframe key={`${revision.id}:${selectedViewer}:${settings}:${editing}`} ref={frame} title={settings ? "Integration settings" : "Session explorer"} className="artifact-frame" sandbox="allow-scripts" referrerPolicy="no-referrer" allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'" src={`/api/v1/artifacts/${id}/revisions/${revision.id}/preview${viewerQuery}${settings ? (viewerQuery ? "&" : "?") + "surface=settings" : ""}`} onLoad={mount} />}
    {settings && editing && view && <>{generic && <GenericSettingsForm resource={view.resource} proposal={proposal} onProposal={setProposal} />}<SettingsControls view={view} proposal={proposal} onProposal={setProposal} surface={{ artifact_id: id, revision_id: revision!.id, surface_lease_id: lease!.id }} surfaceExpiresAt={lease?.expires_at} surfaceGeneration={lease?.generation} archivedSettingsVersion={archivedSettingsVersion} onResult={result} /></>}
    {remove && <ConfirmSheet title="Delete this artifact?" body="All saved revisions and their files will be removed from this thread." confirmLabel="Delete artifact" danger onClose={() => setRemove(false)} onConfirm={async () => { try { await artifactAPI.remove(id); navigate(`/t/${thread}`); } catch (e) { setError(e instanceof APIError ? e.message : "Could not delete artifact."); setRemove(false); } }} />}
  </div>;
}
