import { useSyncExternalStore } from "react";
import { api, APIError } from "./api";
import { forgetPushSubscription, resyncPushSubscription, setBadge } from "./push";
import { serverNow, syncServerTime } from "./time";
import type { Activity, Counts, Message, Question, Settings, SignupMode, Thread, User } from "./types";

export type Connection = "idle" | "connecting" | "online" | "offline";

export interface Toast {
  id: number;
  text: string;
  kind: "info" | "error" | "success";
  action?: { label: string; onClick: () => void };
}

export type ThreadLoad = "ok" | "missing" | "error";

export interface State {
  /** null while the initial auth check runs; false when signed out. */
  user: User | null | false;
  settings: Settings;
  counts: Counts;
  pushEnabled: boolean;
  attachmentsEnabled: boolean;
  version: string;
  signup: SignupMode;
  connection: Connection;
  /** True when the current state came from the on-device snapshot and has not been confirmed by the server yet. */
  fromSnapshot: boolean;
  threads: Record<string, Thread>;
  archivedLoaded: boolean;
  inboxLoaded: boolean;
  inboxCursor: string | null;
  /** The user paged to the end of the inbox; a refresh must not offer "Load older" again. */
  inboxExhausted: boolean;
  messages: Record<string, Message[]>;
  threadQuestions: Record<string, Question[]>;
  hasOlder: Record<string, boolean>;
  threadLoaded: Record<string, boolean>;
  threadError: Record<string, string>;
  /** last_read_at at the moment a thread with unread messages was opened; drives the "New" divider. */
  unreadMarker: Record<string, string>;
  /** Server time of the last status event applied per thread; older activity in any payload is ignored. */
  activitySeen: Record<string, number>;
  pending: Question[];
  toasts: Toast[];
  drafts: Record<string, string>;
  /** Thread ids the server returned for the last search, or null when not searching. */
  searchHits: { query: string; ids: string[] } | null;
}

const defaultSettings: Settings = { notify_all_messages: false, remote_mode: false };
const emptyCounts: Counts = { pending_questions: 0, unread_threads: 0, attention: 0 };

const snapshotKey = "fc.snapshot";
const draftPrefix = "fc.draft.";

interface Snapshot {
  user: User;
  settings: Settings;
  counts: Counts;
  threads: Thread[];
  version: string;
  pushEnabled: boolean;
  attachmentsEnabled: boolean;
}

function readSnapshot(): Snapshot | null {
  try {
    const raw = localStorage.getItem(snapshotKey);
    if (!raw) return null;
    const snap = JSON.parse(raw) as Snapshot;
    return snap && snap.user && Array.isArray(snap.threads) ? snap : null;
  } catch {
    return null;
  }
}

function readDrafts(): Record<string, string> {
  const out: Record<string, string> = {};
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key && key.startsWith(draftPrefix)) {
        const v = localStorage.getItem(key);
        if (v) out[key.slice(draftPrefix.length)] = v;
      }
    }
  } catch {
    // ignore
  }
  return out;
}

const snapshot = typeof localStorage !== "undefined" ? readSnapshot() : null;

let state: State = {
  user: null,
  settings: snapshot?.settings ?? defaultSettings,
  counts: snapshot?.counts ?? emptyCounts,
  pushEnabled: snapshot?.pushEnabled ?? false,
  attachmentsEnabled: snapshot?.attachmentsEnabled ?? false,
  version: snapshot?.version ?? "",
  signup: "closed",
  connection: "idle",
  fromSnapshot: !!snapshot,
  threads: Object.fromEntries((snapshot?.threads ?? []).map((t) => [t.id, t])),
  archivedLoaded: false,
  inboxLoaded: !!snapshot,
  inboxCursor: null,
  inboxExhausted: false,
  messages: {},
  threadQuestions: {},
  hasOlder: {},
  threadLoaded: {},
  threadError: {},
  unreadMarker: {},
  activitySeen: {},
  pending: [],
  toasts: [],
  drafts: typeof localStorage !== "undefined" ? readDrafts() : {},
  searchHits: null,
};

const listeners = new Set<() => void>();

function set(patch: Partial<State> | ((s: State) => Partial<State>)) {
  const next = typeof patch === "function" ? patch(state) : patch;
  state = { ...state, ...next };
  listeners.forEach((l) => l());
  if ("threads" in next || "counts" in next || "user" in next || "settings" in next) persistSnapshot();
}

export function getState(): State {
  return state;
}

export function useStore<T>(selector: (s: State) => T): T {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => selector(state),
    () => selector(state),
  );
}

