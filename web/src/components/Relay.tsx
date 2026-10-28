import { useEffect, useState } from "react";
import { api } from "../lib/api";
import type { SettingsResource } from "../lib/artifacts";
import { navigate } from "../lib/router";
import { toast } from "../lib/store";
import { sameDay, shortTime, useNow } from "../lib/time";
import type { RelayReceipt, Thread, ThreadRelay } from "../lib/types";
import { Avatar, ConfirmSheet, Sheet } from "./Common";
import { IconBell, IconChevron, IconClock, IconCopy, IconExpand, IconInfo, IconPause, IconRefresh, IconRepeat, IconStop, IconSwap, IconTerminal, IconUpgrade } from "./Icons";

// eagent can start Codex or Claude Code in a terminal it owns and pass the
// user's messages on (a relay). While one runs, eagent keeps thread meta
// `relay` current; the controls here are its slash commands, sent as
// ordinary messages so they show in the conversation like anything else.

/** The relay eagent reports on a thread, or null when there is none. */
export function threadRelay(t: Thread | undefined): ThreadRelay | null {
  const raw = t?.meta.relay;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const r = raw as Record<string, unknown>;
  const text = (v: unknown) => (typeof v === "string" ? v.trim() : "");
  const name = text(r.name);
  const agent = text(r.agent);
  if (!name && !agent) return null;
  return {
    name: name || agent,
    agent,
    label: text(r.label),
    state: (text(r.state) || "starting") as ThreadRelay["state"],
    autopilot: r.autopilot === true,
    autopilot_until: text(r.autopilot_until),
    updates: text(r.updates),
    target: text(r.target),
    attach: text(r.attach),
    notify_done: r.notify_done === true,
  };
}

const receiptStates: readonly string[] = ["sent", "delivered", "held", "eagent", "failed"];

/** eagent's receipts for the user's recent messages, by message id; empty
 * outside a relay thread. */
export function relayReceipts(t: Thread | undefined): Record<string, RelayReceipt> {
  const raw = t?.meta.relay_receipts;
  if (!threadRelay(t) || !raw || typeof raw !== "object" || Array.isArray(raw)) return {};
  const out: Record<string, RelayReceipt> = {};
  for (const [id, v] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof v === "string" && receiptStates.includes(v)) out[id] = v as RelayReceipt;
  }
  return out;
}

/** "Codex" or "Claude" for the agents eagent relays to, else the name as given. */
export function agentName(agent: string, fallback = ""): string {
  const a = agent.toLowerCase();
  if (a.startsWith("codex")) return "Codex";
  if (a.startsWith("claude")) return "Claude";
  const f = fallback.trim() || agent.trim();
  return f ? f.charAt(0).toUpperCase() + f.slice(1) : "Agent";
}

export function relayAgent(r: ThreadRelay): string {
  return agentName(r.agent || r.name, r.label.split(" · ")[0] || r.name);
}

/** The agent a /switch hands the work to: the other of Codex and Claude. */
function otherAgent(r: ThreadRelay): { profile: string; name: string } | null {
  const a = (r.agent || r.name).toLowerCase();
  if (a.startsWith("codex")) return { profile: "claude", name: "Claude" };
  if (a.startsWith("claude")) return { profile: "codex", name: "Codex" };
  return null;
}

/** "10:00 AM", or "Thu 10:00 AM" when it is not today. */
function untilText(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const time = shortTime(iso);
  return sameDay(iso, new Date().toISOString()) ? time : `${new Date(t).toLocaleDateString(undefined, { weekday: "short" })} ${time}`;
}

const ended = (r: ThreadRelay) => r.state === "stopped" || r.state === "exited";

/** "working", "autopilot", "autopilot until 10:00 AM". */
export function relayStatus(r: ThreadRelay): string {
  if (r.autopilot && !ended(r)) {
    const until = r.autopilot_until ? untilText(r.autopilot_until) : "";
    return until ? `autopilot until ${until}` : "autopilot";
  }
  return r.state;
}

/** The compact header badge: who is relayed to and what it is doing. */
export function RelayBadge({ relay, onOpen }: { relay: ThreadRelay; onOpen: () => void }) {
  const name = relayAgent(relay);
  const status = relayStatus(relay);
  return (
    <button type="button" className={`relay-badge ${relay.state}`} onClick={onOpen} aria-haspopup="dialog" aria-label={`${name}, ${status}. Controls`} title={`${relay.label || name}: ${relay.state}${status !== relay.state ? `, ${status}` : ""}`}>
      <span className="relay-dot" aria-hidden="true" />
      <span className="relay-badge-text">
        {name} · {status}
      </span>
      <IconExpand aria-hidden="true" />
    </button>
  );
}

/** The bell beside the badge while the agent works: one buzz when this turn
 * ends. Filled once armed; eagent confirms with meta.relay.notify_done. */
