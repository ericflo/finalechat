import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Avatar, ConfirmSheet, Link, Sheet } from "../components/Common";
import {
  IconArchive,
  IconBell,
  IconBellOff,
  IconCheck,
  IconClose,
  IconInbox,
  IconPhone,
  IconPlus,
  IconSearch,
  IconSettings,
  IconTerminal,
  IconTrash,
} from "../components/Icons";
import { ProjectPickerSheet, useSessionStarters } from "../components/NewSession";
import { QuestionCard } from "../components/QuestionCard";
import { TopBar } from "../components/TopBar";
import {
  getPushState,
  isIOS,
  isStandalone,
  subscribeToPush,
  type PushState,
} from "../lib/push";
import { navigate } from "../lib/router";
import {
  clearSearch,
  deleteThread,
  loadArchived,
  loadMoreThreads,
  searchThreads,
  toast,
  updateSettings,
  updateThread,
  useStore,
} from "../lib/store";
import { compactTime, fullDateTime, useNow } from "../lib/time";
import type { Thread } from "../lib/types";
import { api } from "../lib/api";
import { useLiveActivity } from "../lib/activity";

export function Inbox({ filter }: { filter: string | null }) {
  const threads = useStore((s) => s.threads);
  const pending = useStore((s) => s.pending);
  const counts = useStore((s) => s.counts);
  const inboxLoaded = useStore((s) => s.inboxLoaded);
  const fromSnapshot = useStore((s) => s.fromSnapshot);
  const archivedLoaded = useStore((s) => s.archivedLoaded);
  const cursor = useStore((s) => s.inboxCursor);
  const settings = useStore((s) => s.settings);
  const searchHits = useStore((s) => s.searchHits);
  const [tab, setTab] = useState<"active" | "archived">("active");
  const [query, setQuery] = useState("");
  const [showAllPending, setShowAllPending] = useState(false);
  const [rowMenu, setRowMenu] = useState<Thread | null>(null);
  const [newSession, setNewSession] = useState(false);
  const { starters } = useSessionStarters();
  const searchTimer = useRef<number | undefined>(undefined);
  const needsYou = filter === "needs-you";

  useEffect(() => {
    if (tab === "archived" && !archivedLoaded) void loadArchived();
  }, [tab, archivedLoaded]);

  // Local matches appear instantly; the server adds threads not loaded yet.
  useEffect(() => {
    window.clearTimeout(searchTimer.current);
    const q = query.trim();
    if (!q) {
      clearSearch();
      return;
    }
    searchTimer.current = window.setTimeout(() => void searchThreads(q), 250);
    return () => window.clearTimeout(searchTimer.current);
  }, [query]);

  const searching = query.trim().length > 0;
  const list = useMemo(() => {
    const q = query.trim().toLowerCase();
    const hits =
      searchHits && searchHits.query === query.trim()
        ? new Set(searchHits.ids)
        : null;
    return Object.values(threads)
      .filter((t) =>
        q ? true : tab === "archived" ? !!t.archived_at : !t.archived_at,
      )
      .filter((t) => !needsYou || t.pending_questions > 0)
      .filter(
        (t) =>
          !q ||
          hits?.has(t.id) ||
          t.title.toLowerCase().includes(q) ||
          t.agent.toLowerCase().includes(q) ||
          t.preview.toLowerCase().includes(q) ||
          (t.external_id ?? "").toLowerCase().includes(q),
      )
      .sort((a, b) => {
        // Threads with a question waiting come first; then by activity.
        const na = a.pending_questions > 0 ? 1 : 0;
        const nb = b.pending_questions > 0 ? 1 : 0;
        if (na !== nb) return nb - na;
        return a.last_activity_at < b.last_activity_at ? 1 : -1;
      });
  }, [threads, tab, query, searchHits, needsYou]);

  const now = useNow(15000);
  const summary = useMemo(() => {
    const parts: string[] = [];
    // The same source as the "Needs you" section below, so the two agree.
    if (pending.length > 0)
      parts.push(
        `${pending.length} need${pending.length === 1 ? "s" : ""} you`,
      );
    if (counts.unread_threads > 0)
      parts.push(`${counts.unread_threads} unread`);
    // Working means working: an agent waiting on the user is not counted.
    const live = Object.values(threads).filter(
      (t) =>
        !t.archived_at &&
        t.activity &&
        t.activity.kind !== "waiting" &&
        new Date(t.activity.expires_at).getTime() > now,
    ).length;
    if (live > 0) parts.push(`${live} working`);
    return parts.join(" · ");
  }, [counts, threads, pending, now]);

  const toggleRemote = () =>
    updateSettings({ remote_mode: !settings.remote_mode })
      .then(() =>
        toast(
          settings.remote_mode
            ? "Remote mode off · agents use the terminal again"
            : "Remote mode on · Claude Code waits for your replies here",
          "success",
        ),
      )
      .catch((e) =>
        toast(e instanceof Error ? e.message : "Could not update", "error"),
      );

  const visiblePending = showAllPending ? pending : pending.slice(0, 2);

  return (
    <div className="page">
      <TopBar
        big
        title="Finalechat"
        subtitle={
          summary &&
          !(inboxLoaded && tab === "active" && list.length === 0 && !searching)
            ? summary
            : undefined
        }
        right={
          <>
            <button
              type="button"
              className={`remote-chip ${settings.remote_mode ? "on" : ""}`}
              aria-pressed={settings.remote_mode}
              title={
                settings.remote_mode
                  ? "Remote mode is on: agents wait for your replies from here"
                  : "Remote mode is off: agents use the terminal"
              }
              onClick={toggleRemote}
            >
              <span className="dot" />
              {settings.remote_mode ? "Remote on" : "Remote off"}
            </button>
            {starters.length > 0 && (
              <button type="button" className="icon-btn" aria-label="New session" title="Start a new agent session" onClick={() => (starters.length === 1 && starters[0] ? navigate(`/new/${starters[0].resource.id}`) : setNewSession(true))}>
                <IconPlus />
              </button>
            )}
            <Link href="/settings" className="icon-btn" aria-label="Settings">
              <IconSettings />
            </Link>
          </>
        }
      />
      {newSession && <ProjectPickerSheet starters={starters} onClose={() => setNewSession(false)} />}
      <div className="page-body">
        <SetupChecklist hasThreads={Object.keys(threads).length > 0} />

        {needsYou && (
          <div className="filter-row">
            <span className="filter-chip">
              Needs you · {list.length}
              <button
                type="button"
                aria-label="Show everything"
                onClick={() => navigate("/", { replace: true })}
              >
                <IconClose />
              </button>
            </span>
          </div>
        )}

        {pending.length > 0 ? (
          <>
            <div className="section-title">
              Needs you <span className="count">{pending.length}</span>
            </div>
            <div className="thread-list cards">
              {visiblePending.map((q) => (
                <QuestionCard key={q.id} q={q} showThread />
              ))}
              {pending.length > 2 && (
                <button
                  type="button"
                  className="btn block"
                  onClick={() => setShowAllPending((v) => !v)}
                >
                  {showAllPending
                    ? "Show fewer"
                    : `${pending.length - 2} more question${pending.length - 2 === 1 ? "" : "s"}`}
                </button>
              )}
            </div>
          </>
        ) : (
          needsYou && (
            <div className="empty compact">
              <div className="glyph ok">
                <IconCheck />
              </div>
              <h2>All caught up</h2>
              <p>No agent is waiting on you right now.</p>
            </div>
          )
        )}

        {/* Under the Needs-you shortcut the caught-up card is the whole empty state. */}
        {needsYou && list.length === 0 ? null : (
          <>
            <div
              className="section-title"
              style={{ justifyContent: "space-between" }}
            >
              <span>{needsYou ? "Threads that need you" : "Threads"}</span>
              {!needsYou && (
                <div
                  className="segmented"
                  style={{ textTransform: "none", letterSpacing: 0 }}
                >
                  <button
                    type="button"
                    className={tab === "active" ? "active" : ""}
                    onClick={() => setTab("active")}
                  >
                    Active
                  </button>
                  <button
                    type="button"
                    className={tab === "archived" ? "active" : ""}
                    onClick={() => setTab("archived")}
                  >
                    Archived
                  </button>
                </div>
              )}
            </div>
            {(list.length > 5 || searching) && (
              <label className="search">
                <IconSearch aria-hidden />
                <input
                  type="search"
                  aria-label="Search threads"
                  placeholder="Search threads"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
              </label>
            )}
            {!inboxLoaded ? (
              <div className="thread-list">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="skeleton" style={{ height: 84 }} />
                ))}
              </div>
            ) : list.length === 0 ? (
              <EmptyThreads
                archived={tab === "archived"}
                searching={searching}
              />
            ) : (
              <div
                className={`thread-list grouped ${fromSnapshot ? "stale" : ""}`}
              >
                {list.map((t) => (
                  <ThreadRow
                    key={t.id}
                    t={t}
                    showArchived={searching}
                    onMenu={() => setRowMenu(t)}
                  />
                ))}
              </div>
            )}
            {tab === "active" &&
              cursor &&
              !searching &&
              inboxLoaded &&
              list.length > 0 && (
                <button
                  type="button"
                  className="btn block"
                  style={{ marginTop: 10 }}
                  onClick={() =>
                    loadMoreThreads().catch(() =>
                      toast("Could not load more", "error"),
                    )
                  }
                >
                  Load older threads
                </button>
              )}
          </>
        )}
      </div>
      {rowMenu && (
        <RowSheet
          t={threads[rowMenu.id] ?? rowMenu}
          onClose={() => setRowMenu(null)}
        />
      )}
    </div>
  );
}

