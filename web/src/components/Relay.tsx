import { useEffect, useMemo, useState } from "react";
import { api } from "../lib/api";
import { controlAPI, type SettingsResource } from "../lib/artifacts";
import { navigate } from "../lib/router";
import { toast, useStore } from "../lib/store";
import { sameDay, shortTime, useNow } from "../lib/time";
import type { RelayReceipt, Thread, ThreadRelay } from "../lib/types";
import { Avatar, ConfirmSheet, Sheet } from "./Common";
import { IconBell, IconChevron, IconClock, IconCopy, IconCpu, IconExpand, IconRefresh, IconRepeat, IconStop, IconSwap, IconTerminal, IconUpgrade } from "./Icons";
import { relayName, relayProfiles } from "./NewSession";
import { SettingsForm, startModel, type FormModel } from "./SettingsForm";

// A host agent (eagent) can start a coding agent (any relay profile it
// publishes) in a terminal it owns and pass the user's messages on (a
// relay). While one runs, the host keeps thread meta `relay` current; the
// controls here are its slash commands, sent as ordinary messages so they
// show in the conversation like anything else.

/** The relay the host reports on a thread, or null when there is none. */
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
    profile: text(r.profile),
    label: text(r.label),
    model: text(r.model),
    effort: text(r.effort),
    pending: text(r.pending),
    state: (text(r.state) || "starting") as ThreadRelay["state"],
    autopilot: r.autopilot === true,
    autopilot_until: text(r.autopilot_until),
    target: text(r.target),
    attach: text(r.attach),
    notify_done: r.notify_done === true,
    switch_profiles: Array.isArray(r.switch_profiles) ? r.switch_profiles.filter((p): p is string => typeof p === "string" && /^[\w-]+$/.test(p)) : undefined,
  };
}

const receiptStates: readonly string[] = ["sent", "delivered", "held", "eagent", "failed"];

/** The host's receipts for the user's recent messages, by message id; empty
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

/** "Codex" or "Claude" for older hosts, whose relays carry only the agent's
 * command name; otherwise the label's head or the name, capitalised ("Pi"). */
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

/** The host agent's name as the thread gives it ("eagent"), for the route
 * pip and receipts; "Host" when the thread names none. */
export function relayHost(t: Thread | undefined): string {
  return t?.agent?.trim() || "Host";
}

/** Older hosts offered one switch destination for each built-in relay. */
function switchProfiles(r: ThreadRelay): string[] {
  if (r.switch_profiles) return r.switch_profiles;
  if (r.agent === "codex") return ["claude"];
  if (r.agent === "claude") return ["codex"];
  return [];
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

/** What it runs, as its label says after the agent's name: "Fable 5.2 xhigh". */
function relayRuns(r: ThreadRelay): string {
  return r.label.split(" · ").slice(1).join(" · ").trim() || [r.model, r.effort].filter(Boolean).join(" ");
}

type RelayView = "menu" | "model" | "switch" | "autopilot" | "upgrade" | "restart" | "stop";

/** The relay's controls. Each one sends eagent a slash command through
 * `onSend`, the same way a typed message goes. What interrupts the agent
 * mid-step is not here: that is the stop button in the composer. The ones
 * that end or restart it sit at the bottom, and ask first. */
export function RelaySheet({ thread, relay, onClose, onSend, stopSession }: { thread: string; relay: ThreadRelay; onClose: () => void; onSend: (command: string) => void; stopSession?: { label: string; onClick: () => void } }) {
  const [view, setView] = useState<RelayView>("menu");
  const name = relayAgent(relay);
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
    return <ConfirmSheet title={`Stop ${name}?`} body={`${name} ends for good, not just this step. To stop only what it is doing now, use the stop button beside your message.`} confirmLabel={`Stop ${name}`} danger onClose={onClose} onConfirm={() => onSend("/stop-relay")} />;
  }
  if (view === "restart") {
    return <ConfirmSheet title={`Restart ${name}?`} body={`${name} stops what it is doing and starts again on the same conversation.`} confirmLabel="Restart" onClose={onClose} onConfirm={() => onSend("/restart")} />;
  }
  if (view === "upgrade") {
    return <ConfirmSheet title={`Upgrade ${name}?`} body={`${name} stops what it is doing, updates to its latest version, and starts again on the same conversation.`} confirmLabel="Upgrade" onClose={onClose} onConfirm={() => onSend("/upgrade")} />;
  }
  if (view === "autopilot") return <AutopilotSheet relay={relay} onClose={onClose} onSend={send} />;
  if (view === "model" || view === "switch") return <AgentOptionsSheet thread={thread} relay={relay} mode={view} onClose={onClose} onSend={send} />;
  const runs = relayRuns(relay);
  const others = switchProfiles(relay);
  return (
    <Sheet onClose={onClose} label={`${name} controls`} className="relay-sheet">
      <div className="sheet-title">
        <Avatar name={name} small />
        <span className="item-text">
          <span>{relay.label || name}</span>
          <small>{relayStatus(relay)}</small>
        </span>
      </div>
      {!stopped && <Item icon={IconCpu} label="Model" hint={relay.pending ? `${runs ? `${runs}. ` : ""}Next: ${relay.pending}, when this turn ends` : runs || "Its model and reasoning effort"} onClick={() => setView("model")} />}
      {relay.state === "working" && <Item icon={IconBell} label="Tell me when it's done" hint={relay.notify_done ? "You'll get a buzz" : "One buzz when this turn ends"} disabled={relay.notify_done} onClick={() => send("/notify-done")} />}
      {!stopped && <Item icon={IconTerminal} label="Screen" hint="Show its terminal here" onClick={() => send("/screen")} />}
      {!stopped && others.length > 0 && <Item icon={IconSwap} label="Switch agent…" hint={`Hand the work to ${others.map((p) => agentName(p)).join(" or ")}`} onClick={() => setView("switch")} />}
      {!ended(relay) && <Item icon={IconRepeat} label="Autopilot…" hint={relay.autopilot ? `On: ${relayStatus(relay)}` : "Keep it working on its own"} onClick={() => setView("autopilot")} />}
      {relay.attach && <Item icon={IconCopy} label="Copy attach command" onClick={() => void copyAttach()} />}
      {(!stopped || stopSession) && <div className="sheet-sep" role="separator" />}
      {!stopped && <Item icon={IconUpgrade} label="Upgrade…" hint={`Update ${name}, then restart`} onClick={() => setView("upgrade")} />}
      {!stopped && <Item icon={IconRefresh} label="Restart…" hint="Start it again on the same conversation" onClick={() => setView("restart")} />}
      {!stopped && <Item icon={IconStop} label={`Stop ${name}`} danger onClick={() => setView("stop")} />}
      {/* The whole session behind the relay, when its agent offers a stop. */}
      {stopSession && <Item icon={IconStop} label={stopSession.label} hint="The session behind this thread" danger onClick={stopSession.onClick} />}
    </Sheet>
  );
}

