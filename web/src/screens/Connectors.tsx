import { useEffect, useState } from "react";
import { Link } from "../components/Common";
import { GenericSettingsForm, SettingsControls, useSettingsResource } from "../components/IntegrationSettings";
import { TopBar } from "../components/TopBar";
import { controlAPI, scopeLabel, type Connector, type Grant, type SettingsProposal, type SettingsResource } from "../lib/artifacts";

export function ConnectorsCard() {
  const [connectors, setConnectors] = useState<Connector[]>([]);
  const [error, setError] = useState("");
  const refresh = () => controlAPI.connectors().then((r) => { setConnectors(r.connectors); setError(""); }).catch((e: Error) => setError(e.message));
  useEffect(() => { void refresh(); const timer = window.setInterval(() => { void refresh(); }, 15000); return () => window.clearInterval(timer); }, []);
  return <><div className="section-title">Connected agents</div><div className="card connector-list"><p>Open Settings in a conversation to change your agent’s models and behavior. Your own integrations connect automatically.</p>{connectors.filter((c) => c.state !== "revoked").map((c) => <ConnectorRow key={c.id} connector={c} refresh={refresh} />)}{!connectors.some((c) => c.state !== "revoked") && <p>No agents connected yet.</p>}{error && <p role="alert" className="artifact-error">{error}</p>}</div></>;
}

function ConnectorRow({ connector: c, refresh }: { connector: Connector; refresh: () => Promise<void> }) {
  const [grants, setGrants] = useState<Grant[]>(structuredClone(c.requested_grants));
  const [resources, setResources] = useState<SettingsResource[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const online = c.state === "active" && !!c.last_seen_at && Date.now() - new Date(c.last_seen_at).getTime() < 90000;
  useEffect(() => { let alive = true; if (c.state === "active") void controlAPI.connector(c.id).then((r) => { if (alive) setResources(r.resources); }).catch(() => {}); return () => { alive = false; }; }, [c.id, c.state, c.last_seen_at]);
  const update = (key: string, prop: "operations" | "classes", value: string, enabled: boolean) => setGrants((gs) => gs.map((g) => g.key === key ? { ...g, [prop]: enabled ? [...g[prop], value] : g[prop].filter((x) => x !== value) } : g));
  const act = async (fn: () => Promise<unknown>) => { setBusy(true); setError(""); try { await fn(); await refresh(); } catch (e) { setError(e instanceof Error ? e.message : "Could not update connector."); } finally { setBusy(false); } };
  return <article className="connector-row" id={`connector-${c.id}`}><strong>{c.name}</strong><p>{c.provider} · {c.state === "pending" ? "Waiting for the integration to reconnect" : online ? "Connected" : "Offline"}</p>
    {c.state === "pending" ? <><details><summary>Manual connection options for older integrations</summary>{c.requested_grants.map((g) => { const selected = grants.find((x) => x.key === g.key); return <fieldset key={g.key}><legend><label><input type="checkbox" checked={!!selected} onChange={(e) => setGrants((gs) => e.target.checked ? [...gs, structuredClone(g)] : gs.filter((x) => x.key !== g.key))} />{g.label} · {scopeLabel[g.scope]}</label></legend><small>Resource: {g.key}</small>{selected && <div className="grant-options">{g.operations.map((op) => <label key={op}><input type="checkbox" checked={selected.operations.includes(op)} onChange={(e) => update(g.key, "operations", op, e.target.checked)} />{op}</label>)}{g.classes.map((cl) => <label key={cl}><input type="checkbox" checked={selected.classes.includes(cl)} onChange={(e) => update(g.key, "classes", cl, e.target.checked)} />{cl === "cost" ? "Paid provider calls" : cl === "executable" ? "Executable hooks / plugins" : cl === "permissions" ? "Permissions / sandbox" : cl === "credential_reference" ? "Credential references" : "Ordinary preferences"}</label>)}</div>}</fieldset>; })}<button className="btn primary" disabled={busy || !grants.length || grants.some((g) => !g.operations.length) || Date.now() > new Date(c.expires_at).getTime()} onClick={() => void act(() => controlAPI.approve(c.id, grants))}>Connect selected settings</button></details></> : <>{c.grants.map((g) => <p key={g.key}>{g.label} · {scopeLabel[g.scope]}</p>)}{resources.map((r) => <Link className="btn small" key={r.id} href={`/settings/resources/${r.id}`}>{r.label}</Link>)}</>}
    <button className="btn small" disabled={busy} onClick={() => void act(() => controlAPI.revoke(c.id))}>{c.state === "pending" ? "Reject pairing" : "Disconnect"}</button>{error && <p role="alert" className="artifact-error">{error}</p>}
  </article>;
}

export function ResourceSettingsScreen({ id, backTo = "/settings" }: { id: string; backTo?: string }) {
  const { view, error } = useSettingsResource(id);
  const [proposal, setProposal] = useState<SettingsProposal | null>(null);
  const [audit, setAudit] = useState<Awaited<ReturnType<typeof controlAPI.audit>> | null>(null);
  const [auditError, setAuditError] = useState("");
  const loadAudit = (before?: string) => { void controlAPI.audit(id, before).then((r) => setAudit((a) => before && a ? { ...r, audit: [...a.audit, ...r.audit] } : r)).catch((e: Error) => setAuditError(e.message)); };
  useEffect(() => { setProposal(null); setAudit(null); loadAudit(); }, [id]);
  return <div className="page"><TopBar title={view?.resource.label || "Integration settings"} backTo={backTo} /><div className="page-body">{error && <p role="alert" className="artifact-error">{error}</p>}{view ? <><GenericSettingsForm resource={view.resource} proposal={proposal} onProposal={setProposal} /><SettingsControls view={view} proposal={proposal} onProposal={setProposal} onResult={() => loadAudit()} /><details className="settings-audit"><summary>Settings history</summary>{audit?.audit.map((a) => <div key={a.id}><strong>{a.event}</strong> · {new Date(a.created_at).toLocaleString()}<pre>{JSON.stringify(a.detail, null, 2)}</pre></div>)}{audit?.next_before && <button className="btn small" onClick={() => loadAudit(audit.next_before)}>Earlier changes</button>}{auditError && <p>{auditError}</p>}</details></> : !error && <p>Loading settings…</p>}</div></div>;
}
