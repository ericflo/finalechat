import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { AttachmentList, ImageViewer, UploadTray, type PendingUpload } from "../components/Attachments";
import { Avatar, ConfirmSheet, CopyButton, Sheet, sheetsOpen } from "../components/Common";
import { IconAlert, IconArchive, IconAttach, IconBell, IconBellOff, IconBranch, IconCoins, IconCopy, IconCpu, IconDown, IconEdit, IconFolder, IconMore, IconSend, IconServer, IconTrash } from "../components/Icons";
import { api } from "../lib/api";
import { formatElapsed, useElapsed, useLiveActivity } from "../lib/activity";
import { QuestionCard } from "../components/QuestionCard";
import { TopBar } from "../components/TopBar";
import { renderMarkdown, renderText } from "../lib/markdown";
import { navigate } from "../lib/router";
import { AlreadyPostedError, deleteThread, loadOlderMessages, loadThread, markRead, onMessageRemoved, sendMessage, setCurrentThread, setDraft, toast, updateThread, useStore, type ThreadLoad } from "../lib/store";
import { dayLabel, fullDateTime, sameDay, shortTime } from "../lib/time";
import type { Activity, Attachment, Message, Question, Thread } from "../lib/types";
import { ThreadSettingsPanel } from "../components/ThreadSettings";
import { ThreadArtifacts } from "./Artifacts";
import { InspectMessage } from "../components/InspectMessage";

type Item = { kind: "message"; at: string; m: Message } | { kind: "question"; at: string; q: Question };

const itemKey = (it: Item) => (it.kind === "message" ? it.m.id : it.q.id);

function groupedWith(a: Item | undefined, b: Item | undefined): boolean {
  if (!a || !b || a.kind !== "message" || b.kind !== "message") return false;
  if (a.m.sender !== b.m.sender || (a.m.origin || "") !== (b.m.origin || "")) return false;
  if (!sameDay(a.at, b.at)) return false;
  return Math.abs(new Date(b.at).getTime() - new Date(a.at).getTime()) < 3 * 60 * 1000;
}