function ThreadRow({
  t,
  showArchived,
  onMenu,
}: {
  t: Thread;
  showArchived: boolean;
  onMenu: () => void;
}) {
  const unread = t.unread_count > 0;
  const needs = t.pending_questions > 0;
  const name = t.agent || t.title || "Agent";
  const liveActivity = useLiveActivity(t.activity);
  // Idle sessions keep their last message preview and never look like typing.
  const activity = liveActivity?.kind === "waiting" ? null : liveActivity;
  const draft = useStore((s) => s.drafts[t.id]);
  const now = useNow(30000);
  const pressTimer = useRef<number | undefined>(undefined);
  const suppressClick = useRef(false);

  // Long-press (or right-click) opens the row's actions. The click that the
  // finger's lift produces is swallowed once; any later pointer resets that.
  useEffect(() => {
    const reset = () => {
      suppressClick.current = false;
    };
    window.addEventListener("pointerdown", reset, true);
    return () => window.removeEventListener("pointerdown", reset, true);
  }, []);
  const startPress = useCallback(() => {
    suppressClick.current = false;
    window.clearTimeout(pressTimer.current);
    pressTimer.current = window.setTimeout(() => {
      suppressClick.current = true;
      if (navigator.vibrate) navigator.vibrate(8);
      onMenu();
    }, 480);
  }, [onMenu]);
  const endPress = useCallback(
    () => window.clearTimeout(pressTimer.current),
    [],
  );

  return (
    <Link
      href={`/t/${t.id}`}
      className={`thread-row ${unread ? "unread" : ""} ${needs ? "needs-you" : ""} ${activity ? "live" : ""}`}
      onPointerDown={startPress}
      onPointerUp={endPress}
      onPointerLeave={endPress}
      onPointerCancel={endPress}
      onContextMenu={(e: React.MouseEvent) => {
        e.preventDefault();
        onMenu();
      }}
      onClickCapture={(e: React.MouseEvent) => {
        if (suppressClick.current) {
          e.preventDefault();
          e.stopPropagation();
          suppressClick.current = false;
        }
      }}
    >
      <Avatar name={name} />
      <div className="thread-main">
        <div className="thread-head">
          <span className="thread-title">
            {t.title || t.agent || "Untitled thread"}
          </span>
          {t.title && t.agent && (
            <span className="thread-agent">{t.agent}</span>
          )}
        </div>
        <div className="thread-preview">
          {activity ? (
            <span className={`live-line ${activity.kind}`}>
              <span className="live-dot" aria-hidden />
              {activity.text}
            </span>
          ) : (
            <>
              {draft && <span className="draft-tag">Draft</span>}
              {t.preview_sender === "user" && (
                <span style={{ color: "var(--text-3)" }}>You: </span>
              )}
              {t.preview_sender === "question" && (
                <span className="asked">Asked: </span>
              )}
              {t.preview || (
                <em style={{ color: "var(--text-3)" }}>No messages yet</em>
              )}
            </>
          )}
        </div>
      </div>
      <div className="thread-side">
        <span title={fullDateTime(t.last_activity_at)}>
          {compactTime(t.last_activity_at, now)}
        </span>
        {needs ? (
          <span className="pill amber">
            {t.pending_questions === 1
              ? "Needs you"
              : `${t.pending_questions} questions`}
          </span>
        ) : unread ? (
          <span
            className="unread-count"
            aria-label={`${t.unread_count} unread`}
          >
            {t.unread_count > 99 ? "99+" : t.unread_count}
          </span>
        ) : null}
        {showArchived && t.archived_at && (
          <span className="pill muted">Archived</span>
        )}
        {t.muted && !needs && (
          <IconBellOff className="muted-icon" aria-label="Muted" />
        )}
      </div>
    </Link>
  );
}