export function NotifyBell({ relay, onSend }: { relay: ThreadRelay; onSend: (command: string) => void }) {
  const [asked, setAsked] = useState(false);
  // A new turn (or eagent's word) settles what was asked.
  useEffect(() => setAsked(false), [relay.state, relay.notify_done]);
  if (relay.state !== "working") return null;
  const armed = relay.notify_done || asked;
  const label = armed ? "You'll get a buzz when this turn ends" : "Tell me when it's done";
  return (
    <button
      type="button"
      className={`relay-bell${armed ? " armed" : ""}`}
      aria-label={label}
      aria-pressed={armed}
      title={label}
      disabled={armed}
      onClick={() => {
        setAsked(true);
        onSend("/notify-done");
      }}
    >
      <IconBell aria-hidden="true" fill={armed ? "currentColor" : "none"} />
    </button>
  );
}

function Item({ icon: Icon, label, hint, danger, disabled, onClick }: { icon: (p: React.SVGProps<SVGSVGElement>) => React.ReactElement; label: string; hint?: string; danger?: boolean; disabled?: boolean; onClick: () => void }) {
  return (
    <button type="button" className={`item${danger ? " danger" : ""}`} disabled={disabled} onClick={onClick}>
      <Icon aria-hidden="true" />
      <span className="item-text">
        <span>{label}</span>
        {hint && <small>{hint}</small>}
      </span>
    </button>
  );
}

const cadences = [
  { value: "off", label: "Off", aria: "No progress updates" },
  { value: "5m", label: "5m", aria: "Updates every 5 minutes" },
  { value: "15m", label: "15m", aria: "Updates every 15 minutes" },
  { value: "1h", label: "1h", aria: "Updates every hour" },
];

/** The relay's controls. Each one sends eagent a slash command through
 * `onSend`, the same way a typed message goes. */
export function RelaySheet({ relay, onClose, onSend }: { relay: ThreadRelay; onClose: () => void; onSend: (command: string) => void }) {
  const [view, setView] = useState<"menu" | "autopilot" | "stop">("menu");
  const name = relayAgent(relay);
  const other = otherAgent(relay);
  const stopped = relay.state === "stopped";
  const send = (command: string) => {
    onClose();
    onSend(command);
  };
  const copyAttach = async () => {
    onClose();
    try {
      await navigator.clipboard.writeText(relay.attach);
      toast("Attach command copied");
    } catch {
      toast(relay.attach);
    }
  };
  if (view === "stop") {
    return <ConfirmSheet title={`Stop ${name}?`} body={`${name} ends for good, not just this step. To stop only what it is doing now, use Interrupt.`} confirmLabel={`Stop ${name}`} danger onClose={onClose} onConfirm={() => onSend("/stop-relay")} />;
  }
  if (view === "autopilot") return <AutopilotSheet relay={relay} onClose={onClose} onSend={send} />;
  return (
    <Sheet onClose={onClose} label={`${name} controls`} className="relay-sheet">
      <div className="sheet-title">
        <Avatar name={name} small />
        <span className="item-text">
          <span>{relay.label || name}</span>
          <small>{relayStatus(relay)}</small>
        </span>
      </div>
      {!ended(relay) && <Item icon={IconPause} label="Interrupt" hint="Stop its current step" onClick={() => send("/interrupt")} />}
      {relay.state === "working" && <Item icon={IconBell} label="Tell me when it's done" hint={relay.notify_done ? "You'll get a buzz" : "One buzz when this turn ends"} disabled={relay.notify_done} onClick={() => send("/notify-done")} />}
      {!stopped && <Item icon={IconTerminal} label="Screen" onClick={() => send("/screen")} />}
      {!stopped && <Item icon={IconRefresh} label="Restart" onClick={() => send("/restart")} />}
      {!stopped && <Item icon={IconUpgrade} label="Upgrade" hint={`Update ${name}, then restart`} onClick={() => send("/upgrade")} />}
      {!stopped && other && <Item icon={IconSwap} label={`Switch to ${other.name}`} hint="Hands the work over with a brief" onClick={() => send(`/switch ${other.profile}`)} />}
      {!ended(relay) && <Item icon={IconRepeat} label="Autopilot…" hint={relay.autopilot ? `On: ${relayStatus(relay)}` : "Keep it working on its own"} onClick={() => setView("autopilot")} />}
      {!stopped && (
        <div className="relay-updates">
          <span className="relay-updates-label">
            <IconBell aria-hidden="true" /> Updates
          </span>
          <div className="segmented" role="group" aria-label="Progress updates while it works">
            {cadences.map((c) => (
              <button key={c.value} type="button" aria-label={c.aria} aria-pressed={relay.updates === c.value} className={relay.updates === c.value ? "active" : ""} onClick={() => send(`/updates ${c.value}`)}>
                {c.label}
              </button>
            ))}
          </div>
        </div>
      )}
      <Item icon={IconInfo} label="Status" onClick={() => send("/relay")} />
      {relay.attach && <Item icon={IconCopy} label="Copy attach command" onClick={() => void copyAttach()} />}
      {!stopped && <Item icon={IconStop} label={`Stop ${name}`} danger onClick={() => setView("stop")} />}
    </Sheet>
  );
}