export function ThreadScreen({ id, highlightQuestion, highlightMessage = null, settingsOpen = false }: { id: string; highlightQuestion: string | null; highlightMessage?: string | null; settingsOpen?: boolean }) {
  const thread = useStore((s) => s.threads[id]);
  const messages = useStore((s) => s.messages[id]);
  const questions = useStore((s) => s.threadQuestions[id]);
  const loaded = useStore((s) => s.threadLoaded[id]);
  const hasOlder = useStore((s) => s.hasOlder[id]);
  const error = useStore((s) => s.threadError[id]);
  const unreadMarker = useStore((s) => s.unreadMarker[id]);
  const [load, setLoad] = useState<ThreadLoad | null>(null);
  const [menu, setMenu] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [viewing, setViewing] = useState<{ items: Attachment[]; index: number } | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasArtifacts, setHasArtifacts] = useState(false);
  const [focusedMessage, setFocusedMessage] = useState<Message | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const dividerRef = useRef<HTMLDivElement>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const lastCount = useRef(0);
  const lastTail = useRef<string | null>(null);
  // Keyed to the oldest message, not the oldest item: an old question can
  // sit above the first loaded message and would otherwise hold the key.
  const restore = useRef<{ height: number; top: number; head: string | null } | null>(null);
  // Set by the composer before a send: your own message always comes into
  // view, however far up you had scrolled.
  const justSent = useRef(false);
  const initialised = useRef<string | null>(null);
  const activity = useLiveActivity(thread?.activity);

  useEffect(() => {
    let alive = true; setFocusedMessage(null);
    const unsubscribe = onMessageRemoved((thread, message) => { if (thread === id && message === highlightMessage) { alive = false; setFocusedMessage(null); } });
    if (highlightMessage) void api.getMessage(highlightMessage).then(r => {
      if (r.message.thread_id !== id) throw new Error("Message belongs to another thread.");
      if (alive) setFocusedMessage(r.message);
    }).catch(() => { if (alive) toast("This message is no longer available in this thread.", "error"); });
    return () => { alive = false; unsubscribe(); };
  }, [id, highlightMessage]);
  useLayoutEffect(() => {
    if (loaded && focusedMessage) {
      document.getElementById(`message-${focusedMessage.id}`)?.scrollIntoView({ block: "center" });
      setAtBottom(false);
    }
  }, [loaded, focusedMessage]);

  useEffect(() => {
    setCurrentThread(id);
    setLoad(null);
    initialised.current = null;
    lastTail.current = null;
    lastCount.current = 0;
    setUnseen(0);
    setAtBottom(true);
    void loadThread(id).then(setLoad);
    return () => setCurrentThread(null);
  }, [id]);

  const retry = useCallback(() => {
    setLoad(null);
    void loadThread(id).then(setLoad);
  }, [id]);

  // Mark as read when viewing and new agent messages arrive while visible.
  useEffect(() => {
    if (!thread || !loaded) return;
    if (thread.unread_count > 0 && document.visibilityState === "visible") void markRead(id);
  }, [id, thread, loaded]);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    // The answer to a question is shown on its card; the transcript copy the
    // server keeps for agents would only repeat it here. An answer row whose
    // question was not answered here (an agent mirroring a decision made in
    // its terminal after withdrawing the card) is a reply worth seeing.
    const answered = new Set((questions ?? []).filter((q) => q.status === "answered").map((q) => q.id));
    for (const m of messages ?? []) {
      const folded = m.meta.kind === "answer" && typeof m.meta.question_id === "string" && answered.has(m.meta.question_id);
      if (!folded) out.push({ kind: "message", at: m.created_at, m });
    }
    for (const q of questions ?? []) out.push({ kind: "question", at: q.created_at, q });
    out.sort((a, b) => (a.at < b.at ? -1 : a.at > b.at ? 1 : 0));
    return out;
  }, [messages, questions]);

  // Where the "New" divider goes: before the first agent item after last_read_at.
  const newIndex = useMemo(() => {
    if (!unreadMarker) return -1;
    return items.findIndex((it) => it.at > unreadMarker && (it.kind === "question" || it.m.sender !== "user"));
  }, [items, unreadMarker]);

  // The document, not the sentinel: the sticky composer sits in flow after
  // the list, so the true bottom leaves the last bubble clear of it.
  const scrollToBottom = useCallback((behavior: ScrollBehavior = "auto") => {
    const el = document.scrollingElement;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior });
  }, []);

  // Scroll management: stick to the bottom for appended items, hold the
  // anchor for prepended history, and never move on a shrink.
  useLayoutEffect(() => {
    if (!loaded) return;
    const el = document.scrollingElement;
    const head = messages?.[0]?.id ?? null;
    // The anchor restore belongs to the prepend: only spend it when the first
    // item changed. A live message that lands first is handled as an append
    // below and the restore waits for the history page.
    if (restore.current && el && head !== restore.current.head) {
      el.scrollTop = restore.current.top + (el.scrollHeight - restore.current.height);
      restore.current = null;
      lastTail.current = items.length ? itemKey(items[items.length - 1] as Item) : null;
      lastCount.current = items.length;
      return;
    }
    const tail = items.length ? itemKey(items[items.length - 1] as Item) : null;
    if (initialised.current !== id) {
      initialised.current = id;
      lastTail.current = tail;
      lastCount.current = items.length;
      if (highlightQuestion || highlightMessage) return; // the selected item scrolls itself into view
      if (newIndex >= 0 && dividerRef.current) dividerRef.current.scrollIntoView({ block: "start" });
      else scrollToBottom();
      return;
    }
    const appended = tail !== null && tail !== lastTail.current;
    if (appended) {
      if (atBottom || justSent.current) {
        justSent.current = false;
        scrollToBottom();
        if (!atBottom) {
          setAtBottom(true);
          setUnseen(0);
        }
      } else setUnseen((n) => n + Math.max(1, items.length - lastCount.current));
    }
    lastTail.current = tail;
    lastCount.current = items.length;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, loaded, id]);

  // Keep the agent's status bubble in view while the user is at the bottom.
  useLayoutEffect(() => {
    if (activity && atBottom && initialised.current === id) scrollToBottom();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activity?.text, !!activity]);

  const onScroll = useCallback(() => {
    const el = document.scrollingElement;
    // A sheet pins the body; the scroll it causes is not the reader moving.
    if (!el || sheetsOpen > 0) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    const nearBottom = distance < 80;
    setAtBottom(nearBottom);
    if (nearBottom) setUnseen(0);
  }, []);

  useEffect(() => {
    window.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
    return () => window.removeEventListener("scroll", onScroll);
  }, [onScroll]);

  const loadOlder = async () => {
    const el = document.scrollingElement;
    if (!el || loadingOlder) return;
    setLoadingOlder(true);
    restore.current = { height: el.scrollHeight, top: el.scrollTop, head: messages?.[0]?.id ?? null };
    try {
      await loadOlderMessages(id);
    } catch {
      toast("Could not load older messages", "error");
    } finally {
      // An anchor the prepend did not spend (nothing older came) must not
      // be spent by a later structural change.
      restore.current = null;
      setLoadingOlder(false);
    }
  };

  // One handler for every code block's copy button.
  const onListClick = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const btn = (e.target as HTMLElement).closest<HTMLButtonElement>(".code-copy");
    if (!btn) return;
    const pre = btn.closest(".code-block")?.querySelector("pre");
    if (!pre) return;
    void navigator.clipboard.writeText(pre.innerText).then(
      () => {
        btn.textContent = "Copied";
        window.setTimeout(() => (btn.textContent = "Copy"), 1500);
      },
      () => toast("Copy is blocked here", "error"),
    );
  }, []);

  if (load === "missing" || error === "gone") {
    return (
      <div className="page">
        <TopBar title="Thread" backTo="/" />
        <div className="empty">
          <div className="glyph">
            <IconTrash />
          </div>
          <h2>This thread is gone</h2>
          <p>It was deleted, or the link is wrong.</p>
          <button type="button" className="btn" style={{ marginTop: 14 }} onClick={() => navigate("/", { replace: true })}>
            Back to the inbox
          </button>
        </div>
      </div>
    );
  }

  const title = thread?.title || thread?.agent || "Thread";
  const subtitle = thread?.title && thread.agent ? thread.agent : thread?.external_id ? <span style={{ fontFamily: "var(--mono)" }}>{thread.external_id}</span> : undefined;

  return (
    <div className="page thread-page">
      {settingsOpen && <ThreadSettingsPanel thread={id} onClose={() => navigate(`/t/${id}`)} />}
      <TopBar
        title={
          <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, maxWidth: "100%" }}>
            {thread && <Avatar name={thread.agent || thread.title || "?"} small />}
            <span style={{ minWidth: 0, flex: "0 1 auto", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
            {thread?.muted && <IconBellOff style={{ width: 14, height: 14, color: "var(--text-3)", flex: "none" }} />}
          </span>
        }
        subtitle={subtitle}
        backTo="/"
        right={
          <><button type="button" className="btn small thread-settings-trigger" onClick={() => navigate(`/t/${id}?panel=settings`)}>Settings</button><button type="button" className="icon-btn" aria-label="Thread options" onClick={() => setMenu(true)}>
            <IconMore />
          </button></>
        }
        below={thread ? <><MetaStrip thread={thread} /><ThreadArtifacts thread={id} onAvailable={setHasArtifacts} /></> : null}
      />
      <div ref={listRef} className="messages" onClick={onListClick}>
        {focusedMessage && !items.some(it => it.kind === "message" && it.m.id === focusedMessage.id) && <section aria-label="Selected archived message"><p>Selected message · {fullDateTime(focusedMessage.created_at)}</p><MessageBubble m={focusedMessage} grouped={false} showMeta={true} inspectThread={hasArtifacts ? thread : undefined} onOpen={(items, index) => setViewing({ items, index })} /><button className="btn small" onClick={() => navigate(`/t/${id}`)}>Return to recent conversation</button></section>}
        {hasOlder && (
          <button type="button" className="btn small" style={{ alignSelf: "center" }} disabled={loadingOlder} onClick={loadOlder}>
            {loadingOlder ? "Loading…" : "Load earlier messages"}
          </button>
        )}
        {!loaded && !error && (
          <>
            <div className="skeleton" style={{ height: 60, width: "70%" }} />
            <div className="skeleton" style={{ height: 40, width: "45%", alignSelf: "flex-end" }} />
            <div className="skeleton" style={{ height: 90, width: "80%" }} />
          </>
        )}
        {!loaded && error && (
          <div className="empty">
            <div className="glyph warn">
              <IconAlert />
            </div>
            <h2>Couldn't load this thread</h2>
            <p>{error}</p>
            <button type="button" className="btn primary" style={{ marginTop: 14 }} onClick={retry}>
              Try again
            </button>
          </div>
        )}
        {loaded && items.length === 0 && (
          <div className="empty">
            <h2>Nothing here yet</h2>
            <p>Messages from the agent will appear here, and you can reply below.</p>
          </div>
        )}
        {items.map((it, i) => {
          const prev = items[i - 1];
          const next = items[i + 1];
          const showDay = !prev || !sameDay(prev.at, it.at);
          const grouped = !showDay && groupedWith(prev, it);
          const showMeta = !groupedWith(it, next);
          return (
            <div key={itemKey(it)} style={{ display: "contents" }}>
              {showDay && <div className="day-divider">{dayLabel(it.at)}</div>}
              {i === newIndex && (
                <div ref={dividerRef} className="new-divider" aria-label="New messages">
                  New
                </div>
              )}
              {it.kind === "message" ? <MessageBubble m={it.m} grouped={grouped} showMeta={showMeta} inspectThread={hasArtifacts ? thread : undefined} onOpen={(items, index) => setViewing({ items, index })} /> : <QuestionCard q={it.q} highlight={highlightQuestion === it.q.id} />}
            </div>
          );
        })}
        {activity && <ActivityBubble a={activity} />}
        <div ref={bottomRef} />
      </div>
      {!atBottom && loaded && items.length > 0 && (
        <button
          type="button"
          className={`btn small jump-pill ${unseen > 0 ? "primary" : ""}`}
          onClick={() => {
            scrollToBottom("smooth");
            setUnseen(0);
          }}
          aria-label={unseen > 0 ? `${unseen} new messages, jump to latest` : "Jump to latest"}
        >
          <IconDown /> {unseen > 0 ? `${unseen} new` : "Latest"}
        </button>
      )}
      <Composer
        threadId={id}
        ended={sessionEnded(messages, questions, thread)}
        onSending={(sending) => {
          justSent.current = sending;
        }}
      />
      {viewing && <ImageViewer items={viewing.items} index={viewing.index} onClose={() => setViewing(null)} />}

      {menu && thread && (
        <Sheet onClose={() => setMenu(false)} label="Thread options">
          <button type="button" className="item" onClick={() => { setMenu(false); setRenaming(true); }}>
            <IconEdit /> Rename
          </button>
          <button
            type="button"
            className="item"
            onClick={() => {
              setMenu(false);
              updateThread(id, { muted: !thread.muted }).then(() => toast(thread.muted ? "Notifications on for this thread" : "Thread muted")).catch((e) => toast(e.message, "error"));
            }}
          >
            {thread.muted ? <IconBell /> : <IconBellOff />} {thread.muted ? "Unmute" : "Mute notifications"}
          </button>
          <button
            type="button"
            className="item"
            onClick={() => {
              setMenu(false);
              const wasArchived = !!thread.archived_at;
              updateThread(id, { archived: !wasArchived })
                .then(() => {
                  if (wasArchived) toast("Thread restored");
                  else {
                    navigate("/");
                    toast("Thread archived", "info", { label: "Undo", onClick: () => void updateThread(id, { archived: false }) });
                  }
                })
                .catch((e) => toast(e.message, "error"));
            }}
          >
            <IconArchive /> {thread.archived_at ? "Unarchive" : "Archive"}
          </button>
          <button
            type="button"
            className="item"
            onClick={async () => {
              setMenu(false);
              try {
                await navigator.clipboard.writeText(thread.id);
                toast("Thread id copied");
              } catch {
                toast(thread.id);
              }
            }}
          >
            <IconCopy /> Copy thread id
          </button>
          <button type="button" className="item danger" onClick={() => { setMenu(false); setConfirmDelete(true); }}>
            <IconTrash /> Delete thread
          </button>
        </Sheet>
      )}
      {confirmDelete && thread && (
        <ConfirmSheet
          title="Delete this thread?"
          body={`Everything in "${title}" is removed for good, including attachments. This cannot be undone.`}
          confirmLabel="Delete thread"
          danger
          onClose={() => setConfirmDelete(false)}
          onConfirm={() => {
            deleteThread(id)
              .then(() => {
                toast("Thread deleted");
                navigate("/", { replace: true });
              })
              .catch((e) => toast(e.message, "error"));
          }}
        />
      )}
      {renaming && thread && (
        <RenameSheet
          initial={thread.title}
          onClose={() => setRenaming(false)}
          onSave={(t) => {
            setRenaming(false);
            updateThread(id, { title: t }).catch((e) => toast(e.message, "error"));
          }}
        />
      )}
    </div>
  );
}

