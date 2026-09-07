import { useSyncExternalStore } from "react";
import { api, APIError } from "./api";
import { setBadge } from "./push";
import { syncServerTime } from "./time";
import type { Counts, Message, Question, Settings, Thread, User } from "./types";

export type Connection = "idle" | "connecting" | "online" | "offline";

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
  threads: Record<string, Thread>;
  archivedLoaded: boolean;
  inboxLoaded: boolean;
  inboxCursor: string | null;
  messages: Record<string, Message[]>;
  threadQuestions: Record<string, Question[]>;
  hasOlder: Record<string, boolean>;
  threadLoaded: Record<string, boolean>;
  pending: Question[];
  toast: { id: number; text: string; kind: "info" | "error" | "success" } | null;
}

const defaultSettings: Settings = { notify_all_messages: false, remote_mode: false };

let state: State = {
  user: null,
  settings: defaultSettings,
  counts: { pending_questions: 0, unread_threads: 0 },
  pushEnabled: false,
  attachmentsEnabled: false,
  version: "",
  signup: "closed",
  connection: "idle",
  threads: {},
  archivedLoaded: false,
  inboxLoaded: false,
  inboxCursor: null,
  messages: {},
  threadQuestions: {},
  hasOlder: {},
  threadLoaded: {},
  pending: [],
  toast: null,
};

const listeners = new Set<() => void>();

function set(patch: Partial<State> | ((s: State) => Partial<State>)) {
  const next = typeof patch === "function" ? patch(state) : patch;
  state = { ...state, ...next };
  listeners.forEach((l) => l());
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

let toastSeq = 0;
let toastTimer: number | undefined;
export function toast(text: string, kind: "info" | "error" | "success" = "info") {
  const id = ++toastSeq;
  set({ toast: { id, text, kind } });
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    if (state.toast?.id === id) set({ toast: null });
  }, kind === "error" ? 6000 : 3200);
}

export function dismissToast() {
  set({ toast: null });
}

