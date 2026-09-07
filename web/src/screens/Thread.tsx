import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Avatar, Sheet } from "../components/Common";
import { IconArchive, IconBell, IconBellOff, IconCopy, IconDown, IconEdit, IconMore, IconSend, IconTrash } from "../components/Icons";
import { QuestionCard } from "../components/QuestionCard";
import { TopBar } from "../components/TopBar";
import { renderMarkdown, renderText } from "../lib/markdown";
import { navigate } from "../lib/router";
import { deleteThread, loadOlderMessages, loadThread, markRead, sendMessage, setCurrentThread, toast, updateThread, useStore } from "../lib/store";
import { dayLabel, fullDateTime, sameDay, shortTime } from "../lib/time";
import type { Message, Question } from "../lib/types";

type Item = { kind: "message"; at: string; m: Message } | { kind: "question"; at: string; q: Question };

export function ThreadScreen({ id, highlightQuestion }: { id: string; highlightQuestion: string | null }) {
  const thread = useStore((s) => s.threads[id]);
  const messages = useStore((s) => s.messages[id]);
  const questions = useStore((s) => s.threadQuestions[id]);
  const loaded = useStore((s) => s.threadLoaded[id]);
  const hasOlder = useStore((s) => s.hasOlder[id]);
  const [missing, setMissing] = useState(false);
  const [menu, setMenu] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [atBottom, setAtBottom] = useState(true);
  const [unseen, setUnseen] = useState(0);
  const lastCount = useRef(0);

  useEffect(() => {
    setCurrentThread(id);
    void loadThread(id).then((ok) => !ok && setMissing(true));
    return () => setCurrentThread(null);
  }, [id]);

  // Mark as read when viewing and new agent messages arrive while visible.
  useEffect(() => {
    if (!thread || !loaded) return;
    if (thread.unread_count > 0 && document.visibilityState === "visible") void markRead(id);
  }, [id, thread, loaded]);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    // The answer to a question is shown on its card; the transcript copy the
    // server keeps for agents would only repeat it here.
    for (const m of messages ?? []) if (m.meta.kind !== "answer") out.push({ kind: "message", at: m.created_at, m });
    for (const q of questions ?? []) out.push({ kind: "question", at: q.created_at, q });
    out.sort((a, b) => (a.at < b.at ? -1 : a.at > b.at ? 1 : 0));
    return out;
  }, [messages, questions]);

  // Scroll management: stick to the bottom unless the user scrolled up.
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const grew = items.length > lastCount.current;
    lastCount.current = items.length;
    if (!loaded) return;
    if (atBottom || !grew) {
      if (grew || items.length > 0) bottomRef.current?.scrollIntoView({ block: "end" });
    } else if (grew) {
      setUnseen((n) => n + 1);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items.length, loaded]);

  useEffect(() => {
    if (loaded && !highlightQuestion) {
      // Initial paint: jump to the end without animation.
      requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ block: "end" }));
    }
  }, [loaded, highlightQuestion]);

  const onScroll = useCallback(() => {
    const el = document.scrollingElement;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    const nearBottom = distance < 80;
    setAtBottom(nearBottom);
    if (nearBottom) setUnseen(0);
  }, []);

  useEffect(() => {
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, [onScroll]);

  if (missing) {
    return (
      <div className="page">
        <TopBar title="Thread" backTo="/" />
        <div className="empty">
          <h2>This thread is gone</h2>
          <p>It may have been deleted.</p>
        </div>
      </div>
    );
  }

  const title = thread?.title || thread?.agent || "Thread";
  const subtitle = thread?.title && thread.agent ? thread.agent : thread?.external_id ? <span style={{ fontFamily: "var(--mono)" }}>{thread.external_id}</span> : undefined;

  return (
    <div className="page thread-page">
      <TopBar
        title={
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
            {thread && <Avatar name={thread.agent || thread.title || "?"} small />}
            <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
            {thread?.muted && <IconBellOff style={{ width: 14, height: 14, color: "var(--text-3)" }} />}
          </span>
        }
        subtitle={subtitle}
        backTo="/"
        right={
          <button type="button" className="icon-btn" aria-label="Thread options" onClick={() => setMenu(true)}>
            <IconMore />
          </button>
        }
      />
      <div ref={scrollRef} className="messages">
        {hasOlder && (
          <button type="button" className="btn small" style={{ alignSelf: "center" }} onClick={() => loadOlderMessages(id).catch(() => toast("Could not load older messages", "error"))}>
            Load earlier messages
          </button>
        )}
        {!loaded && (
          <>
            <div className="skeleton" style={{ height: 60, width: "70%" }} />
            <div className="skeleton" style={{ height: 40, width: "45%", alignSelf: "flex-end" }} />
            <div className="skeleton" style={{ height: 90, width: "80%" }} />
          </>
        )}
        {loaded && items.length === 0 && (
          <div className="empty">
            <h2>Nothing here yet</h2>
            <p>Messages from the agent will appear here, and you can reply below.</p>
          </div>
        )}
        {items.map((it, i) => {
          const prev = items[i - 1];
          const showDay = !prev || !sameDay(prev.at, it.at);
          const grouped =
            !showDay && prev && prev.kind === "message" && it.kind === "message" && prev.m.sender === it.m.sender && new Date(it.at).getTime() - new Date(prev.at).getTime() < 3 * 60 * 1000;
          return (
            <div key={it.kind === "message" ? it.m.id : it.q.id} style={{ display: "contents" }}>
              {showDay && <div className="day-divider">{dayLabel(it.at)}</div>}
              {it.kind === "message" ? <MessageBubble m={it.m} grouped={!!grouped} /> : <QuestionCard q={it.q} highlight={highlightQuestion === it.q.id} />}
            </div>
          );
        })}
        <div ref={bottomRef} />
      </div>
      {unseen > 0 && !atBottom && (
        <button
          type="button"
          className="btn primary small jump-pill"
          onClick={() => {
            bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
            setUnseen(0);
          }}
        >
          <IconDown /> {unseen} new
        </button>
      )}
      <Composer threadId={id} />

      {menu && thread && (
        <Sheet onClose={() => setMenu(false)}>
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
              updateThread(id, { archived: !thread.archived_at })
                .then(() => {
                  toast(thread.archived_at ? "Thread restored" : "Thread archived");
                  if (!thread.archived_at) navigate("/");
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
          <button
            type="button"
            className="item danger"
            onClick={() => {
              setMenu(false);
              if (!window.confirm("Delete this thread and everything in it?")) return;
              deleteThread(id)
                .then(() => {
                  toast("Thread deleted");
                  navigate("/", { replace: true });
                })
                .catch((e) => toast(e.message, "error"));
            }}
          >
            <IconTrash /> Delete thread
          </button>
        </Sheet>
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

function RenameSheet({ initial, onClose, onSave }: { initial: string; onClose: () => void; onSave: (t: string) => void }) {
  const [value, setValue] = useState(initial);
  return (
    <Sheet onClose={onClose}>
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

function MessageBubble({ m, grouped }: { m: Message; grouped: boolean }) {
  const html = m.format === "markdown" ? renderMarkdown(m.body) : renderText(m.body);
  const kind = typeof m.meta.kind === "string" ? m.meta.kind : "";
  const via = typeof m.meta.via === "string" ? m.meta.via : "";
  return (
    <div className={`msg ${m.sender} ${m.importance === "important" ? "important" : ""} ${grouped ? "grouped" : ""}`}>
      <div className={`bubble ${m.sender === "system" ? "" : "md"}`} dangerouslySetInnerHTML={{ __html: html }} />
      <div className="msg-meta" title={fullDateTime(m.created_at)}>
        {m.importance === "important" && <span className="important-tag">Important</span>}
        {kind === "answer" && <span>answer</span>}
        {kind === "notification" && <span>needs attention</span>}
        {via && <span>via {via}</span>}
        <span>{shortTime(m.created_at)}</span>
      </div>
    </div>
  );
}

function Composer({ threadId }: { threadId: string }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);

  const resize = () => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  };

  const send = async () => {
    const body = text.trim();
    if (!body || busy) return;
    setBusy(true);
    try {
      await sendMessage(threadId, body);
      setText("");
      requestAnimationFrame(resize);
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not send", "error");
    } finally {
      setBusy(false);
      ref.current?.focus();
    }
  };

  return (
    <div className="composer-wrap">
      <form
        className="composer"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <textarea
          ref={ref}
          rows={1}
          placeholder="Reply to the agent…"
          value={text}
          enterKeyHint="send"
          onChange={(e) => {
            setText(e.target.value);
            resize();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && (e.metaKey || e.ctrlKey || !isTouch())) {
              e.preventDefault();
              void send();
            }
          }}
        />
        <button type="submit" className="send-btn" aria-label="Send" disabled={!text.trim() || busy}>
          <IconSend />
        </button>
      </form>
    </div>
  );
}

function isTouch(): boolean {
  return window.matchMedia("(pointer: coarse)").matches;
}