/** A session that has said goodbye: the composer says so instead of pretending.
 * The agent's own `session_end` marker decides, and any later agent message
 * or question lifts it; a reply from the user does not. Threads from before
 * the marker existed fall back to reading the preview. */
function sessionEnded(messages: Message[] | undefined, questions: Question[] | undefined, t: Thread | undefined): boolean {
  let marker: Message | null = null;
  for (const m of messages ?? []) {
    if (m.sender === "system" && (m.meta.kind === "session_end" || m.meta.kind === "session_start")) marker = m;
  }
  if (marker) {
    if (marker.meta.kind !== "session_end") return false;
    const since = marker.created_at;
    if ((messages ?? []).some((m) => m.sender !== "user" && m.created_at > since)) return false;
    if ((questions ?? []).some((q) => q.created_at > since)) return false;
    return true;
  }
  return !!t && t.preview_sender === "system" && /session (ended|finished|stopped|was interrupted)|finished this session/i.test(t.preview);
}

// Reserved meta keys agents may set; rendered as compact chips under the title.
const metaChips: { key: string; alt?: string; icon: (p: React.SVGProps<SVGSVGElement>) => React.ReactElement; format?: (v: unknown) => string }[] = [
  { key: "host", alt: "hostname", icon: IconServer },
  { key: "cwd", icon: IconFolder, format: (v) => String(v).replace(/^\/(?:home|Users)\/[^/]+/, "~") },
  { key: "branch", icon: IconBranch },
  { key: "model", icon: IconCpu, format: (v) => String(v).split("/").pop() ?? String(v) },
  { key: "cost_usd", icon: IconCoins, format: (v) => (typeof v === "number" ? (v >= 1 ? `$${v.toFixed(2)}` : v > 0 ? `$${v.toFixed(3)}` : "$0") : String(v)) },
  { key: "tokens", icon: IconCpu, format: (v) => (typeof v === "number" ? `${v >= 1e6 ? (v / 1e6).toFixed(1) + "M" : v >= 1000 ? (v / 1000).toFixed(0) + "k" : v} tok` : String(v)) },
];

