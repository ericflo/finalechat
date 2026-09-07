import { useSyncExternalStore } from "react";
import { api, APIError } from "./api";
import { forgetPushSubscription, resyncPushSubscription, setBadge } from "./push";
import { serverNow, syncServerTime } from "./time";
import type { Activity, Counts, Message, Question, Settings, Thread, User } from "./types";

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
  signup: "open" | "invite" | "closed";
  connection: Connection;
  /** True when the current state came from the on-device snapshot and has not been confirmed by the server yet. */
  fromSnapshot: boolean;
  threads: Record<string, Thread>;
  archivedLoaded: boolean;
  inboxLoaded: boolean;
  inboxCursor: string | null;
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
function persistSnapshot() {
  if (typeof localStorage === "undefined") return;
  window.clearTimeout(persistTimer);
  persistTimer = window.setTimeout(() => {
    try {
      if (!state.user) {
        lastSnapshot = "";
        localStorage.removeItem(snapshotKey);
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

export async function bootstrap(): Promise<void> {
  try {
    const status = await api.authStatus();
    set({ signup: status.signup, pushEnabled: status.push_enabled, attachmentsEnabled: status.attachments_enabled, version: status.version });
    if (!status.authenticated || !status.user) {
      set({ user: false, fromSnapshot: false, threads: {}, inboxLoaded: false });
      return;
    }
    set({ user: status.user, settings: status.user.settings });
    await loadInbox();
    connect();
    void resyncPushSubscription();
  } catch (err) {
    if (!isTransport(err)) {
      set({ user: false, fromSnapshot: false });
      toast(errorText(err), "error");
      return;
    }
    // No network. Keep whatever the device remembers and keep trying.
    if (state.user === null) set({ user: snapshot ? snapshot.user : false });
    set({ connection: "offline" });
    if (state.user) connect();
    else toast("You're offline. Sign in when you're back online.", "error");
  }
}

export async function signIn(email: string, password: string) {
  const { user } = await api.login({ email, password });
  set({ user, settings: user.settings, fromSnapshot: false });
  await loadInbox();
  connect();
}

export async function register(input: { email: string; password: string; display_name?: string; invite_code?: string }) {
  const { user } = await api.register(input);
  set({ user, settings: user.settings, signup: "closed", fromSnapshot: false });
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
  disconnect();
  await forgetPushSubscription();
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
  return true;
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

/** Stores threads, keeping the status line the app already knows to be newer
 * than what a slower REST response carries. */
function withKnownActivity(s: State, t: Thread): Thread {
  const seen = s.activitySeen[t.id];
  if (!seen) return t;
  if (t.activity && new Date(t.activity.at).getTime() < seen) return { ...t, activity: s.threads[t.id]?.activity ?? null };
  if (!t.activity && s.threads[t.id]?.activity && new Date(s.threads[t.id]!.activity!.at).getTime() > seen) return { ...t, activity: s.threads[t.id]!.activity };
  return t;
}

function upsertThreads(list: Thread[]) {
  set((s) => {
    const threads = { ...s.threads };
    for (const t of list) threads[t.id] = withKnownActivity(s, t);
    return { threads };
  });
}

export async function loadInbox(): Promise<void> {
  try {
    const [threads, pending, me] = await Promise.all([api.listThreads({ limit: 100 }), api.listPendingQuestions(), api.me()]);
    const page = threads.threads;
    const oldest = page.length ? page[page.length - 1]!.last_activity_at : "";
    set((s) => {
      const merged: Record<string, Thread> = {};
      for (const t of Object.values(s.threads)) {
        // Keep archived threads we know, and active threads older than this
        // page (paged in earlier; their absence here says nothing). Active
        // threads inside the page's window that the page lacks were
        // archived or deleted elsewhere and drop out.
        if (t.archived_at || (oldest && t.last_activity_at < oldest)) merged[t.id] = t;
      }
      for (const t of page) merged[t.id] = withKnownActivity(s, t);
      return {
        threads: merged,
        inboxLoaded: true,
        fromSnapshot: false,
        // A refresh keeps the cursor from the deepest page already loaded.
        inboxCursor: s.inboxLoaded && !s.fromSnapshot && s.inboxCursor !== null ? s.inboxCursor : (threads.next_cursor ?? null),
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
      api
        .getThread(id)
        .then((r) => upsertThreads([r.thread]))
        .catch(() => {});
    }
  } catch (err) {
    if (err instanceof APIError && err.status === 401) {
      set({ user: false, fromSnapshot: false });
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
  const res = await api.listThreads({ limit: 100, cursor });
  upsertThreads(res.threads);
  set({ inboxCursor: res.next_cursor ?? null });
}

export async function loadArchived(): Promise<void> {
  const res = await api.listThreads({ archived: true, limit: 200 });
  upsertThreads(res.threads);
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
    const [active, archived] = await Promise.all([api.listThreads({ q, limit: 100 }), api.listThreads({ q, archived: true, limit: 100 })]);
    const hits = [...active.threads, ...archived.threads];
    upsertThreads(hits);
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

export async function loadThread(id: string): Promise<ThreadLoad> {
  try {
    const [thread, messages, questions] = await Promise.all([api.getThread(id), api.listMessages(id, { limit: 100 }), api.listThreadQuestions(id)]);
    set((s) => {
      const errors = { ...s.threadError };
      delete errors[id];
      const unreadMarker = { ...s.unreadMarker };
      if (!unreadMarker[id] && thread.thread.unread_count > 0) unreadMarker[id] = thread.thread.last_read_at;
      return {
        threads: { ...s.threads, [id]: withKnownActivity(s, thread.thread) },
        messages: { ...s.messages, [id]: messages.messages },
        hasOlder: { ...s.hasOlder, [id]: messages.has_more },
        threadQuestions: { ...s.threadQuestions, [id]: questions.questions },
        threadLoaded: { ...s.threadLoaded, [id]: true },
        threadError: errors,
        unreadMarker,
      };
    });
    return "ok";
  } catch (err) {
    if (err instanceof APIError && err.status === 404) return "missing";
    set((s) => ({ threadError: { ...s.threadError, [id]: isTransport(err) ? "You're offline." : errorText(err) } }));
    return "error";
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
  try {
    const [thread, questions, newer] = await Promise.all([
      api.getThread(id),
      api.listThreadQuestions(id),
      last ? api.listMessages(id, { after: last.id, limit: 200 }) : api.listMessages(id, { limit: 100 }),
    ]);
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
      return {
        threads: { ...s.threads, [id]: withKnownActivity(s, thread.thread) },
        messages: { ...s.messages, [id]: merged },
        hasOlder: last ? s.hasOlder : { ...s.hasOlder, [id]: newer.has_more },
        threadQuestions: { ...s.threadQuestions, [id]: questions.questions },
        threadError: errors,
      };
    });
  } catch (err) {
    if (err instanceof APIError && err.status === 404) {
      set((s) => ({ threadError: { ...s.threadError, [id]: "gone" } }));
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
    const res = await api.markRead(id);
    upsertThreads([res.thread]);
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
  const res = await api.sendMessage(threadId, body, attachments, clientKey);
  appendMessage(res.message);
  upsertThreads([res.thread]);
  // A replay of an earlier post: if what was typed differs, the server holds
  // the old text and this send must not look like a success.
  if (res.created === false && res.message.body !== body) throw new AlreadyPostedError();
}

function appendMessage(m: Message) {
  set((s) => {
    const list = s.messages[m.thread_id];
    if (!list) return {};
    if (list.some((x) => x.id === m.id)) return {};
    return { messages: { ...s.messages, [m.thread_id]: [...list, m] } };
  });
}

function removeMessage(threadId: string, id: string) {
  set((s) => {
    const list = s.messages[threadId];
    if (!list || !list.some((m) => m.id === id)) return {};
    return { messages: { ...s.messages, [threadId]: list.filter((m) => m.id !== id) } };
  });
}

function upsertQuestion(q: Question) {
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

/** Merges a status line into its thread; an event older than what we hold is ignored. */
function applyActivity(threadId: string, activity: Activity | null, at?: string) {
  set((s) => {
    const t = s.threads[threadId];
    if (!t) return {};
    const held = t.activity;
    const when = at ? new Date(at).getTime() : Date.now();
    if (held && when < new Date(held.at).getTime()) return {};
    if (s.activitySeen[threadId] && when < s.activitySeen[threadId]!) return {};
    return { threads: { ...s.threads, [threadId]: { ...t, activity } }, activitySeen: { ...s.activitySeen, [threadId]: when } };
  });
}

export async function answerQuestion(id: string, answer: { selected: string[]; text?: string }): Promise<void> {
  const res = await api.answerQuestion(id, answer);
  upsertQuestion(res.question);
  appendMessage(res.message);
  upsertThreads([res.thread]);
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
  const res = await api.updateThread(id, patch);
  upsertThreads([res.thread]);
  if (patch.archived !== undefined) {
    const counts = await api.counts().catch(() => null);
    if (counts) applyCounts(counts.counts);
  }
}

export async function deleteThread(id: string): Promise<void> {
  await api.deleteThread(id);
  forgetThread(id);
}

function forgetThread(id: string) {
  set((s) => {
    const threads = { ...s.threads };
    delete threads[id];
    const drafts = { ...s.drafts };
    delete drafts[id];
    return { threads, drafts, pending: s.pending.filter((q) => q.thread_id !== id) };
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

  withPayload("thread.created", (d) => d.thread && upsertThreads([d.thread]));
  withPayload("thread.updated", (d) => d.thread && upsertThreads([d.thread]));
  withPayload("thread.activity", (d) => d.thread_id && applyActivity(d.thread_id, d.activity ?? null, d.at));
  withPayload("thread.deleted", (d) => d.thread_id && forgetThread(d.thread_id));
  withPayload("message.created", (d) => {
    if (d.message) appendMessage(d.message);
    if (d.thread) upsertThreads([d.thread]);
  });
  withPayload("message.deleted", (d) => {
    if (d.message_id && d.thread) removeMessage(d.thread.id, d.message_id);
    if (d.thread) upsertThreads([d.thread]);
  });
  for (const name of ["question.created", "question.answered", "question.cancelled", "question.expired", "question.dismissed"]) {
    withPayload(name, (d) => {
      if (d.question) upsertQuestion(d.question);
      if (d.thread) upsertThreads([d.thread]);
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