// ---------------------------------------------------------------------------
// Snapshot: the inbox paints instantly on a cold open, offline or not.

let persistTimer: number | undefined;
let lastSnapshot = "";
/** Set only by a real sign-out (the user's, or a 401): the one case where
 * the on-device snapshot is dropped. A server having a bad minute is not it. */
let signedOut = false;
function persistSnapshot() {
  if (typeof localStorage === "undefined") return;
  window.clearTimeout(persistTimer);
  persistTimer = window.setTimeout(() => {
    try {
      if (!state.user) {
        if (signedOut) {
          lastSnapshot = "";
          localStorage.removeItem(snapshotKey);
        }
        return;
      }
      const threads = Object.values(state.threads)
        .filter((t) => !t.archived_at)
        .sort((a, b) => (a.last_activity_at < b.last_activity_at ? 1 : -1))
        .slice(0, 80)
        // Statuses are ephemeral; a snapshot must not resurrect one.
        .map((t) => ({ ...t, activity: null }));
      const snap: Snapshot = {
        user: state.user,
        settings: state.settings,
        counts: state.counts,
        threads,
        version: state.version,
        pushEnabled: state.pushEnabled,
        attachmentsEnabled: state.attachmentsEnabled,
      };
      const serialized = JSON.stringify(snap);
      // Status lines churn the thread map without changing the snapshot.
      if (serialized === lastSnapshot) return;
      lastSnapshot = serialized;
      localStorage.setItem(snapshotKey, serialized);
    } catch {
      // Storage full or blocked; the app works without it.
    }
  }, 400);
}

// ---------------------------------------------------------------------------
// Toasts

let toastSeq = 0;
export function toast(text: string, kind: Toast["kind"] = "info", action?: Toast["action"]): number {
  const id = ++toastSeq;
  set((s) => ({ toasts: [...s.toasts.slice(-2), { id, text, kind, action }] }));
  window.setTimeout(() => dismissToast(id), action ? 8000 : kind === "error" ? 6000 : 3200);
  return id;
}

export function dismissToast(id: number) {
  set((s) => (s.toasts.some((t) => t.id === id) ? { toasts: s.toasts.filter((t) => t.id !== id) } : {}));
}

function errorText(err: unknown): string {
  if (err instanceof APIError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
}

function isTransport(err: unknown): boolean {
  return !(err instanceof APIError) || err.status === 0;
}

// ---------------------------------------------------------------------------
// Session

/** The server said the session is gone: paint the login form and forget the device's copy. */
function markSignedOut() {
  signedOut = true;
  set({ user: false, fromSnapshot: false, threads: {}, inboxLoaded: false, inboxExhausted: false });
}

export async function bootstrap(): Promise<void> {
  try {
    const status = await api.authStatus();
    set({ signup: status.signup, pushEnabled: status.push_enabled, attachmentsEnabled: status.attachments_enabled, version: status.version });
    if (!status.authenticated || !status.user) {
      markSignedOut();
      return;
    }
    signedOut = false;
    set({ user: status.user, settings: status.user.settings });
    await loadInbox();
    connect();
    void resyncPushSubscription();
  } catch (err) {
    if (err instanceof APIError && err.status === 401) {
      markSignedOut();
      return;
    }
    // No network, or a server having a bad minute (a 503 during a rollout,
    // a 500 while the database blips): neither means the user is signed
    // out. Keep whatever the device remembers, say so, and keep trying; the
    // stream's reconnect backoff does the retrying and refreshes on success.
    if (state.user === null) set({ user: snapshot ? snapshot.user : false });
    set({ connection: "offline" });
    if (state.user) connect();
    else if (isTransport(err)) toast("You're offline. Sign in when you're back online.", "error");
    else toast(errorText(err), "error");
  }
}

export async function signIn(email: string, password: string) {
  const { user } = await api.login({ email, password });
  signedOut = false;
  set({ user, settings: user.settings, fromSnapshot: false });
  await loadInbox();
  connect();
}

export async function register(input: { email: string; password: string; display_name?: string; invite_code?: string }) {
  const { user } = await api.register(input);
  signedOut = false;
  // A first-account server closes behind its owner; an open server stays open.
  set({ user, settings: user.settings, signup: state.signup === "first" ? "closed" : state.signup, fromSnapshot: false });
  await loadInbox();
  connect();
}

/** Signs out. Without a network the server session cannot be ended, so the
 * app stays signed in and says so rather than pretending. */
export async function signOut(): Promise<boolean> {
  try {
    await api.logout();
  } catch (err) {
    if (isTransport(err)) {
      set({ connection: "offline" });
      toast("You're offline, so you're still signed in. Try again when you're back online.", "error", { label: "Retry", onClick: () => void signOut() });
      return false;
    }
    // Any HTTP answer means the cookie is gone or useless; wipe locally.
  }
  await forgetAccount();
  return true;
}

/** The server deleted the account (and with it the session); wipe the device's copy. */
export async function accountDeleted(): Promise<void> {
  await forgetAccount();
}

async function forgetAccount(): Promise<void> {
  disconnect();
  await forgetPushSubscription();
  signedOut = true;
  set({
    user: false,
    fromSnapshot: false,
    threads: {},
    messages: {},
    threadQuestions: {},
    threadLoaded: {},
    threadError: {},
    hasOlder: {},
    unreadMarker: {},
    activitySeen: {},
    drafts: {},
    pending: [],
    inboxLoaded: false,
    inboxExhausted: false,
    archivedLoaded: false,
    searchHits: null,
    counts: emptyCounts,
  });
  setBadge(0);
  try {
    lastSnapshot = "";
    localStorage.removeItem(snapshotKey);
    const keys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && k.startsWith(draftPrefix)) keys.push(k);
    }
    keys.forEach((k) => localStorage.removeItem(k));
  } catch {
    // ignore
  }
  // Attachment bytes the service worker kept for offline reading belong to
  // the account that just left.
  try {
    navigator.serviceWorker?.controller?.postMessage({ type: "clear-media" });
    if ("caches" in window) await caches.delete("fc-media-v1");
  } catch {
    // ignore
  }
}