function MetaStrip({ thread }: { thread: Thread }) {
  const chips = metaChips
    .map((c) => {
      const raw = thread.meta[c.key] ?? (c.alt ? thread.meta[c.alt] : undefined);
      if (raw === undefined || raw === null || raw === "") return null;
      const text = c.format ? c.format(raw) : String(raw);
      return { key: c.key, icon: c.icon, text, full: String(raw) };
    })
    .filter((c): c is NonNullable<typeof c> => !!c);
  if (chips.length === 0) return null;
  return (
    <div className="meta-strip">
      {chips.map((c) => {
        const Icon = c.icon;
        return (
          <span key={c.key} className="meta-chip" title={`${c.key}: ${c.full}`}>
            <Icon /> {c.text}
          </span>
        );
      })}
    </div>
  );
}

function RenameSheet({ initial, onClose, onSave }: { initial: string; onClose: () => void; onSave: (t: string) => void }) {
  const [value, setValue] = useState(initial);
  return (
    <Sheet onClose={onClose} label="Rename thread">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave(value.trim());
        }}
        style={{ padding: "4px 6px" }}
      >
        <div className="field">
          <label htmlFor="rename">Thread title</label>
          <input id="rename" autoFocus value={value} onChange={(e) => setValue(e.target.value)} maxLength={300} />
        </div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn primary">
            Save
          </button>
        </div>
      </form>
    </Sheet>
  );
}