/** "HH:00" two hours from now, a reasonable first guess for a deadline. */
function defaultUntil(): string {
  const d = new Date(Date.now() + 2 * 3600 * 1000);
  return `${String(d.getHours()).padStart(2, "0")}:00`;
}

function AutopilotSheet({ relay, onClose, onSend }: { relay: ThreadRelay; onClose: () => void; onSend: (command: string) => void }) {
  const name = relayAgent(relay);
  const [until, setUntil] = useState(defaultUntil);
  const [note, setNote] = useState("");
  // eagent reads the words after the time as the instruction; one line.
  const extra = note.trim().replace(/\s+/g, " ");
  const withNote = (command: string) => (extra ? `${command} ${extra}` : command);
  return (
    <Sheet onClose={onClose} label="Autopilot">
      <div className="confirm autopilot">
        <h2>Autopilot</h2>
        <p>{relay.autopilot ? `On: ${relayStatus(relay)}. ` : ""}{name} keeps working without waiting for you.</p>
        <Item icon={IconRepeat} label="Until I say stop" onClick={() => onSend(withNote("/autopilot"))} />
        <form
          className="autopilot-until"
          onSubmit={(e) => {
            e.preventDefault();
            if (/^\d{1,2}:\d{2}$/.test(until)) onSend(withNote(`/autopilot until ${until}`));
          }}
        >
          <label htmlFor="autopilot-until">
            <IconClock aria-hidden="true" /> Until
          </label>
          <input id="autopilot-until" type="time" value={until} required onChange={(e) => setUntil(e.target.value)} />
          <button type="submit" className="btn small primary" disabled={!/^\d{1,2}:\d{2}$/.test(until)}>
            Start
          </button>
        </form>
        {relay.autopilot && <Item icon={IconStop} label="Turn off" danger onClick={() => onSend("/autopilot off")} />}
        <div className="field">
          <label htmlFor="autopilot-note">Instruction (optional)</label>
          <input id="autopilot-note" value={note} maxLength={500} placeholder="Finish the migration, then open a PR" onChange={(e) => setNote(e.target.value)} />
        </div>
      </div>
    </Sheet>
  );
}

/** A relay running on the connector's machine, as its snapshot lists it. */
export interface RunningRelay {
  name: string;
  agent: string;
  target: string;
  thread: string;
  dir: string;
  state: string;
  created: string;
}

export function runningRelays(resource: SettingsResource): RunningRelay[] {
  const raw = resource.snapshot.details?.relays;
  if (!Array.isArray(raw)) return [];
  const text = (v: unknown) => (typeof v === "string" ? v : "");
  return raw
    .filter((r): r is Record<string, unknown> => !!r && typeof r === "object" && text((r as Record<string, unknown>).thread) !== "")
    .map((r) => ({ name: text(r.name), agent: text(r.agent), target: text(r.target), thread: text(r.thread), dir: text(r.dir), state: text(r.state), created: text(r.created) }));
}

/** "now", "12m", "3h", "2d". */
function runningFor(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const m = Math.max(0, Math.floor((now - t) / 60000));
  if (m < 1) return "now";
  if (m < 60) return `${m}m`;
  if (m < 48 * 60) return `${Math.floor(m / 60)}h`;
  return `${Math.floor(m / 1440)}d`;
}

/** What is already running on this machine; a row opens its thread. */
export function RunningRelays({ relays }: { relays: RunningRelay[] }) {
  const now = useNow(30000);
  const [opening, setOpening] = useState<string | null>(null);
  const open = async (r: RunningRelay) => {
    setOpening(r.thread);
    try {
      const { thread } = await api.getThread(r.thread);
      navigate(`/t/${thread.id}`);
    } catch {
      toast("Its thread is not here yet.", "error");
    } finally {
      setOpening(null);
    }
  };
  return (
    <section className="running" aria-labelledby="running-title">
      <div className="section-title" id="running-title">
        Running now
      </div>
      {relays.map((r, i) => {
        const name = agentName(r.agent, r.name);
        const folder = r.dir.replace(/\/+$/, "").split("/").pop() || r.dir;
        const age = runningFor(r.created, now);
        const when = r.state === "exited" ? ["exited", age].filter(Boolean).join(" · ") : age;
        return (
          <button key={r.target || `${r.thread}-${r.name}-${i}`} type="button" className="running-row" title={r.dir || undefined} disabled={opening === r.thread} onClick={() => void open(r)}>
            <Avatar name={name} small />
            <span className="running-main">
              <span className="running-agent">{name}</span>
              {folder && <span className="running-dir">{folder}</span>}
            </span>
            {when && <span className={`running-age${r.state === "exited" ? " exited" : ""}`}>{when}</span>}
            <IconChevron aria-hidden="true" />
          </button>
        );
      })}
    </section>
  );
}