export function setUser(user: User) {
  set({ user, settings: user.settings });
}

// ---------------------------------------------------------------------------
// Inbox

function applyCounts(counts: Counts) {
  set({ counts });
  // The badge is the number of threads that need the user: one countable thing.
  setBadge(counts.attention ?? counts.pending_questions + counts.unread_threads);
}

/** Where a thread payload came from. Events are post-commit and in stream
 * order, so their status is the truth; a REST response was computed at some
 * point after the request was issued, so its status is compared by the
 * database clock against the one the app holds. */
type Source = { kind: "event"; at?: string } | { kind: "rest"; issuedAt: number };

/** A REST source stamped now; call it before the fetch it describes. */
function rest(): Source {
  return { kind: "rest", issuedAt: serverNow() };
}

/** Merges a payload's status line with the held one and says what the
 * watermark (the DB time of the newest status seen) should become. */
function mergeActivity(s: State, t: Thread, source: Source): { thread: Thread; seen?: number } {
  const held = s.threads[t.id]?.activity ?? null;
  const heldAt = held ? new Date(held.at).getTime() : 0;
  if (source.kind === "event") {
    const seen = t.activity ? new Date(t.activity.at).getTime() : source.at ? new Date(source.at).getTime() : undefined;
    return { thread: t, seen };
  }
  if (t.activity) {
    const at = new Date(t.activity.at).getTime();
    // A slower response carrying a status older than the one on screen.
    if (at < heldAt) return { thread: { ...t, activity: held } };
    return { thread: t, seen: at };
  }
  // A null from a request issued before the held status was set says
  // nothing about that status; one issued after it says it is gone.
  if (held && source.issuedAt < heldAt) return { thread: { ...t, activity: held } };
  return { thread: t };
}

function upsertThreads(list: Thread[], source: Source) {
  set((s) => {
    const threads = { ...s.threads };
    const activitySeen = { ...s.activitySeen };
    for (const t of list) {
      const m = mergeActivity(s, t, source);
      threads[t.id] = m.thread;
      if (m.seen !== undefined) activitySeen[t.id] = Math.max(activitySeen[t.id] ?? 0, m.seen);
    }
    return { threads, activitySeen };
  });
}

