import { useEffect, useState } from "react";
import { APIError, api } from "../lib/api";
import { controlAPI, providerName, type Connector, type SettingsCommand, type SettingsResource } from "../lib/artifacts";
import { navigate } from "../lib/router";
import { Sheet } from "./Common";

/** A settings resource whose connector can start a new agent session. */
export interface SessionStarter {
  resource: SettingsResource;
  connector: Connector;
  online: boolean;
}

/** Every connected integration that advertises `session.start`, refreshed
 * while the page is visible so the button appears when the agent comes up. */
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

/** An agent the integration can start and pass the session's messages to
 * (a relay profile), as the integration published it. */
export interface RelayProfile {
  name: string;
  agent: string;
  label: string;
  model: string;
  effort: string;
}

export function relayProfiles(resource: SettingsResource): RelayProfile[] {
  const raw = resource.snapshot.details?.relay_profiles;
  if (!Array.isArray(raw)) return [];
  const text = (v: unknown) => (typeof v === "string" ? v : "");
  return raw
    .filter((p): p is Record<string, unknown> => !!p && typeof p === "object" && text((p as Record<string, unknown>).name) !== "")
    .map((p) => ({ name: text(p.name), agent: text(p.agent), label: text(p.label) || text(p.name), model: text(p.model), effort: text(p.effort) }));
}

/** Who starts the session, as people call it: the connector's provider
 * ("eagent", "Claude Code"), else "the agent". */
export function starterHost(starter: SessionStarter): string {
  return providerName(starter.connector.provider.trim()) || "the agent";
}

/** Whether a session may start without a relay profile, the agent doing the
 * work itself. True unless the action's `relay` parameter is an enum that
 * leaves out "" (an agent that always relays), or there is nothing to pick. */
export function startsWithoutRelay(resource: SettingsResource): boolean {
  if (!relayProfiles(resource).length) return true;
  const relay = resource.descriptor.actions?.find((a) => a.operation === "session.start")?.parameters?.properties?.relay;
  return !relay || !Array.isArray(relay.enum) || relay.enum.includes("");
}

/** "Codex" from "Codex · GPT-6 Astra xhigh". */
export function relayName(p: RelayProfile): string {
  return p.label.split(" · ")[0]?.trim() || p.name;
}

/** "GPT-6 Astra xhigh" from "Codex · GPT-6 Astra xhigh", else model and effort. */
export function relayDetail(p: RelayProfile): string {
  const rest = p.label.split(" · ").slice(1).join(" · ").trim();
  return rest || [p.model, p.effort].filter(Boolean).join(" ");
}

/** Starts a session through the trusted command channel and resolves to the
 * id of its thread once the integration's mirror has created it. With a
 * relay profile, the integration starts that agent to do the work. */
export async function startSession(starter: SessionStarter, prompt: string, cwd: string | null, relay: RelayProfile | null, options: Record<string, unknown>, onStatus: (s: string) => void, cancelled: () => boolean): Promise<string> {
  const host = starterHost(starter);
  onStatus(relay ? `Asking ${host} to start ${relayName(relay)}…` : `Asking ${host} to start the session…`);
  let current: SettingsCommand | null = null;
  // The connector republishes its settings whenever they change (a relay
  // starting or ending elsewhere is enough), so the version read here can be
  // stale by the time the start arrives; one fresh try covers that.
  for (let attempt = 0; attempt < 2; attempt++) {
    const r = (await controlAPI.resource(starter.resource.id)).resource;
    // The options the agent asks for (its declared inputs), as the user set
    // them; only inputs the action still declares are sent.
    const inputs = new Set(r.descriptor.actions?.find((a) => a.operation === "session.start")?.inputs?.map((i) => i.name));
    const parameters: Record<string, unknown> = Object.fromEntries(Object.entries(options).filter(([k, v]) => inputs.has(k) && v !== undefined && v !== ""));
    parameters.prompt = prompt.trim();
    if (cwd && cwd !== directoryChoices(r).root) parameters.cwd = cwd;
    if (relay) {
      if (!relayProfiles(r).some((p) => p.name === relay.name)) throw new Error(`This project no longer offers ${relayName(relay)}.`);
      parameters.relay = relay.name;
    }
    try {
      ({ command: current } = await controlAPI.createCommand(r.id, {
        client_key: crypto.randomUUID(),
        proposal: { operation: "session.start", schema_version: r.descriptor.schema_version, expected_version: r.snapshot.version, generation: r.generation, parameters },
        ttl_seconds: 300,
      }));
    } catch (e) {
      if (attempt === 0 && e instanceof APIError && e.status === 409 && e.code === "conflict") continue;
      throw e;
    }
    while (["queued", "executing"].includes(current.status)) {
      if (cancelled()) throw new Error("cancelled");
      await sleep(1500);
      current = (await controlAPI.command(current.id)).command;
    }
    if (current.status !== "conflicted") break;
  }
  if (!current || current.status !== "succeeded") {
    const message = typeof current?.result?.message === "string" ? current.result.message : `The session did not start (${current?.status ?? "no answer"}).`;
    throw new Error(message);
  }
  const ref = typeof current.result?.thread === "string" ? current.result.thread : null;
  if (!ref) throw new Error("The session started but its thread was not reported.");
  const started = relay ? `${relayName(relay)} started` : "Session started";
  onStatus(current.result?.interactive === false ? `${started} as a batch run. Waiting for its thread…` : `${started}. Waiting for its thread…`);
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

/** Picks the project for a new session when more than one agent can start one. */
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