function errorText(err: unknown): string {
  if (err instanceof APIError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
}

// ---------------------------------------------------------------------------
// Session

export async function bootstrap(): Promise<void> {
  try {
    const status = await api.authStatus();
    set({ signup: status.signup, pushEnabled: status.push_enabled, attachmentsEnabled: status.attachments_enabled, version: status.version });
    if (!status.authenticated || !status.user) {
      set({ user: false });
      return;
    }
    set({ user: status.user, settings: status.user.settings });
    await loadInbox();
    connect();
  } catch (err) {
    set({ user: false });
    toast(errorText(err), "error");
  }
}

export async function signIn(email: string, password: string) {
  const { user } = await api.login({ email, password });
  set({ user, settings: user.settings });
  await loadInbox();
  connect();
}

export async function register(input: { email: string; password: string; display_name?: string; invite_code?: string }) {
  const { user } = await api.register(input);
  set({ user, settings: user.settings, signup: "closed" });
  await loadInbox();
  connect();
}

export async function signOut() {
  disconnect();
  try {
    await api.logout();
  } finally {
    set({
      user: false,
      threads: {},
      messages: {},
      threadQuestions: {},
      pending: [],
      inboxLoaded: false,
      archivedLoaded: false,
      counts: { pending_questions: 0, unread_threads: 0 },
    });
    setBadge(0);
  }
}

export function setUser(user: User) {
  set({ user, settings: user.settings });
}

// ---------------------------------------------------------------------------
// Inbox

function applyCounts(counts: Counts) {
  set({ counts });
  setBadge(counts.pending_questions + counts.unread_threads);
}

function upsertThreads(list: Thread[]) {
  set((s) => {
    const threads = { ...s.threads };
    for (const t of list) threads[t.id] = t;
    return { threads };
  });
}

export async function loadInbox(): Promise<void> {
  try {
    const [threads, pending, me] = await Promise.all([api.listThreads({ limit: 100 }), api.listPendingQuestions(), api.me()]);
    set((s) => {
      const merged: Record<string, Thread> = {};
      // Keep archived threads we already know about; refresh active ones.
      for (const t of Object.values(s.threads)) if (t.archived_at) merged[t.id] = t;
      for (const t of threads.threads) merged[t.id] = t;
      return {
        threads: merged,
        inboxLoaded: true,
        inboxCursor: threads.next_cursor ?? null,
        pending: pending.questions,
        user: me.user,
        settings: me.user.settings,
        pushEnabled: me.push_enabled,
        attachmentsEnabled: me.attachments_enabled,
        version: me.version,
      };
    });
    applyCounts(me.counts);
  } catch (err) {
    if (err instanceof APIError && err.status === 401) {
      set({ user: false });
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

// ---------------------------------------------------------------------------
// Thread

export async function loadThread(id: string): Promise<boolean> {
  try {
    const [thread, messages, questions] = await Promise.all([api.getThread(id), api.listMessages(id, { limit: 100 }), api.listThreadQuestions(id)]);
    set((s) => ({
      threads: { ...s.threads, [id]: thread.thread },
      messages: { ...s.messages, [id]: messages.messages },
      hasOlder: { ...s.hasOlder, [id]: messages.has_more },
      threadQuestions: { ...s.threadQuestions, [id]: questions.questions },
      threadLoaded: { ...s.threadLoaded, [id]: true },
    }));
    return true;
  } catch (err) {
    if (err instanceof APIError && err.status === 404) return false;
    toast(errorText(err), "error");
    return false;
  }
}

export async function loadOlderMessages(id: string): Promise<void> {
  const current = state.messages[id] ?? [];
  const first = current[0];
  if (!first) return;
  const res = await api.listMessages(id, { before: first.id, limit: 100 });
  set((s) => ({
    messages: { ...s.messages, [id]: [...res.messages, ...(s.messages[id] ?? [])] },
    hasOlder: { ...s.hasOlder, [id]: res.has_more },
  }));
}

export async function markRead(id: string): Promise<void> {
  const t = state.threads[id];
  if (!t || t.unread_count === 0) return;
  // Optimistic.
  set((s) => ({ threads: { ...s.threads, [id]: { ...t, unread_count: 0, last_read_at: new Date().toISOString() } } }));
  try {
    const res = await api.markRead(id);
    upsertThreads([res.thread]);
    const counts = await api.counts();
    applyCounts(counts.counts);
  } catch {
    // Will reconcile on next load.
  }
}

export async function sendMessage(threadId: string, body: string, attachments: string[] = []): Promise<void> {
  const res = await api.sendMessage(threadId, body, attachments);
  appendMessage(res.message);
  upsertThreads([res.thread]);
}

function appendMessage(m: Message) {
  set((s) => {
    const list = s.messages[m.thread_id];
    if (!list) return {};
    if (list.some((x) => x.id === m.id)) return {};
    return { messages: { ...s.messages, [m.thread_id]: [...list, m] } };
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

export async function answerQuestion(id: string, answer: { selected: string[]; text?: string }): Promise<void> {
  const res = await api.answerQuestion(id, answer);
  upsertQuestion(res.question);
  appendMessage(res.message);
  upsertThreads([res.thread]);
  const counts = await api.counts().catch(() => null);
  if (counts) applyCounts(counts.counts);
}

export async function updateThread(id: string, patch: { title?: string; archived?: boolean; muted?: boolean }): Promise<void> {
  const res = await api.updateThread(id, patch);
  upsertThreads([res.thread]);
}

export async function deleteThread(id: string): Promise<void> {
  await api.deleteThread(id);
  set((s) => {
    const threads = { ...s.threads };
    delete threads[id];
    return { threads, pending: s.pending.filter((q) => q.thread_id !== id) };
  });
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
// Live updates

let source: EventSource | null = null;
let reconnectTimer: number | undefined;
let reconnectDelay = 1000;
let wantConnection = false;

export function connect() {
  wantConnection = true;
  if (source) return;
  set({ connection: "connecting" });
  const es = new EventSource("/api/v1/events");
  source = es;

  es.addEventListener("ready", (ev) => {
    reconnectDelay = 1000;
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
    if (openThread) void loadThread(openThread);
  });

  const withPayload = (name: string, fn: (d: EventPayload) => void) => {
    es.addEventListener(name, (ev) => {
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
  withPayload("thread.activity", (d) => d.thread && upsertThreads([d.thread]));
  withPayload("thread.deleted", (d) => {
    if (!d.thread_id) return;
    const id = d.thread_id;
    set((s) => {
      const threads = { ...s.threads };
      delete threads[id];
      return { threads, pending: s.pending.filter((q) => q.thread_id !== id) };
    });
  });
  withPayload("message.created", (d) => {
    if (d.message) appendMessage(d.message);
    if (d.thread) upsertThreads([d.thread]);
  });
  for (const name of ["question.created", "question.answered", "question.cancelled", "question.expired"]) {
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
  currentThread = id;
}
function currentThreadId() {
  return currentThread;
}

// Reconnect eagerly when the app comes back to the foreground; browsers
// silently drop EventSource connections while a PWA is backgrounded.
if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && wantConnection) {
      if (!source) connect();
      else void loadInbox();
    }
  });
  window.addEventListener("online", () => {
    if (wantConnection && !source) connect();
  });
  window.addEventListener("focus", () => {
    if (wantConnection && !source) connect();
  });
}

// Service worker messages (e.g. a notification tap) trigger refreshes.
if (typeof navigator !== "undefined" && "serviceWorker" in navigator) {
  navigator.serviceWorker.addEventListener("message", (ev) => {
    const data = ev.data as { type?: string } | undefined;
    if (data?.type === "refresh" && state.user) void loadInbox();
  });
}