export async function loadInbox(): Promise<void> {
  const source = rest();
  try {
    const [threads, pending, me] = await Promise.all([api.listThreads({ limit: 100 }), api.listPendingQuestions(), api.me()]);
    const page = threads.threads;
    const oldest = page.length ? page[page.length - 1]!.last_activity_at : "";
    // A page with more behind it says nothing about older threads; a page
    // that ends the list is the whole list, and anything active it lacks was
    // archived or deleted elsewhere.
    const partial = !!threads.next_cursor;
    set((s) => {
      const merged: Record<string, Thread> = {};
      const activitySeen = { ...s.activitySeen };
      for (const t of Object.values(s.threads)) {
        if (t.archived_at || (partial && oldest && t.last_activity_at < oldest)) merged[t.id] = t;
      }
      for (const t of page) {
        const m = mergeActivity(s, t, source);
        merged[t.id] = m.thread;
        if (m.seen !== undefined) activitySeen[t.id] = Math.max(activitySeen[t.id] ?? 0, m.seen);
      }
      return {
        threads: merged,
        activitySeen,
        inboxLoaded: true,
        fromSnapshot: false,
        // A refresh keeps the cursor from the deepest page already loaded,
        // and offers nothing once the user has reached the end.
        inboxCursor: s.inboxExhausted ? null : s.inboxLoaded && !s.fromSnapshot && s.inboxCursor !== null ? s.inboxCursor : (threads.next_cursor ?? null),
        pending: pending.questions,
        user: me.user,
        settings: me.user.settings,
        pushEnabled: me.push_enabled,
        attachmentsEnabled: me.attachments_enabled,
        version: me.version,
      };
    });
    applyCounts(me.counts);
    // A pending question whose thread is archived or past the first page
    // still needs its thread for the card's name and link.
    const missing = pending.questions.map((q) => q.thread_id).filter((id, i, all) => !state.threads[id] && all.indexOf(id) === i);
    for (const id of missing.slice(0, 10)) {
      const source = rest();
      api
        .getThread(id)
        .then((r) => upsertThreads([r.thread], source))
        .catch(() => {});
    }
  } catch (err) {
    if (err instanceof APIError && err.status === 401) {
      markSignedOut();
      return;
    }
    if (isTransport(err)) {
      set({ connection: "offline" });
      return;
    }
    toast(errorText(err), "error");
  }
}

export async function loadMoreThreads(): Promise<void> {
  const cursor = state.inboxCursor;
  if (!cursor) return;
  const source = rest();
  const res = await api.listThreads({ limit: 100, cursor });
  upsertThreads(res.threads, source);
  set({ inboxCursor: res.next_cursor ?? null, inboxExhausted: !res.next_cursor });
}

export async function loadArchived(): Promise<void> {
  const source = rest();
  const res = await api.listThreads({ archived: true, limit: 200 });
  upsertThreads(res.threads, source);
  set({ archivedLoaded: true });
}

/** Server-side search across every thread, active and archived. */
export async function searchThreads(query: string): Promise<void> {
  const q = query.trim();
  if (!q) {
    set({ searchHits: null });
    return;
  }
  try {
    const source = rest();
    const [active, archived] = await Promise.all([api.listThreads({ q, limit: 100 }), api.listThreads({ q, archived: true, limit: 100 })]);
    const hits = [...active.threads, ...archived.threads];
    upsertThreads(hits, source);
    // A slower response for an older query must not overwrite a newer one.
    if (state.searchHits && state.searchHits.query !== q && state.searchHits.query.length > q.length) return;
    set({ searchHits: { query: q, ids: hits.map((t) => t.id) } });
  } catch {
    // The local filter still applies.
  }
}

export function clearSearch() {
  set({ searchHits: null });
}

// ---------------------------------------------------------------------------
// Thread

/** Threads whose page is in flight, with what the stream delivered for them
 * meanwhile; a message that lands during the fetch is merged, not lost until
 * the next foreground. */
const loading = new Map<string, { messages: Message[]; questions: Question[] }>();

/** (created_at, id) order, the server's. */
function after(a: { created_at: string; id: string }, b: { created_at: string; id: string }): boolean {
  return a.created_at > b.created_at || (a.created_at === b.created_at && a.id > b.id);
}

