import { api, APIError } from "../lib/api";
import { controlAPI, type SettingsCommand } from "../lib/artifacts";
import { forgetLive, refreshLive, setStopping, toast, updateThread } from "../lib/store";
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
 * the agent it relays to ("Claude") when a relay is running in it. While a
 * stop asked for here is on its way to the agent, "Stopping…". */
export function SessionMark({ relay, stopping }: { relay: ThreadRelay | null; stopping?: boolean }) {
  if (stopping) {
    return (
      <span className="session-mark stopping" role="status">
        <span className="session-dot" aria-hidden="true" />
        Stopping…
      </span>
    );
  }
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

/** Stops a session without holding the user for the agent's answer: the
 * session's mark reads "Stopping…" until then, and only a failure says
 * anything. With `archive` the thread is archived at once, not after the
 * wait (`onArchived` runs when it is), and comes back if the session turns
 * out to be running still. */
export function stopInBackground(session: LiveSession, { archive, onArchived }: { archive?: boolean; onArchived?: () => void } = {}): void {
  const id = session.thread_id;
  setStopping(id, true);
  const archived = archive
    ? updateThread(id, { archived: true }).then(
        () => {
          onArchived?.();
          return true;
        },
        (e: Error) => {
          toast(e.message || "Could not archive", "error");
          return false;
        },
      )
    : Promise.resolve(false);
  stopSession(session)
    .then(() => forgetLive(id))
    .catch(async (e: Error) => {
      // A session that ended by itself in the meantime is stopped all the same.
      const running = await api.liveSessions().then(
        (r) => (r.sessions ?? []).some((l) => l.thread_id === id),
        () => true,
      );
      if (!running) return;
      const restored = (await archived) && (await updateThread(id, { archived: false }).then(() => true, () => false));
      const message = e.message || "Could not stop the session";
      const said = /[.!?]$/.test(message) ? message : `${message}.`;
      toast(restored ? `${said} The thread is back in your inbox.` : said, "error");
    })
    .finally(() => {
      setStopping(id, false);
      void refreshLive();
    });
}

/** Confirms, then stops: what the agent says the action does is the body.
 * With `archive` it is "Stop and archive", the stop and the archive in one. */
export function StopSessionSheet({ session, archive, onClose, onArchived }: { session: LiveSession; archive?: boolean; onClose: () => void; onArchived?: () => void }) {
  const label = archive ? "Stop and archive" : session.stop?.label || "Stop session";
  return (
    <ConfirmSheet
      title={`${label}?`}
      body={session.stop?.description || "The agent's session ends now."}
      confirmLabel={label}
      danger
      onClose={onClose}
      onConfirm={() => stopInBackground(session, { archive, onArchived })}
    />
  );
}
