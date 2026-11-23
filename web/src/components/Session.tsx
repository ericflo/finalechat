import { APIError } from "../lib/api";
import { controlAPI, type SettingsCommand } from "../lib/artifacts";
import { forgetLive, refreshLive, toast } from "../lib/store";
import type { LiveSession, Thread, ThreadRelay } from "../lib/types";
import { ConfirmSheet } from "./Common";
import { IconTerminal } from "./Icons";
import { sleep } from "./NewSession";
import { relayAgent, relayStatus, threadRelay } from "./Relay";

/** The relay a thread reports, when it is running. Call it only for a
 * thread whose session runs: otherwise nobody keeps that report current. */
export function liveRelay(t: Thread): ThreadRelay | null {
  const relay = threadRelay(t);
  return relay && relay.state !== "stopped" && relay.state !== "exited" ? relay : null;
}

/** Beside a row's time while its agent session is running: "Active", or
 * the agent it relays to ("Claude") when a relay is running in it. */
export function SessionMark({ relay }: { relay: ThreadRelay | null }) {
  const name = relay ? relayAgent(relay) : "";
  const label = relay ? `Active session, relaying to ${relay.label || name} (${relayStatus(relay)})` : "Active session";
  return (
    <span className={`session-mark${relay ? ` relayed ${relay.state}` : ""}`} title={label} aria-label={label} role="img">
      <span className="session-dot" aria-hidden="true" />
      {relay ? (
        <>
          <IconTerminal className="session-relay-icon" aria-hidden="true" />
          {name}
        </>
      ) : (
        "Active"
      )}
    </span>
  );
}

// A running session's agent may offer to stop it (session.stop on the
// session's own settings resource). FinaleChat asks for confirmation in the
// agent's words, queues the stop like any session control, and waits for the
// agent's answer; it knows nothing of how a particular agent stops.

/** Queues the stop and resolves once the agent confirms it. */
export async function stopSession(session: LiveSession): Promise<void> {
  let command: SettingsCommand | null = null;
  // The session's settings may change between reading them and the stop
  // arriving; one fresh try covers that.
  for (let attempt = 0; attempt < 2; attempt++) {
    const r = (await controlAPI.resource(session.resource_id)).resource;
    if (!r.descriptor.actions?.some((a) => a.operation === "session.stop")) throw new Error("This session can no longer be stopped from here.");
    try {
      ({ command } = await controlAPI.createCommand(r.id, {
        client_key: crypto.randomUUID(),
        proposal: { operation: "session.stop", schema_version: r.descriptor.schema_version, expected_version: r.snapshot.version, generation: r.generation, parameters: {} },
        // A stop that has not happened within a minute should not happen later.
        ttl_seconds: 60,
      }));
    } catch (e) {
      if (attempt === 0 && e instanceof APIError && e.status === 409 && e.code === "conflict") continue;
      if (e instanceof APIError && e.code === "connector_offline") throw new Error("The session is no longer running.");
      throw e;
    }
    while (command && ["queued", "executing"].includes(command.status)) {
      await sleep(1000);
      command = (await controlAPI.command(command.id)).command;
    }
    if (command?.status !== "conflicted") break;
  }
  if (command?.status !== "succeeded") {
    const message = typeof command?.result?.message === "string" ? command.result.message : "";
    throw new Error(message || `The session did not confirm it stopped (${command?.status ?? "no answer"}).`);
  }
}

/** Confirms, then stops: what the agent says the action does is the body. */
export function StopSessionSheet({ session, onClose }: { session: LiveSession; onClose: () => void }) {
  const label = session.stop?.label || "Stop session";
  return (
    <ConfirmSheet
      title={`${label}?`}
      body={session.stop?.description || "The agent's session ends now."}
      confirmLabel={label}
      danger
      onClose={onClose}
      onConfirm={() => {
        toast("Stopping the session…", "info");
        stopSession(session)
          .then(() => {
            forgetLive(session.thread_id);
            toast("Session stopped", "success");
          })
          .catch((e: Error) => toast(e.message || "Could not stop the session", "error"))
          .finally(() => void refreshLive());
      }}
    />
  );
}