export async function loadThread(id: string): Promise<ThreadLoad> {
  const source = rest();
  loading.set(id, { messages: [], questions: [] });
  try {
    const [thread, messages, questions] = await Promise.all([api.getThread(id), api.listMessages(id, { limit: 100 }), api.listThreadQuestions(id)]);
    const buffered = loading.get(id) ?? { messages: [], questions: [] };
    set((s) => {
      const errors = { ...s.threadError };
      delete errors[id];
      const unreadMarker = { ...s.unreadMarker };
      if (!unreadMarker[id] && thread.thread.unread_count > 0) unreadMarker[id] = thread.thread.last_read_at;
      // The page is the truth for its window; anything newer than its last
      // row that arrived meanwhile (or was already held) is kept after it.
      const page = messages.messages;
      const ids = new Set(page.map((m) => m.id));
      const last = page[page.length - 1];
      const extra = new Map<string, Message>();
      for (const m of [...(s.messages[id] ?? []), ...buffered.messages]) {
        if (!ids.has(m.id) && !m.deleted && (!last || after(m, last))) extra.set(m.id, m);
      }
      const merged = [...page, ...Array.from(extra.values()).sort((a, b) => (after(a, b) ? 1 : -1))];
      // Questions only move from pending to a final state, so the final one wins.
      const qs = new Map(questions.questions.map((q) => [q.id, q]));
      for (const q of buffered.questions) {
        const have = qs.get(q.id);
        if (!have || have.status === "pending") qs.set(q.id, q);
      }
      const m = mergeActivity(s, thread.thread, source);
      return {
        threads: { ...s.threads, [id]: m.thread },
        activitySeen: m.seen !== undefined ? { ...s.activitySeen, [id]: Math.max(s.activitySeen[id] ?? 0, m.seen) } : s.activitySeen,
        messages: { ...s.messages, [id]: merged },
        hasOlder: { ...s.hasOlder, [id]: messages.has_more },
        threadQuestions: { ...s.threadQuestions, [id]: Array.from(qs.values()) },
        threadLoaded: { ...s.threadLoaded, [id]: true },
        threadError: errors,
        unreadMarker,
      };
    });
    return "ok";
  } catch (err) {
    if (err instanceof APIError && err.status === 404) {
      // Deleted elsewhere: the row must not linger as a ghost in the inbox.
      forgetThread(id);
      return "missing";
    }
    set((s) => ({ threadError: { ...s.threadError, [id]: isTransport(err) ? "You're offline." : errorText(err) } }));
    return "error";
  } finally {
    loading.delete(id);
  }
}

/** Brings an open thread up to date without discarding what is loaded. */
export async function refreshThread(id: string): Promise<void> {
  const current = state.messages[id];
  if (!current || !state.threadLoaded[id]) {
    await loadThread(id);
    return;
  }
  const last = current[current.length - 1];
  const source = rest();
  try {
    const [thread, questions, newer] = await Promise.all([
      api.getThread(id),
      api.listThreadQuestions(id),
      last ? api.listMessages(id, { after: last.id, limit: 200 }) : api.listMessages(id, { limit: 100 }),
    ]);
    for (const message of newer.messages) if (message.deleted) removeMessage(id, message.id);
    if (newer.has_more && last) {
      // Too much happened; start over from the newest page.
      await loadThread(id);
      return;
    }
    set((s) => {
      const list = s.messages[id] ?? [];
      // A catch-up page carries tombstones for messages deleted meanwhile.
      const gone = new Set(newer.messages.filter((m) => m.deleted).map((m) => m.id));
      const fresh = newer.messages.filter((m) => !m.deleted);
      const seen = new Set(list.map((m) => m.id));
      const merged = last ? [...list.filter((m) => !gone.has(m.id)), ...fresh.filter((m) => !seen.has(m.id))] : fresh;
      const errors = { ...s.threadError };
      delete errors[id];
      const m = mergeActivity(s, thread.thread, source);
      return {
        threads: { ...s.threads, [id]: m.thread },
        activitySeen: m.seen !== undefined ? { ...s.activitySeen, [id]: Math.max(s.activitySeen[id] ?? 0, m.seen) } : s.activitySeen,
        messages: { ...s.messages, [id]: merged },
        hasOlder: last ? s.hasOlder : { ...s.hasOlder, [id]: newer.has_more },
        threadQuestions: { ...s.threadQuestions, [id]: questions.questions },
        threadError: errors,
      };
    });
  } catch (err) {
    if (err instanceof APIError && err.status === 404) {
      set((s) => ({ threadError: { ...s.threadError, [id]: "gone" } }));
      forgetThread(id);
    }
    // Transport errors: the stream reconnect will try again.
  }
}

export async function loadOlderMessages(id: string): Promise<number> {
  const current = state.messages[id] ?? [];
  const first = current[0];
  if (!first) return 0;
  const res = await api.listMessages(id, { before: first.id, limit: 100 });
  set((s) => ({
    messages: { ...s.messages, [id]: [...res.messages, ...(s.messages[id] ?? [])] },
    hasOlder: { ...s.hasOlder, [id]: res.has_more },
  }));
  return res.messages.length;
}

