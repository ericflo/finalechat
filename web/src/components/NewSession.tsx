import { useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { controlAPI, type Connector, type SettingsResource } from "../lib/artifacts";
import { navigate } from "../lib/router";
import { Sheet } from "./Common";

/** A settings resource whose connector can start a new agent session. */
export interface SessionStarter {
  resource: SettingsResource;
  connector: Connector;
  online: boolean;
}

/** Every connected integration that advertises `session.start`, refreshed
 * while the inbox is visible so the button appears when eagent comes up. */
export function useSessionStarters(): SessionStarter[] {
  const [starters, setStarters] = useState<SessionStarter[]>([]);
  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const { connectors } = await controlAPI.connectors();
        const found: SessionStarter[] = [];
        for (const c of connectors.filter((c) => c.state === "active")) {
          const detail = await controlAPI.connector(c.id);
          for (const r of detail.resources) {
            if (r.descriptor.actions?.some((a) => a.operation === "session.start")) found.push({ resource: r, connector: detail.connector, online: detail.online });
          }
        }
        if (alive) setStarters(found);
      } catch {
        // The inbox works without integrations; the button simply stays hidden.
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void refresh();
    }, 20000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);
  return starters;
}

/** Starts a new agent session in a project from the phone: the trusted Save
 * of the settings protocol, with the first message as its only parameter. */
export function NewSessionSheet({ starters, onClose }: { starters: SessionStarter[]; onClose: () => void }) {
  const preferred = starters.find((s) => s.online) ?? starters[0];
  const [resourceId, setResourceId] = useState(preferred?.resource.id ?? "");
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [commandId, setCommandId] = useState<string | null>(null);
  const cancelled = useRef(false);
  useEffect(() => () => { cancelled.current = true; }, []);

  const starter = starters.find((s) => s.resource.id === resourceId) ?? preferred;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!starter || !prompt.trim()) return;
    setBusy(true);
    setError(null);
    setStatus("Asking eagent to start the session…");
    try {
      // The connector republishes its settings whenever they change; take the
      // version it holds now, not the one the inbox loaded a while ago.
      const r = (await controlAPI.resource(starter.resource.id)).resource;
      const { command } = await controlAPI.createCommand(r.id, {
        client_key: crypto.randomUUID(),
        proposal: { operation: "session.start", schema_version: r.descriptor.schema_version, expected_version: r.snapshot.version, generation: r.generation, parameters: { prompt: prompt.trim() } },
        ttl_seconds: 300,
      });
      setCommandId(command.id);
      let current = command;
      while (["queued", "executing"].includes(current.status)) {
        if (cancelled.current) return;
        await sleep(1500);
        current = (await controlAPI.command(command.id)).command;
      }
      if (current.status !== "succeeded") {
        const message = typeof current.result?.message === "string" ? current.result.message : `eagent did not start the session (${current.status}).`;
        throw new Error(message);
      }
      const ref = typeof current.result?.thread === "string" ? current.result.thread : null;
      const interactive = current.result?.interactive !== false;
      setStatus(interactive ? "Session started. Waiting for its thread…" : "Session started as a batch run. Waiting for its thread…");
      if (!ref) throw new Error("The session started but its thread was not reported.");
      const deadline = Date.now() + 45000;
      while (Date.now() < deadline) {
        if (cancelled.current) return;
        try {
          const { thread } = await api.getThread(ref);
          onClose();
          navigate(`/t/${thread.id}`);
          return;
        } catch {
          await sleep(1500);
        }
      }
      throw new Error("The session started, but its thread has not appeared yet. It will show up in the inbox shortly.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the session.");
      setStatus(null);
    } finally {
      setBusy(false);
    }
  };

  const cancelQueued = async () => {
    if (!commandId) return;
    try {
      await controlAPI.cancel(commandId);
    } catch {
      // Already claimed; the connector will report a result.
    }
    onClose();
  };

  return (
    <Sheet onClose={busy ? () => {} : onClose} label="New session">
      <form onSubmit={submit} style={{ padding: "4px 6px" }}>
        <h2 style={{ fontSize: 18, marginBottom: 6 }}>New session</h2>
        <p style={{ marginBottom: 12 }}>Start an eagent session in a project and talk to it from here. Sessions run on the machine where eagent is running, with that project’s settings.</p>
        {starters.length > 1 && (
          <div className="field">
            <label htmlFor="new-session-project">Project</label>
            <select id="new-session-project" value={starter?.resource.id ?? ""} onChange={(e) => setResourceId(e.target.value)} disabled={busy}>
              {starters.map((s) => (
                <option key={s.resource.id} value={s.resource.id}>
                  {s.resource.label} · {s.connector.name}{s.online ? "" : " (offline)"}
                </option>
              ))}
            </select>
          </div>
        )}
        {starters.length === 1 && starter && (
          <div className="help" style={{ marginBottom: 10 }}>
            {starter.resource.label} · {starter.connector.name}{starter.online ? "" : " · offline"}
          </div>
        )}
        {starter && !starter.online && <div className="callout" style={{ marginBottom: 12 }}>This project’s eagent is offline right now. Start <code>eagent serve</code>, a session, or <code>eagent connector run</code> in it first.</div>}
        <div className="field">
          <label htmlFor="new-session-prompt">What should it do?</label>
          <textarea id="new-session-prompt" rows={5} autoFocus value={prompt} maxLength={32768} disabled={busy} onChange={(e) => setPrompt(e.target.value)} placeholder="Read INSTRUCTIONS.md and build it." />
        </div>
        {status && <div className="help" style={{ marginBottom: 10 }}>{status}</div>}
        {error && <div className="form-error">{error}</div>}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          {busy ? (
            <button type="button" className="btn" onClick={() => void cancelQueued()}>
              Cancel
            </button>
          ) : (
            <button type="button" className="btn" onClick={onClose}>
              Close
            </button>
          )}
          <button type="submit" className="btn primary" disabled={busy || !starter || !starter.online || !prompt.trim()}>
            {busy ? "Starting…" : "Start session"}
          </button>
        </div>
      </form>
    </Sheet>
  );
}

function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms));
}