/** Bodies taller than this collapse behind "Show all" so one log dump does not bury the thread. */
const collapseAt = 520;

const MessageBubble = memo(function MessageBubble({ m, grouped, showMeta, inspectThread, onOpen }: { m: Message; grouped: boolean; showMeta: boolean; inspectThread?: Thread; onOpen: (images: Attachment[], index: number) => void }) {
  const html = useMemo(() => (m.format === "markdown" ? renderMarkdown(m.body) : renderText(m.body)), [m.format, m.body]);
  const kind = typeof m.meta.kind === "string" ? m.meta.kind : "";
  const via = typeof m.meta.via === "string" ? m.meta.via : "";
  const attachments = m.attachments ?? [];
  const hasMedia = attachments.some((a) => a.kind === "image");
  const hasBody = m.body.trim().length > 0;
  const mirrored = m.sender === "user" && m.origin === "token";
  const bodyRef = useRef<HTMLDivElement>(null);
  const [tall, setTall] = useState(false);
  const [expanded, setExpanded] = useState(false);
  useLayoutEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    setTall(el.scrollHeight > collapseAt + 80);
  }, [html]);
  const collapsed = tall && !expanded;
  const collapse = () => {
    setExpanded(false);
    requestAnimationFrame(() => bodyRef.current?.closest(".msg")?.scrollIntoView({ block: "start" }));
  };
  return (
    <div id={`message-${m.id}`} className={`msg ${m.sender} ${m.importance === "important" ? "important" : ""} ${grouped ? "grouped" : ""} ${mirrored ? "mirrored" : ""}`}>
      {m.importance === "important" && <span className="important-chip">Important</span>}
      <div className={`bubble ${m.sender === "system" ? "" : "md"} ${hasMedia ? "has-media" : ""} ${collapsed ? "collapsed" : ""}`}>
        {hasBody && <div ref={bodyRef} className={`${m.sender === "system" ? "" : "md"} body`} dangerouslySetInnerHTML={{ __html: html }} />}
        {attachments.length > 0 && <AttachmentList items={attachments} onOpen={onOpen} />}
        {tall && !expanded && (
          <button type="button" className="show-all" onClick={() => setExpanded(true)}>
            Show all
          </button>
        )}
        {tall && expanded && (
          <button type="button" className="show-less" onClick={collapse}>
            Show less
          </button>
        )}
      </div>
      <div className="message-footer">
      {showMeta && (
        <div className="msg-meta" title={fullDateTime(m.created_at)}>
          {kind === "notification" && <span>needs attention</span>}
          {mirrored && <span>from the terminal</span>}
          {!mirrored && via && <span>via {via}</span>}
          <span>{shortTime(m.created_at)}</span>
        </div>
      )}
      <div className="message-actions">
        {hasBody && <CopyButton text={m.body} className="message-action" label="Copy message" />}
        {inspectThread && <InspectMessage message={m} thread={inspectThread} />}
      </div>
      </div>
    </div>
  );
});