export async function markRead(id: string): Promise<void> {
  const t = state.threads[id];
  if (!t || t.unread_count === 0) return;
  // Optimistic.
  set((s) => ({ threads: { ...s.threads, [id]: { ...t, unread_count: 0, last_read_at: new Date(serverNow()).toISOString() } } }));
  try {
    const source = rest();
    const res = await api.markRead(id);
    upsertThreads([res.thread], source);
    const counts = await api.counts();
    applyCounts(counts.counts);
  } catch {
    // Will reconcile on next load.
  }
}

export class AlreadyPostedError extends Error {
  constructor() {
    super("That reply was already posted; nothing new was sent.");
  }
}

export async function sendMessage(threadId: string, body: string, attachments: string[] = [], clientKey?: string): Promise<void> {
  const source = rest();
  const res = await api.sendMessage(threadId, body, attachments, clientKey);
  appendMessage(res.message);
  upsertThreads([res.thread], source);
  // A replay of an earlier post: if what was typed differs, the server holds
  // the old text and this send must not look like a success.
  if (res.created === false && res.message.body !== body) throw new AlreadyPostedError();
}

function appendMessage(m: Message) {
  loading.get(m.thread_id)?.messages.push(m);
  set((s) => {
    const list = s.messages[m.thread_id];
    if (!list) return {};
    if (list.some((x) => x.id === m.id)) return {};
    return { messages: { ...s.messages, [m.thread_id]: [...list, m] } };
  });
}

const messageRemovalListeners = new Set<(thread: string, id: string) => void>();
export function onMessageRemoved(fn: (thread: string, id: string) => void) {
  messageRemovalListeners.add(fn);
  return () => { messageRemovalListeners.delete(fn); };
}
function removeMessage(threadId: string, id: string) {
  for (const listener of messageRemovalListeners) listener(threadId, id);
  set((s) => {
    const list = s.messages[threadId];
    if (!list || !list.some((m) => m.id === id)) return {};
    return { messages: { ...s.messages, [threadId]: list.filter((m) => m.id !== id) } };
  });
}

function upsertQuestion(q: Question) {
  loading.get(q.thread_id)?.questions.push(q);
  set((s) => {
    const list = s.threadQuestions[q.thread_id];
    let threadQuestions = s.threadQuestions;
    if (list) {
      const idx = list.findIndex((x) => x.id === q.id);
      const next = idx >= 0 ? list.map((x) => (x.id === q.id ? q : x)) : [...list, q];
      threadQuestions = { ...s.threadQuestions, [q.thread_id]: next };
    }
    let pending = s.pending.filter((x) => x.id !== q.id);
    if (q.status === "pending") pending = [q, ...pending];
    return { threadQuestions, pending };
  });
}

/** Merges a status line into its thread. The watermark is the status's own
 * time (the database clock); a clear has none, so it uses the event's. */
function applyActivity(threadId: string, activity: Activity | null, at?: string) {
  set((s) => {
    const t = s.threads[threadId];
    if (!t) return {};
    const when = activity ? new Date(activity.at).getTime() : at ? new Date(at).getTime() : serverNow();
    if (when < (s.activitySeen[threadId] ?? 0)) return {};
    return { threads: { ...s.threads, [threadId]: { ...t, activity } }, activitySeen: { ...s.activitySeen, [threadId]: when } };
  });
}

export async function answerQuestion(id: string, answer: { selected: string[]; text?: string }): Promise<void> {
  const source = rest();
  const res = await api.answerQuestion(id, answer);
  upsertQuestion(res.question);
  appendMessage(res.message);
  upsertThreads([res.thread], source);
  const counts = await api.counts().catch(() => null);
  if (counts) applyCounts(counts.counts);
}

export async function dismissQuestion(id: string): Promise<void> {
  const res = await api.dismissQuestion(id);
  upsertQuestion(res.question);
  const counts = await api.counts().catch(() => null);
  if (counts) applyCounts(counts.counts);
}

export async function updateThread(id: string, patch: { title?: string; archived?: boolean; muted?: boolean }): Promise<void> {
  const source = rest();
  const res = await api.updateThread(id, patch);
  upsertThreads([res.thread], source);
  if (patch.archived !== undefined) {
    const counts = await api.counts().catch(() => null);
    if (counts) applyCounts(counts.counts);
  }
}

export async function deleteThread(id: string): Promise<void> {
  await api.deleteThread(id);
  forgetThread(id);
}

