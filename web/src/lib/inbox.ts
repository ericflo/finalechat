// What the inbox shows for a thread and in what order. Pure functions over
// the thread objects the API returns, so the rules can be tested without a
// browser (web/tests/inbox.mjs).

import type { Thread } from "./types";

/** The one thing a row's marker says: amber, green, accent, or nothing. */
export type RowState = "needs-you" | "working" | "unread" | "idle";

export type InboxView = "all" | "unread" | "working" | "live" | "archived";

const inboxViews: readonly InboxView[] = ["all", "unread", "working", "live", "archived"];

export function parseInboxView(raw: string | null | undefined): InboxView {
  return raw && (inboxViews as readonly string[]).includes(raw) ? (raw as InboxView) : "all";
}

export function inboxViewPath(view: InboxView): string {
  return view === "all" ? "/" : `/?view=${view}`;
}

/** An agent is working when it says so and the status has not lapsed. An
 * agent waiting on the user is not working. */
export function isWorking(t: Thread, now: number): boolean {
  const a = t.activity;
  return !!a && a.kind !== "waiting" && new Date(a.expires_at).getTime() > now;
}

/** Whether the agent a thread relays to is stopped on something on its
 * screen (thread meta `relay.state` "blocked"): the session waits, but on
 * the user to look, unlike an agent that is only ready for a message. */
export function relayBlocked(t: Pick<Thread, "meta"> | undefined): boolean {
  const relay = t?.meta?.relay;
  return !!relay && typeof relay === "object" && !Array.isArray(relay) && (relay as Record<string, unknown>).state === "blocked";
}

export function rowState(t: Thread, now: number): RowState {
  if (t.archived_at) return "idle";
  if (t.pending_questions > 0) return "needs-you";
  if (isWorking(t, now)) return "working";
  if (t.unread_count > 0) return "unread";
  return "idle";
}

/** The thread's summary: `description`, falling back to its `summary` alias. */
export function threadSummary(t: Thread): string {
  return (t.description || t.summary || "").trim();
}

export function threadTitle(t: Thread): string {
  return t.title || t.agent || "Untitled thread";
}

/** `live` holds the threads whose agent session is running, by id. */
export function inView(t: Thread, view: InboxView, now: number, live: Record<string, unknown> = {}): boolean {
  if (view === "archived") return !!t.archived_at;
  if (t.archived_at) return false;
  if (view === "live") return !!live[t.id];
  if (view === "unread") return t.unread_count > 0 || t.pending_questions > 0;
  if (view === "working") return isWorking(t, now);
  return true;
}

/** Threads with a question waiting come first; then newest activity. */
export function byAttentionThenActivity(a: Thread, b: Thread): number {
  const na = !a.archived_at && a.pending_questions > 0 ? 1 : 0;
  const nb = !b.archived_at && b.pending_questions > 0 ? 1 : 0;
  if (na !== nb) return nb - na;
  if (a.last_activity_at !== b.last_activity_at) return a.last_activity_at < b.last_activity_at ? 1 : -1;
  return a.id < b.id ? 1 : -1;
}

/** The agent's name says something only when the list has more than one. */
export function agentsDiffer(threads: Thread[]): boolean {
  let first: string | null = null;
  for (const t of threads) {
    const a = t.agent.trim();
    if (!a) continue;
    if (first === null) first = a;
    else if (a !== first) return true;
  }
  return false;
}

/** How long an agent has been busy, for a list row: "now", "12m", "2h 05m". */
export function busyFor(since: string, now: number): string {
  const secs = Math.max(0, Math.floor((now - new Date(since).getTime()) / 1000));
  if (secs < 60) return "now";
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, "0")}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// ---------------------------------------------------------------------------
// Order hold. Agents post while you read, and every post moves its thread to
// the top. Sorting live means the list reshuffles between two taps: you open
// a thread, come back, and the row you meant to open next is somewhere else.
// So the order you last saw is held while you are using the list, and catches
// up with the latest activity once you have left it alone for a while.

/** How long after the reader last touched the list its order stays put. */
export const orderHoldMs = 3 * 60 * 1000;

export interface OrderHold {
  /** Which list this is (view, filter, search); another list starts from the latest order. */
  view: string;
  ids: string[];
  /** When the reader last touched the list, or 0 if they have not. */
  touchedAt: number;
}

/**
 * Returns the ids to show, in order, and the hold to keep for next time.
 * `latest` is the order the list would have if it were sorted right now.
 * While the hold is fresh, threads keep the places they had and threads the
 * hold has not seen go on top; otherwise `latest` is taken as it is.
 */
export function heldOrder(hold: OrderHold | null, view: string, latest: string[], now: number): OrderHold {
  // Another list starts from the latest order. The reader is still the same
  // reader, so the time they last touched the screen carries over.
  if (!hold || hold.view !== view) return { view, ids: latest, touchedAt: hold?.touchedAt ?? 0 };
  if (!hold.touchedAt || now - hold.touchedAt >= orderHoldMs) return { view, ids: latest, touchedAt: hold.touchedAt };
  const present = new Set(latest);
  const known = new Set(hold.ids);
  const kept = hold.ids.filter((id) => present.has(id));
  const fresh = latest.filter((id) => !known.has(id));
  return { view, ids: [...fresh, ...kept], touchedAt: hold.touchedAt };
}

/** True when two orders list the same ids in the same places. */
export function sameOrder(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((id, i) => id === b[i]);
}