/** The agent's live status: "running tests…" with a timer once it has taken a while. */
function ActivityBubble({ a }: { a: Activity }) {
  const elapsed = useElapsed(a.since);
  if (a.kind === "waiting") {
    return <div className="activity-waiting" role="status" aria-live="polite">{a.text === "Waiting for your next message" ? "Ready for your message" : a.text}</div>;
  }
  // Only the text is live: the timer ticks every second and would otherwise
  // be re-announced by a screen reader for as long as the agent works.
  return (
    <div className="msg agent activity-msg">
      <div className={`bubble activity ${a.kind}`}>
        <span className="act-dots" aria-hidden>
          <i />
          <i />
          <i />
        </span>
        <span className="act-text" role="status" aria-live="polite">
          {a.text}
        </span>
        {elapsed >= 15 && (
          <span className="act-time" title="How long the agent has been busy" aria-hidden="true">
            {formatElapsed(elapsed)}
          </span>
        )}
      </div>
    </div>
  );
}

function Composer({ threadId, ended, onSending }: { threadId: string; ended: boolean; onSending: (sending: boolean) => void }) {
  const attachmentsEnabled = useStore((s) => s.attachmentsEnabled);
  const connection = useStore((s) => s.connection);
  const draft = useStore((s) => s.drafts[threadId] ?? "");
  const [text, setText] = useState(draft);
  const [busy, setBusy] = useState(false);
  const [uploads, setUploads] = useState<PendingUpload[]>([]);
  const [dragging, setDragging] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const draftTimer = useRef<number | undefined>(undefined);
  // One key per composed payload: a retry after a lost response cannot
  // double-post, and an edited reply gets a fresh key so it is not mistaken
  // for a replay of the old text.
  const keyed = useRef<{ payload: string; key: string } | null>(null);
  const sendRef = useRef<() => void>(() => {});
  const touch = useMemo(() => isTouch(), []);

  // Toasts and the jump pill sit above the composer, whatever its height.
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const apply = () => document.documentElement.style.setProperty("--composer-h", `${el.offsetHeight}px`);
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(el);
    return () => {
      ro.disconnect();
      document.documentElement.style.removeProperty("--composer-h");
    };
  }, []);

  useEffect(() => {
    setText(draft);
    requestAnimationFrame(resize);
    // Only when switching threads; local edits flow the other way.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId]);

  useEffect(() => {
    return () => {
      for (const u of uploads) if (u.previewURL) URL.revokeObjectURL(u.previewURL);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const resize = () => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  };

  const update = (value: string) => {
    setText(value);
    resize();
    window.clearTimeout(draftTimer.current);
    draftTimer.current = window.setTimeout(() => setDraft(threadId, value), 300);
  };

  const addFiles = (files: FileList | File[]) => {
    const list = Array.from(files).filter((f) => f.size > 0);
    if (list.length === 0) return;
    if (!attachmentsEnabled) {
      toast("Attachments are not enabled on this server.", "error");
      return;
    }
    const room = 8 - uploads.length;
    if (list.length > room) toast(`You can attach up to 8 files per message.`, "error");
    for (const file of list.slice(0, Math.max(0, room))) {
      if (file.size > 10 * 1024 * 1024) {
        toast(`${file.name} is larger than 10 MB.`, "error");
        continue;
      }
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const previewURL = file.type.startsWith("image/") ? URL.createObjectURL(file) : null;
      setUploads((cur) => [...cur, { key, file, previewURL, progress: 0, attachment: null, error: null }]);
      api
        .uploadAttachment(threadId, file, (fraction) => setUploads((cur) => cur.map((u) => (u.key === key ? { ...u, progress: fraction } : u))))
        .then((attachment) => setUploads((cur) => cur.map((u) => (u.key === key ? { ...u, attachment, progress: 1 } : u))))
        .catch((e) => {
          const message = e instanceof Error ? e.message : "Upload failed";
          setUploads((cur) => cur.map((u) => (u.key === key ? { ...u, error: message } : u)));
          toast(message, "error");
        });
    }
  };

  const removeUpload = (key: string) => {
    setUploads((cur) => {
      const gone = cur.find((u) => u.key === key);
      if (gone?.previewURL) URL.revokeObjectURL(gone.previewURL);
      return cur.filter((u) => u.key !== key);
    });
  };

  const uploading = uploads.some((u) => !u.attachment && !u.error);
  const ready = uploads.filter((u) => u.attachment);
  const offline = connection === "offline";
  const canSend = !busy && !uploading && (text.trim().length > 0 || ready.length > 0);

  const send = async () => {
    const body = text.trim();
    if (!canSend) return;
    const ids = ready.map((u) => (u.attachment as Attachment).id);
    const payload = JSON.stringify([body, ids]);
    if (!keyed.current || keyed.current.payload !== payload) keyed.current = { payload, key: newKey() };
    setBusy(true);
    onSending(true);
    try {
      await sendMessage(threadId, body, ids, keyed.current.key);
      keyed.current = null;
      setText("");
      window.clearTimeout(draftTimer.current);
      setDraft(threadId, "");
      for (const u of uploads) if (u.previewURL) URL.revokeObjectURL(u.previewURL);
      setUploads([]);
      requestAnimationFrame(resize);
    } catch (e) {
      onSending(false);
      if (e instanceof AlreadyPostedError) {
        // The earlier attempt did land; what is typed now is a new message.
        keyed.current = null;
        toast(e.message, "error");
      } else {
        toast(e instanceof Error ? e.message : "Could not send", "error", { label: "Retry", onClick: () => sendRef.current() });
      }
    } finally {
      setBusy(false);
      ref.current?.focus();
    }
  };
  sendRef.current = () => void send();

  return (
    <div
      ref={wrapRef}
      className={`composer-wrap ${dragging ? "dragging" : ""}`}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        addFiles(e.dataTransfer.files);
      }}
    >
      <UploadTray items={uploads} onRemove={removeUpload} />
      {ended && <div className="composer-note">This session has ended. Your reply is kept here for the next one.</div>}
      <form
        className="composer"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        {attachmentsEnabled && (
          <>
            <input
              ref={fileRef}
              type="file"
              multiple
              hidden
              accept="image/*,.pdf,.txt,.log,.md,.json,.csv,.zip"
              onChange={(e) => {
                if (e.target.files) addFiles(e.target.files);
                e.target.value = "";
              }}
            />
            <button type="button" className="attach-btn" aria-label="Attach a file" onClick={() => fileRef.current?.click()}>
              <IconAttach />
            </button>
          </>
        )}
        <textarea
          ref={ref}
          rows={1}
          placeholder={offline ? "Offline. Your reply is kept until you're back." : "Reply to the agent…"}
          value={text}
          enterKeyHint={touch ? "enter" : "send"}
          onChange={(e) => update(e.target.value)}
          onPaste={(e) => {
            const files = Array.from(e.clipboardData.files ?? []);
            if (files.length > 0) {
              e.preventDefault();
              addFiles(files);
            }
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && (e.metaKey || e.ctrlKey || !touch)) {
              e.preventDefault();
              void send();
            }
          }}
        />
        <button type="submit" className="send-btn" aria-label="Send" disabled={!canSend}>
          {uploading ? <span className="spinner" style={{ borderTopColor: "#fff", borderColor: "rgba(255,255,255,0.35)" }} /> : <IconSend />}
        </button>
      </form>
    </div>
  );
}

function newKey(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function isTouch(): boolean {
  return window.matchMedia("(pointer: coarse)").matches;
}