/** A command argument: quoted when it holds a space (some model names do). */
const arg = (v: string) => (/\s/.test(v) ? `"${v.replace(/"/g, "")}"` : v);

/** The model and reasoning effort for the running agent ("model"), or for
 * another agent taking the work over ("switch"). The choices are the ones
 * the project's session.start action offers for that agent: its inputs
 * `relay_<profile>_model` and `relay_<profile>_effort`, the same two the
 * new-session form asks for. The result goes as `/model` or `/switch`.
 * Without those choices (eagent away, an older host) a switch still
 * works, with the other agent's own model and effort. */
function AgentOptionsSheet({ thread, relay, mode, onClose, onSend }: { thread: string; relay: ThreadRelay; mode: "model" | "switch"; onClose: () => void; onSend: (command: string) => void }) {
  const name = relayAgent(relay);
  const others = useMemo(() => switchProfiles(relay), [relay]);
  const [picked, setPicked] = useState(others[0] ?? "");
  // undefined while loading; null when the project's settings cannot be read.
  const [resource, setResource] = useState<SettingsResource | null | undefined>(undefined);
  // What was chosen here; null until something is.
  const [values, setValues] = useState<Record<string, unknown> | null>(null);
  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const { resources } = await controlAPI.threadSettings(thread);
        const link = resources.find((r) => r.scope !== "session");
        if (!link) throw new Error("no project settings");
        const view = await controlAPI.resource(link.id);
        if (alive) setResource(view.resource);
      } catch {
        if (alive) setResource(null);
      }
    })();
    return () => {
      alive = false;
    };
  }, [thread]);
  const profiles = useMemo(() => (resource ? relayProfiles(resource) : []), [resource]);
  const nameOf = (profile: string) => {
    const p = profiles.find((x) => x.name === profile);
    return p ? relayName(p) : agentName(profile);
  };
  const target = mode === "switch" ? picked : relay.profile || profiles.find((p) => p.agent === relay.agent)?.name || relay.name;
  const modelKey = `relay_${target}_model`;
  const effortKey = `relay_${target}_effort`;
  const action = resource?.descriptor.actions?.find((a) => a.operation === "session.start");
  // The running agent's sheet opens on what it runs now; another agent's on its own defaults.
  const running: Record<string, unknown> = mode === "model" ? { [modelKey]: relay.model || undefined, [effortKey]: relay.effort || undefined } : {};
  const built = resource && action ? startModel(resource.descriptor, action, values ?? running, (next) => setValues(next), { relay: target }) : null;
  const mine = (key: string) => key === modelKey || key === effortKey;
  let form: FormModel | null = built ? { ...built, items: built.items.filter((i) => mine(i.key)) } : null;
  if (form && built && mode === "model") {
    // Here a choice is a change to what runs, undone back to that, rather
    // than a departure from a default.
    const differs = (key: string) => String(built.state(key).value ?? "") !== String(running[key] ?? "");
    form = {
      ...form,
      items: form.items.map((i) => ({ ...i, resettable: false })),
      state: (key) => ({ ...built.state(key), pending: differs(key) }),
      revert: (keys) =>
        setValues((prev) => {
          const next = { ...(prev ?? running) };
          for (const k of keys) {
            if (running[k] === undefined) delete next[k];
            else next[k] = running[k];
          }
          return next;
        }),
    };
  }
  const has = (key: string) => !!form?.items.some((i) => i.key === key);
  const chosen = (key: string) => has(key) && form!.state(key).set;
  // What a field comes to (the choice, or what leaving it alone gives), as the command takes it.
  const value = (key: string) => (has(key) && form!.state(key).value !== undefined && form!.state(key).value !== "" ? arg(String(form!.state(key).value)) : "default");
  let command = "";
  if (mode === "switch") {
    // Left alone, the other agent starts with its own model and effort.
    command = !target ? "" : chosen(modelKey) || chosen(effortKey) ? `/switch ${target} ${chosen(modelKey) ? value(modelKey) : "default"} ${chosen(effortKey) ? value(effortKey) : "default"}` : `/switch ${target}`;
  } else if (form && form.items.some((i) => form!.state(i.key).pending)) {
    command = `/model ${value(modelKey)} ${value(effortKey)}`;
  }
  const title = mode === "model" ? `${name}'s model` : others.length === 1 ? `Switch to ${nameOf(target)}` : "Switch agent";
  return (
    <Sheet onClose={onClose} label={title} className="start-sheet agent-sheet">
      <div className="start-sheet-head">
        <h2>{title}</h2>
        <p>{mode === "model" ? `${name} starts again on the same conversation with what you choose, once the turn it is on ends.` : `${nameOf(target)} takes over in the same folder, with a brief of your instructions and ${name}'s last report. ${name} stops.`}</p>
        {mode === "switch" && others.length > 1 && (
          <div className="segmented agent-pick" role="radiogroup" aria-label="Agent to switch to">
            {others.map((p) => (
              <button key={p} type="button" role="radio" aria-checked={p === picked} className={p === picked ? "active" : ""} onClick={() => setPicked(p)}>
                {nameOf(p)}
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="start-sheet-body">
        {resource === undefined ? <p className="agent-note">Loading its choices…</p> : form && form.items.length > 0 ? <SettingsForm model={form} /> : <p className="agent-note">{mode === "switch" ? `${nameOf(target)} will start with its own model and reasoning effort.` : `${name}'s choices are not available right now. You can still type /model with a model and an effort.`}</p>}
      </div>
      <div className="start-sheet-foot">
        <button type="button" className="btn" onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="btn primary" disabled={!command} onClick={() => onSend(command)}>
          {mode === "model" ? "Change" : `Switch to ${nameOf(target)}`}
        </button>
      </div>
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

/** How many running sessions show before "Show all". */
const runningShown = 4;

/** What is already running on this machine; a row opens its thread. Each
 * row is named by its thread's title when the inbox has the thread, since
 * the agent and the folder are the same for most of them. */
export function RunningRelays({ relays }: { relays: RunningRelay[] }) {
  const now = useNow(30000);
  const threads = useStore((s) => s.threads);
  const titles = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of Object.values(threads)) {
      if (!t.title) continue;
      m.set(t.id, t.title);
      if (t.external_id) m.set(`ext:${t.external_id}`, t.title);
    }
    return m;
  }, [threads]);
  const [all, setAll] = useState(false);
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
  const shown = all ? relays : relays.slice(0, runningShown);
  return (
    <section className="running" aria-labelledby="running-title">
      <h2 className="draft-label" id="running-title">
        Running now <span className="n">{relays.length}</span>
      </h2>
      <div className="running-list">
        {shown.map((r, i) => {
          const name = agentName(r.agent, r.name);
          const folder = r.dir.replace(/\/+$/, "").split("/").pop() || r.dir;
          const title = titles.get(r.thread) ?? "";
          const named = title !== "" && title.toLowerCase() !== folder.toLowerCase();
          const age = runningFor(r.created, now);
          const when = r.state === "exited" ? ["exited", age].filter(Boolean).join(" · ") : age;
          const dot = r.state === "working" || r.state === "starting" ? "working" : r.state === "blocked" ? "blocked" : r.state === "exited" ? "exited" : "idle";
          return (
            <button key={r.target || `${r.thread}-${r.name}-${i}`} type="button" className="running-row" title={r.dir || undefined} disabled={opening === r.thread} onClick={() => void open(r)}>
              <span className={`running-dot ${dot}`} role="img" aria-label={dot === "working" ? "working" : dot === "blocked" ? "needs you" : dot === "exited" ? "exited" : "idle"} />
              <span className="running-main">
                <span className="running-title">{named ? title : name}</span>
                <span className="running-sub">
                  {named && <>{name} <span className="in">in</span> </>}
                  {folder && <span className="dir">{folder}</span>}
                </span>
              </span>
              {when && <span className={`running-age${r.state === "exited" ? " exited" : ""}`}>{when}</span>}
              <IconChevron aria-hidden="true" />
            </button>
          );
        })}
      </div>
      {relays.length > runningShown && (
        <button type="button" className="running-more" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : `Show all ${relays.length}`}
        </button>
      )}
    </section>
  );
}