/** Actions for one thread without opening it. */
function RowSheet({ t, onClose }: { t: Thread; onClose: () => void }) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  if (confirmDelete) {
    return (
      <ConfirmSheet
        title="Delete this thread?"
        body={`Everything in "${t.title || t.agent || "this thread"}" is removed for good. This cannot be undone.`}
        confirmLabel="Delete thread"
        danger
        onClose={onClose}
        onConfirm={() =>
          deleteThread(t.id)
            .then(() => toast("Thread deleted"))
            .catch((e) => toast(e.message, "error"))
        }
      />
    );
  }
  return (
    <Sheet onClose={onClose} label="Thread actions">
      <div className="sheet-title">
        <Avatar name={t.agent || t.title || "?"} small />
        <span>{t.title || t.agent || "Thread"}</span>
      </div>
      <button
        type="button"
        className="item"
        onClick={() => {
          onClose();
          updateThread(t.id, { muted: !t.muted })
            .then(() => toast(t.muted ? "Notifications on" : "Thread muted"))
            .catch((e) => toast(e.message, "error"));
        }}
      >
        {t.muted ? <IconBell /> : <IconBellOff />}{" "}
        {t.muted ? "Unmute" : "Mute notifications"}
      </button>
      <button
        type="button"
        className="item"
        onClick={() => {
          onClose();
          const wasArchived = !!t.archived_at;
          updateThread(t.id, { archived: !wasArchived })
            .then(() =>
              wasArchived
                ? toast("Thread restored")
                : toast("Thread archived", "info", {
                    label: "Undo",
                    onClick: () => void updateThread(t.id, { archived: false }),
                  }),
            )
            .catch((e) => toast(e.message, "error"));
        }}
      >
        <IconArchive /> {t.archived_at ? "Unarchive" : "Archive"}
      </button>
      <button
        type="button"
        className="item danger"
        onClick={() => setConfirmDelete(true)}
      >
        <IconTrash /> Delete thread
      </button>
    </Sheet>
  );
}