export async function bulkUpdateThreads(
  ids: string[],
  patch: { archived?: boolean; muted?: boolean; mark_read?: true },
): Promise<{ threads: Thread[]; deleted: string[] }> {
  const source = rest();
  const body: { ids: string[]; archived?: boolean; muted?: boolean; mark_read?: true } = { ids, ...patch };
  const res = await api.bulkThreads(body);
  // Partial success: the server skips ids it cannot find; merge whatever came back.
  if (res.threads.length > 0) upsertThreads(res.threads, source);
  for (const id of res.deleted) forgetThread(id);
  try {
    const counts = await api.counts().catch(() => null);
    if (counts) applyCounts(counts.counts);
    else {
      const me = await api.me().catch(() => null);
      if (me) applyCounts(me.counts);
    }
  } catch {
    // Counts reconcile on the next load.
  }
  return res;
}

export async function bulkDeleteThreads(ids: string[]): Promise<{ threads: Thread[]; deleted: string[] }> {
  const res = await api.bulkThreads({ ids, delete: true });
  // Partial success: the server skips ids it cannot find. Forget what it
  // confirms, plus requested ids it neither returned nor confirmed (they are
  // gone from here either way); anything it returned stays.
  const alive = new Set(res.threads.map((t) => t.id));
  for (const id of ids) {
    if (!alive.has(id)) forgetThread(id);
  }
  try {
    const counts = await api.counts().catch(() => null);
    if (counts) applyCounts(counts.counts);
    else {
      const me = await api.me().catch(() => null);
      if (me) applyCounts(me.counts);
    }
  } catch {
    // Counts reconcile on the next load.
  }
  return res;
}

function forgetThread(id: string) {
  set((s) => {
    const threads = { ...s.threads };
    delete threads[id];
    const drafts = { ...s.drafts };
    delete drafts[id];
    const messages = { ...s.messages };
    delete messages[id];
    const threadQuestions = { ...s.threadQuestions };
    delete threadQuestions[id];
    const threadLoaded = { ...s.threadLoaded };
    delete threadLoaded[id];
    return { threads, drafts, messages, threadQuestions, threadLoaded, pending: s.pending.filter((q) => q.thread_id !== id) };
  });
  try {
    localStorage.removeItem(draftPrefix + id);
  } catch {
    // ignore
  }
}

