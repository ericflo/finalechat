import { useEffect, useMemo, useState } from "react";
import { Avatar, Link } from "../components/Common";
import { IconBell, IconCheck, IconInbox, IconPhone, IconSearch, IconSettings, IconTerminal } from "../components/Icons";
import { QuestionCard } from "../components/QuestionCard";
import { TopBar } from "../components/TopBar";
import { getPushState, isIOS, isStandalone, subscribeToPush, type PushState } from "../lib/push";
import { navigate } from "../lib/router";
import { loadArchived, loadMoreThreads, toast, updateSettings, useStore } from "../lib/store";
import { relativeTime } from "../lib/time";
import type { Thread } from "../lib/types";
import { api } from "../lib/api";
import { useLiveActivity } from "../lib/activity";

export function Inbox({ filter }: { filter: string | null }) {
  const threads = useStore((s) => s.threads);
  const pending = useStore((s) => s.pending);
  const inboxLoaded = useStore((s) => s.inboxLoaded);
  const archivedLoaded = useStore((s) => s.archivedLoaded);
  const cursor = useStore((s) => s.inboxCursor);
  const connection = useStore((s) => s.connection);
  const settings = useStore((s) => s.settings);
  const [tab, setTab] = useState<"active" | "archived">("active");
  const [query, setQuery] = useState("");

  useEffect(() => {
    if (tab === "archived" && !archivedLoaded) void loadArchived();
  }, [tab, archivedLoaded]);

  const list = useMemo(() => {
    const q = query.trim().toLowerCase();
    return Object.values(threads)
      .filter((t) => (tab === "archived" ? !!t.archived_at : !t.archived_at))
      .filter((t) => !q || t.title.toLowerCase().includes(q) || t.agent.toLowerCase().includes(q) || t.preview.toLowerCase().includes(q))
      .sort((a, b) => (a.last_activity_at < b.last_activity_at ? 1 : -1));
  }, [threads, tab, query]);

  const needsYou = filter === "needs-you";

  return (
    <div className="page">
      <TopBar
        big
        title="Finalechat"
        right={
          <>
            <button
              type="button"
              className={`remote-chip ${settings.remote_mode ? "on" : ""}`}
              title="Remote mode: agents wait for your replies from here instead of the terminal"
              onClick={() =>
                updateSettings({ remote_mode: !settings.remote_mode }).catch((e) => toast(e instanceof Error ? e.message : "Could not update", "error"))
              }
            >
              <span className="dot" />
              {settings.remote_mode ? "Remote" : "At desk"}
            </button>
            <Link href="/settings" className="icon-btn" aria-label="Settings">
              <IconSettings />
            </Link>
          </>
        }
      />
      {connection === "offline" && <div className="status-strip">Reconnecting…</div>}
      <div className="page-body">
        <SetupChecklist hasThreads={Object.keys(threads).length > 0} />

        {pending.length > 0 && (
          <>
            <div className="section-title">
              Needs you <span className="count">{pending.length}</span>
            </div>
            <div className="thread-list">
              {pending.map((q) => (
                <QuestionCard key={q.id} q={q} showThread />
              ))}
            </div>
          </>
        )}

        {!needsYou && (
          <>
            <div className="section-title" style={{ justifyContent: "space-between" }}>
              <span>Threads</span>
              <div className="segmented" style={{ textTransform: "none", letterSpacing: 0 }}>
                <button type="button" className={tab === "active" ? "active" : ""} onClick={() => setTab("active")}>
                  Active
                </button>
                <button type="button" className={tab === "archived" ? "active" : ""} onClick={() => setTab("archived")}>
                  Archived
                </button>
              </div>
            </div>
            {(list.length > 6 || query) && (
              <label className="search">
                <IconSearch />
                <input type="search" placeholder="Search threads" value={query} onChange={(e) => setQuery(e.target.value)} />
              </label>
            )}
            {!inboxLoaded ? (
              <div className="thread-list">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="skeleton" style={{ height: 84 }} />
                ))}
              </div>
            ) : list.length === 0 ? (
              <EmptyThreads archived={tab === "archived"} searching={!!query} />
            ) : (
              <div className="thread-list">
                {list.map((t) => (
                  <ThreadRow key={t.id} t={t} />
                ))}
                {tab === "active" && cursor && !query && (
                  <button type="button" className="btn block" onClick={() => loadMoreThreads().catch(() => toast("Could not load more", "error"))}>
                    Load older threads
                  </button>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function ThreadRow({ t }: { t: Thread }) {
  const unread = t.unread_count > 0;
  const needs = t.pending_questions > 0;
  const name = t.agent || t.title || "Agent";
  const activity = useLiveActivity(t.activity);
  return (
    <Link href={`/t/${t.id}`} className={`thread-row ${unread ? "unread" : ""} ${needs ? "needs-you" : ""} ${activity ? "live" : ""}`}>
      <Avatar name={name} />
      <div className="thread-main">
        <div className="thread-title">{t.title || t.agent || "Untitled thread"}</div>
        {t.title && t.agent && <div className="thread-agent">{t.agent}</div>}
        <div className="thread-preview">
          {activity ? (
            <span className={`live-line ${activity.kind}`}>
              <span className="live-dot" aria-hidden />
              {activity.text}
            </span>
          ) : (
            <>
              {t.preview_sender === "user" && <span style={{ color: "var(--text-3)" }}>You: </span>}
              {t.preview_sender === "question" && <span style={{ color: "var(--amber)", fontWeight: 600 }}>Asked: </span>}
              {t.preview || <em style={{ color: "var(--text-3)" }}>No messages yet</em>}
            </>
          )}
        </div>
      </div>
      <div className="thread-side">
        <span>{relativeTime(t.last_activity_at)}</span>
        {needs ? <span className="pill amber">{t.pending_questions === 1 ? "Needs you" : `${t.pending_questions} questions`}</span> : unread ? <span className="dot" aria-label={`${t.unread_count} unread`} /> : t.muted ? <span className="pill muted">Muted</span> : null}
      </div>
    </Link>
  );
}

function EmptyThreads({ archived, searching }: { archived: boolean; searching: boolean }) {
  return (
    <div className="empty">
      <div className="glyph">
        <IconInbox />
      </div>
      <h2>{searching ? "No matches" : archived ? "Nothing archived" : "No threads yet"}</h2>
      <p>
        {searching
          ? "Try a different search."
          : archived
            ? "Archived threads will show up here."
            : "When an agent posts its first message, a thread appears here and your phone can buzz."}
      </p>
    </div>
  );
}

/** A small, dismissable checklist that gets a new install to a working state. */
function SetupChecklist({ hasThreads }: { hasThreads: boolean }) {
  const pushEnabled = useStore((s) => s.pushEnabled);
  const [push, setPush] = useState<PushState | null>(null);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem("fc.setup.dismissed") === "1";
    } catch {
      return false;
    }
  });
  const [tokens, setTokens] = useState<number | null>(null);
  const standalone = isStandalone();

  useEffect(() => {
    void getPushState().then(setPush);
    api
      .listTokens()
      .then((r) => setTokens(r.tokens.length))
      .catch(() => setTokens(0));
  }, []);

  const pushDone = push === "subscribed";
  const tokenDone = tokens !== null && tokens > 0;
  const allDone = standalone && pushDone && tokenDone && hasThreads;

  useEffect(() => {
    if (allDone && !dismissed) {
      try {
        localStorage.setItem("fc.setup.dismissed", "1");
      } catch {
        // ignore
      }
      setDismissed(true);
    }
  }, [allDone, dismissed]);

  if (dismissed || push === null || tokens === null) return null;

  const enablePush = async () => {
    try {
      const st = await subscribeToPush();
      setPush(st);
      if (st === "subscribed") toast("Notifications on", "success");
      else if (st === "denied") toast("Notifications are blocked for this site in your browser settings.", "error");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not enable notifications", "error");
    }
  };

  return (
    <div className="card checklist" style={{ marginBottom: 6 }}>
      <div style={{ display: "flex", alignItems: "center", padding: "8px 10px 4px" }}>
        <h2 style={{ fontSize: 16, flex: 1 }}>Get set up</h2>
        <button type="button" className="btn ghost small" onClick={() => {
          setDismissed(true);
          try { localStorage.setItem("fc.setup.dismissed", "1"); } catch { /* ignore */ }
        }}>
          Hide
        </button>
      </div>
      <CheckItem
        done={standalone}
        icon={<IconPhone />}
        label="Install on your phone"
        hint={standalone ? "Running as an app." : isIOS() ? "In Safari: Share → Add to Home Screen. Notifications need the installed app." : "Use your browser's Install / Add to Home Screen option."}
      />
      <CheckItem
        done={pushDone}
        icon={<IconBell />}
        label="Turn on notifications"
        hint={
          !pushEnabled
            ? "This server has no push keys configured."
            : push === "unsupported"
              ? isIOS() && !standalone
                ? "Install to the Home Screen first, then enable."
                : "This browser does not support Web Push."
              : push === "denied"
                ? "Blocked. Allow notifications for this site in your browser settings."
                : "Questions and important messages will buzz your phone."
        }
        action={
          !pushDone && pushEnabled && push !== "unsupported" && push !== "denied" ? (
            <button type="button" className="btn primary small" onClick={enablePush}>
              Enable
            </button>
          ) : undefined
        }
      />
      <CheckItem
        done={tokenDone}
        icon={<IconTerminal />}
        label="Connect an agent"
        hint="Create a token and paste one line into your terminal."
        action={
          !tokenDone ? (
            <button type="button" className="btn primary small" onClick={() => navigate("/settings/agents")}>
              Connect
            </button>
          ) : undefined
        }
      />
    </div>
  );
}

function CheckItem({ done, icon, label, hint, action }: { done: boolean; icon: React.ReactNode; label: string; hint: string; action?: React.ReactNode }) {
  return (
    <div className={`check-item ${done ? "done" : ""}`}>
      <div className="mark">{done ? <IconCheck /> : null}</div>
      <div>
        <div className="label" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ display: "inline-flex", width: 18, height: 18, color: "var(--text-3)" }}>{icon}</span>
          {label}
        </div>
        <div className="hint">{hint}</div>
      </div>
      <div>{action}</div>
    </div>
  );
}