function EmptyThreads({
  archived,
  searching,
}: {
  archived: boolean;
  searching: boolean;
}) {
  return (
    <div className="empty">
      <div className="glyph">
        <IconInbox />
      </div>
      <h2>
        {searching
          ? "No matches"
          : archived
            ? "Nothing archived"
            : "No threads yet"}
      </h2>
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

const dismissedKey = "fc.setup.dismissed";

function readDismissed(): boolean {
  try {
    return localStorage.getItem(dismissedKey) === "1";
  } catch {
    return false;
  }
}

/** A small, dismissable checklist that gets a new install to a working state. */
function SetupChecklist({ hasThreads }: { hasThreads: boolean }) {
  const pushEnabled = useStore((s) => s.pushEnabled);
  const [push, setPush] = useState<PushState | null>(null);
  const [dismissed, setDismissed] = useState(readDismissed);
  const [tokens, setTokens] = useState<number | null>(null);
  const standalone = isStandalone();

  useEffect(() => {
    if (dismissed) return;
    void getPushState().then(setPush);
    api
      .listTokens()
      .then((r) => setTokens(r.tokens.length))
      .catch(() => setTokens(0));
  }, [dismissed]);

  const pushDone = push === "subscribed";
  const tokenDone = tokens !== null && tokens > 0;
  const allDone = standalone && pushDone && tokenDone && hasThreads;

  useEffect(() => {
    if (allDone && !dismissed) {
      try {
        localStorage.setItem(dismissedKey, "1");
      } catch {
        // ignore
      }
      setDismissed(true);
    }
  }, [allDone, dismissed]);

  if (dismissed) return null;
  // Reserve the card's space so the list does not jump when the checks land.
  if (push === null || tokens === null)
    return <div className="card checklist placeholder" aria-hidden />;

  const enablePush = async () => {
    try {
      const st = await subscribeToPush();
      setPush(st);
      if (st === "subscribed") toast("Notifications on", "success");
      else if (st === "denied")
        toast(
          "Notifications are blocked for this site in your browser settings.",
          "error",
        );
    } catch (e) {
      toast(
        e instanceof Error ? e.message : "Could not enable notifications",
        "error",
      );
    }
  };

  return (
    <div className="card checklist" style={{ marginBottom: 6 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "8px 10px 4px",
        }}
      >
        <h2 style={{ fontSize: 16, flex: 1 }}>Get set up</h2>
        <button
          type="button"
          className="btn ghost small"
          onClick={() => {
            setDismissed(true);
            try {
              localStorage.setItem(dismissedKey, "1");
            } catch {
              // ignore
            }
          }}
        >
          Hide
        </button>
      </div>
      <CheckItem
        done={standalone}
        icon={<IconPhone />}
        label="Install on your phone"
        hint={
          standalone
            ? "Running as an app."
            : isIOS()
              ? "In Safari: Share → Add to Home Screen. Notifications need the installed app."
              : "Use your browser's Install / Add to Home Screen option."
        }
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
          !pushDone &&
          pushEnabled &&
          push !== "unsupported" &&
          push !== "denied" ? (
            <button
              type="button"
              className="btn primary small"
              onClick={enablePush}
            >
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
            <button
              type="button"
              className="btn primary small"
              onClick={() => navigate("/settings/agents")}
            >
              Connect
            </button>
          ) : undefined
        }
      />
    </div>
  );
}

function CheckItem({
  done,
  icon,
  label,
  hint,
  action,
}: {
  done: boolean;
  icon: React.ReactNode;
  label: string;
  hint: string;
  action?: React.ReactNode;
}) {
  return (
    <div className={`check-item ${done ? "done" : ""}`}>
      <div className="mark">{done ? <IconCheck /> : null}</div>
      <div>
        <div
          className="label"
          style={{ display: "flex", alignItems: "center", gap: 8 }}
        >
          <span
            style={{
              display: "inline-flex",
              width: 18,
              height: 18,
              color: "var(--text-3)",
            }}
          >
            {icon}
          </span>
          {label}
        </div>
        <div className="hint">{hint}</div>
      </div>
      <div>{action}</div>
    </div>
  );
}