export async function updateSettings(patch: Partial<Settings>): Promise<void> {
  const prev = state.settings;
  set({ settings: { ...prev, ...patch } });
  try {
    const res = await api.updateSettings(patch);
    set({ settings: res.settings });
  } catch (err) {
    set({ settings: prev });
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Drafts survive navigation, backgrounding and the app being evicted.

export function setDraft(threadId: string, text: string) {
  const trimmed = text.trim();
  set((s) => {
    const drafts = { ...s.drafts };
    if (trimmed) drafts[threadId] = text;
    else delete drafts[threadId];
    return { drafts };
  });
  try {
    if (trimmed) localStorage.setItem(draftPrefix + threadId, text);
    else localStorage.removeItem(draftPrefix + threadId);
  } catch {
    // ignore
  }
}

// ---------------------------------------------------------------------------
// Live updates

let source: EventSource | null = null;
let reconnectTimer: number | undefined;
let reconnectDelay = 1000;
let wantConnection = false;
let lastEventAt = 0;
let hiddenSince = 0;

/** The server pings every 20 seconds; silence for longer means the socket is dead. */
const staleAfter = 45000;

export function connect() {
  wantConnection = true;
  if (source) return;
  set({ connection: "connecting" });
  const es = new EventSource("/api/v1/events");
  source = es;
  lastEventAt = Date.now();

  es.addEventListener("ready", (ev) => {
    reconnectDelay = 1000;
    lastEventAt = Date.now();
    set({ connection: "online" });
    try {
      const data = JSON.parse((ev as MessageEvent).data) as { counts: Counts; at?: string };
      syncServerTime(data.at);
      applyCounts(data.counts);
    } catch {
      // ignore
    }
    // Catch up on anything that happened while disconnected.
    void loadInbox();
    const openThread = currentThreadId();
    if (openThread) void refreshThread(openThread);
  });

  es.addEventListener("ping", (ev) => {
    lastEventAt = Date.now();
    try {
      syncServerTime((JSON.parse((ev as MessageEvent).data) as { at?: string }).at);
    } catch {
      // ignore
    }
  });

  const withPayload = (name: string, fn: (d: EventPayload) => void) => {
    es.addEventListener(name, (ev) => {
      lastEventAt = Date.now();
      try {
        const data = JSON.parse((ev as MessageEvent).data) as EventPayload;
        syncServerTime(data.at);
        fn(data);
        if (data.counts) applyCounts(data.counts);
      } catch {
        // ignore malformed events
      }
    });
  };

  const event = (d: EventPayload): Source => ({ kind: "event", at: d.at });
  withPayload("thread.created", (d) => d.thread && upsertThreads([d.thread], event(d)));
  withPayload("thread.updated", (d) => d.thread && upsertThreads([d.thread], event(d)));
  withPayload("thread.activity", (d) => d.thread_id && applyActivity(d.thread_id, d.activity ?? null, d.at));
  withPayload("thread.deleted", (d) => d.thread_id && forgetThread(d.thread_id));
  withPayload("message.created", (d) => {
    if (d.message) appendMessage(d.message);
    if (d.thread) upsertThreads([d.thread], event(d));
  });
  withPayload("message.deleted", (d) => {
    if (d.message_id && d.thread) removeMessage(d.thread.id, d.message_id);
    if (d.thread) upsertThreads([d.thread], event(d));
  });
  for (const name of ["question.created", "question.answered", "question.cancelled", "question.expired", "question.dismissed"]) {
    withPayload(name, (d) => {
      if (d.question) upsertQuestion(d.question);
      if (d.thread) upsertThreads([d.thread], event(d));
    });
  }
  withPayload("settings.updated", (d) => d.settings && set({ settings: d.settings }));
  es.addEventListener("reconnect", () => {
    scheduleReconnect(500);
  });
  es.onerror = () => {
    scheduleReconnect(reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 30000);
  };
}

interface EventPayload {
  at?: string;
  thread?: Thread;
  thread_id?: string;
  message_id?: string;
  activity?: Activity | null;
  message?: Message;
  question?: Question;
  settings?: Settings;
  counts?: Counts;
}

function scheduleReconnect(delay: number) {
  if (source) {
    source.close();
    source = null;
  }
  set({ connection: "offline" });
  window.clearTimeout(reconnectTimer);
  if (!wantConnection) return;
  reconnectTimer = window.setTimeout(() => {
    if (wantConnection && document.visibilityState !== "hidden") connect();
    else if (wantConnection) scheduleReconnect(5000);
  }, delay);
}

/** Reconnects now and refreshes; for the "Reconnecting… tap to retry" strip. */
export function retryConnection() {
  reconnectDelay = 1000;
  if (state.user === null || state.user === false) {
    void bootstrap();
    return;
  }
  wantConnection = true;
  scheduleReconnect(0);
}

export function disconnect() {
  wantConnection = false;
  window.clearTimeout(reconnectTimer);
  if (source) {
    source.close();
    source = null;
  }
  set({ connection: "idle" });
}

let currentThread: string | null = null;
export function setCurrentThread(id: string | null) {
  const previous = currentThread;
  currentThread = id;
  if (previous && previous !== id) {
    // The "New" divider belongs to one visit.
    set((s) => {
      const unreadMarker = { ...s.unreadMarker };
      delete unreadMarker[previous];
      return { unreadMarker };
    });
  }
}
function currentThreadId() {
  return currentThread;
}

if (typeof document !== "undefined") {
  // A socket can sit open but dead after the phone sleeps; watch for silence.
  window.setInterval(() => {
    if (source && wantConnection && document.visibilityState === "visible" && Date.now() - lastEventAt > staleAfter) {
      scheduleReconnect(0);
    }
  }, 10000);

  // Reconnect eagerly when the app comes back to the foreground; browsers
  // silently drop EventSource connections while a PWA is backgrounded.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      hiddenSince = Date.now();
      return;
    }
    if (!wantConnection) return;
    const away = hiddenSince ? Date.now() - hiddenSince : 0;
    if (!source || away > 30000) {
      // Assume the socket died while we were away; a fresh one replays state.
      scheduleReconnect(0);
    } else {
      void loadInbox();
      const openThread = currentThreadId();
      if (openThread) void refreshThread(openThread);
    }
  });
  window.addEventListener("online", () => {
    if (wantConnection && !source) scheduleReconnect(0);
  });
  window.addEventListener("offline", () => {
    if (wantConnection) scheduleReconnect(2000);
  });
  window.addEventListener("focus", () => {
    if (wantConnection && !source) connect();
  });
}

// Service worker messages (e.g. a notification tap) trigger refreshes.
if (typeof navigator !== "undefined" && "serviceWorker" in navigator) {
  navigator.serviceWorker.addEventListener("message", (ev) => {
    const data = ev.data as { type?: string } | undefined;
    if (data?.type === "refresh" && state.user) {
      void loadInbox();
      const openThread = currentThreadId();
      if (openThread) void refreshThread(openThread);
    }
  });
}
