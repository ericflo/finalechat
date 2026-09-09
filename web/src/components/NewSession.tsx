import { useEffect, useState } from "react";
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
 * while the page is visible so the button appears when eagent comes up. */
export function useSessionStarters(): { starters: SessionStarter[]; loaded: boolean } {
  const [starters, setStarters] = useState<SessionStarter[]>([]);
  const [loaded, setLoaded] = useState(false);
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
      } finally {
        if (alive) setLoaded(true);
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
  return { starters, loaded };
}

/** Where a new session may start, as the integration published it. */
export interface DirectoryChoices {
  root: string;
  recent: string[];
  children: string[];
  siblings: string[];
}

export function directoryChoices(resource: SettingsResource): DirectoryChoices {
  const raw = resource.snapshot.details?.directories as Partial<DirectoryChoices> | undefined;
  const list = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  return { root: typeof raw?.root === "string" ? raw.root : resource.snapshot.context, recent: list(raw?.recent), children: list(raw?.children), siblings: list(raw?.siblings) };
}

/** Starts a session through the trusted command channel and resolves to the
 * id of its thread once the integration's mirror has created it. */
export async function startSession(starter: SessionStarter, prompt: string, cwd: string | null, onStatus: (s: string) => void, cancelled: () => boolean): Promise<string> {
  onStatus("Asking eagent to start the session…");
  // The connector republishes its settings whenever they change; take the
  // version it holds now, not the one the page loaded a while ago.
  const r = (await controlAPI.resource(starter.resource.id)).resource;
  const parameters: Record<string, unknown> = { prompt: prompt.trim() };
  if (cwd && cwd !== directoryChoices(r).root) parameters.cwd = cwd;
  const { command } = await controlAPI.createCommand(r.id, {
    client_key: crypto.randomUUID(),
    proposal: { operation: "session.start", schema_version: r.descriptor.schema_version, expected_version: r.snapshot.version, generation: r.generation, parameters },
    ttl_seconds: 300,
  });
  let current = command;
  while (["queued", "executing"].includes(current.status)) {
    if (cancelled()) throw new Error("cancelled");
    await sleep(1500);
    current = (await controlAPI.command(command.id)).command;
  }
  if (current.status !== "succeeded") {
    const message = typeof current.result?.message === "string" ? current.result.message : `eagent did not start the session (${current.status}).`;
    throw new Error(message);
  }
  const ref = typeof current.result?.thread === "string" ? current.result.thread : null;
  if (!ref) throw new Error("The session started but its thread was not reported.");
  onStatus(current.result?.interactive === false ? "Session started as a batch run. Waiting for its thread…" : "Session started. Waiting for its thread…");
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    if (cancelled()) throw new Error("cancelled");
    try {
      const { thread } = await api.getThread(ref);
      return thread.id;
    } catch {
      await sleep(1500);
    }
  }
  throw new Error("The session started, but its thread has not appeared yet. It will show up in the inbox shortly.");
}

/** Picks the project for a new session when more than one eagent is connected. */
export function ProjectPickerSheet({ starters, onClose }: { starters: SessionStarter[]; onClose: () => void }) {
  return (
    <Sheet onClose={onClose} label="New session">
      <div style={{ padding: "4px 6px" }}>
        <h2 style={{ fontSize: 18, marginBottom: 6 }}>New session</h2>
        <p style={{ marginBottom: 10 }}>Which project?</p>
        {starters.map((s) => (
          <button
            key={s.resource.id}
            type="button"
            className="item"
            onClick={() => {
              onClose();
              navigate(`/new/${s.resource.id}`);
            }}
          >
            <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", minWidth: 0 }}>
              <span>{s.resource.label}</span>
              <small style={{ color: "var(--text-3)" }}>
                {s.connector.name}
                {s.online ? "" : " · offline"}
              </small>
            </span>
          </button>
        ))}
      </div>
    </Sheet>
  );
}

export function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms));
}
