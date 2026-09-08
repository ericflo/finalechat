import { useEffect, useRef, useState } from "react";
import { ConfirmSheet, Link } from "../components/Common";
import { GenericSettingsForm, SettingsControls, useSettingsResource } from "../components/IntegrationSettings";
import { TopBar } from "../components/TopBar";
import { APIError } from "../lib/api";
import { artifactAPI, fileURL, validateProposal, type Artifact, type ArtifactRevision, type RevisionInfo, type SettingsBinding, type SettingsCommand, type SettingsProposal, type SettingsSurfaceLease } from "../lib/artifacts";
import { connectArtifactFrame } from "../lib/artifact-bridge";
import { navigate } from "../lib/router";
import { fullDateTime } from "../lib/time";

export function ThreadArtifacts({ thread }: { thread: string }) {
  const [items, setItems] = useState<Artifact[]>([]);
  useEffect(() => {
    let alive = true;
    const refresh = () => { void artifactAPI.list(thread).then((r) => { if (alive) setItems(r.artifacts); }).catch(() => {}); };
    refresh(); const timer = window.setInterval(refresh, 15000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [thread]);
  if (!items.length) return null;
  return <div className="thread-artifacts" aria-label="Saved session artifacts">{items.map((a) => <Link key={a.id} className="btn small" href={`/t/${thread}/artifacts/${a.id}`}>{a.title}{!a.current_revision_id ? " · Uploading…" : ""}</Link>)}</div>;
}

export function ArtifactScreen({ id, thread, selectedRevision, surface }: { id: string; thread: string; selectedRevision: string | null; surface: string | null }) {
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
  const frame = useRef<HTMLIFrameElement>(null);
  const bridge = useRef<ReturnType<typeof connectArtifactFrame> | null>(null);
  const loadedFrame = useRef<HTMLIFrameElement | null>(null);
  const settings = surface === "settings";
  const { view, error: settingsError } = useSettingsResource(editing ? lease?.resource_id || null : null);
  const currentState = useRef({ view, editing, proposal, lease }); currentState.current = { view, editing, proposal, lease };

  useEffect(() => {
    let alive = true; setArtifact(null); setRevision(null); setEditing(false); setProposal(null); setError(""); setBinding(null);
    void artifactAPI.get(id).then(async (r) => {
      if (r.artifact.thread_id !== thread) throw new Error("This artifact belongs to another thread.");
      const rev = selectedRevision ? (await artifactAPI.revision(id, selectedRevision)).revision : r.revision;
      if (alive) { setArtifact(r.artifact); setRevision(rev || null); const v = r.revision?.manifest.dataset.settings_version; setArchivedSettingsVersion(typeof v === "string" ? v : null); }
    }).catch((e: Error) => { if (alive) setError(e.message); });
    void artifactAPI.revisions(id).then((r) => { if (alive) { setHistory(r.revisions); setBefore(r.next_before); } }).catch(() => {});
    void artifactAPI.binding(id).then((r) => { if (alive) setBinding(r.binding); }).catch(() => {});
    return () => { alive = false; editAttempt.current++; bridge.current?.close(); bridge.current = null; };
  }, [id, thread, selectedRevision]);
  useEffect(() => {
    let alive = true;
    const timer = window.setInterval(() => {
      void artifactAPI.get(id).then((r) => { if (alive) { setArtifact(r.artifact); const v = r.revision?.manifest.dataset.settings_version; setArchivedSettingsVersion(typeof v === "string" ? v : null); } }).catch(() => {});
      void artifactAPI.binding(id).then((r) => { if (alive) setBinding(r.binding); }).catch(() => {});
    }, 15000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [id]);
  useEffect(() => { editAttempt.current++; setOpening(false); setLease(null); setEditing(false); setProposal(null); bridge.current?.close(); }, [id, selectedRevision, surface]);
  useEffect(() => { if (!editing) bridge.current?.close(); }, [editing]);
  useEffect(() => () => { bridge.current?.close(); }, [generic, editing, surface]);

  const url = (rev: string | null, settings: boolean) => `/t/${thread}/artifacts/${id}${rev || settings ? "?" + new URLSearchParams({ ...(rev ? { revision: rev } : {}), ...(settings ? { surface: "settings" } : {}) }) : ""}`;
  const mount = () => {
    bridge.current?.close(); if (!frame.current || !revision) return;
    if (loadedFrame.current === frame.current) { setError("The viewer navigated away. Reopen this saved revision to reconnect it."); return; }
    loadedFrame.current = frame.current;
    bridge.current = connectArtifactFrame(frame.current, id, revision, settings && editing ? {
      "settings.read": () => { const s = currentState.current; if (!s.editing || !s.view) throw new Error("Current settings are not ready."); return { ...s.view, editable: s.view.connector.state === "active" && !!s.lease && Date.parse(s.lease.expires_at) > Date.now() && s.lease.generation === s.view.resource.generation }; },
      "settings.propose": (raw) => { const s = currentState.current; setProposal(null); if (!s.editing || !s.view || s.view.connector.state !== "active" || !s.lease || Date.parse(s.lease.expires_at) <= Date.now() || s.lease.generation !== s.view.resource.generation) throw new Error("This surface is read only."); const p = validateProposal(raw, s.view.resource); setProposal(p); return { staged: true }; },
    } : {});
  };
  const result = (command: SettingsCommand) => { bridge.current?.notify("settings.result", command); };
  const isCurrent = !!revision && artifact?.current_revision_id === revision.id;
  const canEdit = isCurrent && binding?.revision_id === revision?.id;
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
      {revision && <><span>Captured {fullDateTime(revision.manifest.captured_at)} · {revision.manifest.producer.name}</span><div className="artifact-actions"><button className={`btn small ${!settings ? "primary" : ""}`} onClick={() => navigate(url(selectedRevision, false))}>Explore session</button>{revision.manifest.settings_entrypoint && <button className={`btn small ${settings ? "primary" : ""}`} onClick={() => navigate(url(selectedRevision, true))}>Settings</button>}<a className="btn small" href={`/api/v1/artifacts/${id}/revisions/${revision.id}/download`}>Download archive</a></div></>}
      <details><summary>Saved revisions and files</summary><div className="artifact-history">{history.map((r) => <button key={r.id} className="btn small" onClick={() => navigate(url(r.id, settings))}>{fullDateTime(r.captured_at)}{r.id === artifact?.current_revision_id ? " · Latest" : ""}</button>)}{before && <button className="btn small" onClick={() => void more()}>Earlier revisions</button>}</div>{revision && <div className="artifact-files">{revision.manifest.files.map((f) => <a key={f.path} href={fileURL(id, revision.id, f.path)}>{f.path} <small>{f.size.toLocaleString()} bytes · {f.role}</small></a>)}</div>}<button className="btn small danger" onClick={() => setRemove(true)}>Delete artifact and history</button></details>
      {revision && !isCurrent && <div className="artifact-notice">{editing ? "A newer archive is available. This editor remains connected to the same settings resource until its editing session expires." : "You are viewing a historical revision."} <button className="btn small" onClick={() => navigate(url(artifact?.current_revision_id || null, settings))}>Open latest revision</button></div>}
      {settings && !editing && <div className="artifact-notice">This is a saved settings snapshot.{canEdit ? <button className="btn primary" disabled={opening} onClick={() => void beginEdit()}>{opening ? "Opening…" : "Edit current settings"}</button> : " Current control is unavailable for this revision."}</div>}
      {settings && editing && <div className="artifact-actions"><button className="btn small" onClick={() => setGeneric((v) => !v)}>{generic ? "Integration settings page" : "Standard settings form"}</button><button className="btn small" onClick={() => { setEditing(false); setProposal(null); }}>Return to snapshot</button></div>}
    </div>
    {(error || settingsError) && <p role="alert" className="artifact-error">{error || settingsError}</p>}
    {!revision && !error && <p className="artifact-notice">{artifact ? "The integration has not finished publishing its first revision." : "Loading saved session…"}</p>}
    {revision && !(editing && generic) && (!editing || view) && <iframe key={`${revision.id}:${settings}:${editing}`} ref={frame} title={settings ? "Integration settings" : "Session explorer"} className="artifact-frame" sandbox="allow-scripts" referrerPolicy="no-referrer" allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'" src={`/api/v1/artifacts/${id}/revisions/${revision.id}/preview${settings ? "?surface=settings" : ""}`} onLoad={mount} />}
    {settings && editing && view && <>{generic && <GenericSettingsForm resource={view.resource} proposal={proposal} onProposal={setProposal} />}<SettingsControls view={view} proposal={proposal} onProposal={setProposal} surface={{ artifact_id: id, revision_id: revision!.id, surface_lease_id: lease!.id }} surfaceExpiresAt={lease?.expires_at} surfaceGeneration={lease?.generation} archivedSettingsVersion={archivedSettingsVersion} onResult={result} /></>}
    {remove && <ConfirmSheet title="Delete this artifact?" body="All saved revisions and their files will be removed from this thread." confirmLabel="Delete artifact" danger onClose={() => setRemove(false)} onConfirm={async () => { try { await artifactAPI.remove(id); navigate(`/t/${thread}`); } catch (e) { setError(e instanceof APIError ? e.message : "Could not delete artifact."); setRemove(false); } }} />}
  </div>;
}
