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
  bulkDeleteThreads,
  bulkUpdateThreads,
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
  const [selectMode, setSelectMode] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [bulkBusy, setBulkBusy] = useState(false);
  const { starters } = useSessionStarters();
  const searchTimer = useRef<number | undefined>(undefined);
  const lastIndex = useRef<number | null>(null);
  const selectAllRef = useRef<HTMLInputElement | null>(null);
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
  // Bulk selection lives here (not in the store): it clears whenever the
  // visible set changes meaning — tab switch, new search — or after delete.
  const switchTab = (next: "active" | "archived") => {
    setTab(next);
    setSelected(new Set());
  };
  useEffect(() => {
    setSelected(new Set());
  }, [query]);
  const toggleSelect = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    lastIndex.current = null;
  }, []);
  // Shift-click range select (desktop): extend from the last toggled row.
  const toggleSelectRange = useCallback(
    (id: string, index: number, extend: boolean, ids: string[]) => {
      if (
        extend &&
        lastIndex.current !== null &&
        lastIndex.current !== index
      ) {
        const [from, to] =
          lastIndex.current < index
            ? [lastIndex.current, index]
            : [index, lastIndex.current];
        const range = ids.slice(from, to + 1);
        setSelected((prev) => {
          const next = new Set(prev);
          // If the anchor row is selected, select the range; else deselect it.
          const on = next.has(ids[lastIndex.current as number] as string);
          for (const rid of range) {
            if (on) next.add(rid);
            else next.delete(rid);
          }
          return next;
        });
      } else {
        toggleSelect(id);
      }
      lastIndex.current = index;
    },
    [toggleSelect],
  );
  // Long-press / right-click enters select mode with the row pre-selected.
  const enterSelect = useCallback((id: string) => {
    setSelectMode(true);
    setSelected(new Set([id]));
    lastIndex.current = null;
  }, []);
  const toggleSelectMode = () => {
    setSelectMode((v) => {
      if (v) setSelected(new Set());
      return !v;
    });
  };  const list = useMemo(() => {
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

  const selectedIds = useMemo(() => [...selected], [selected]);
  // Indeterminate state for the header select-all checkbox.
  useEffect(() => {
    const el = selectAllRef.current;
    if (el) {
      el.indeterminate =
        selectedIds.length > 0 && selectedIds.length < list.length;
    }
  }, [selectedIds, list.length]);
  // Drop ids that no longer exist (deleted elsewhere) so the count stays true.
  useEffect(() => {
    setSelected((prev) => {
      if (prev.size === 0) return prev;
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (threads[id]) next.add(id);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [threads]);
  const allMuted =
    selectedIds.length > 0 &&
    selectedIds.every((id) => threads[id]?.muted);
  const plural = (n: number, what: string) =>
    `${n} thread${n === 1 ? "" : "s"} ${what}`;

  const doBulkArchive = async () => {
    const ids = [...selected];
    if (ids.length === 0 || bulkBusy) return;
    const archiving = tab !== "archived";
    setBulkBusy(true);
    try {
      const res = await bulkUpdateThreads(ids, { archived: archiving });
      const n = res.threads.length || ids.length;
      if (archiving) {
        toast(plural(n, "archived"), "info", {
          label: "Undo",
          onClick: () =>
            void bulkUpdateThreads(ids, { archived: false }).catch((e) =>
              toast(e instanceof Error ? e.message : "Could not undo", "error"),
            ),
        });
      } else {
        toast(plural(n, "unarchived"), "success");
      }
      // Archived threads leave this tab; unarchived ones leave the other.
      // Keep the selection of whatever remains instead of clearing it all.
      setSelected((prev) => {
        const gone = new Set(ids);
        const next = new Set([...prev].filter((id) => !gone.has(id)));
        return next;
      });
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not update", "error");
    } finally {
      setBulkBusy(false);
    }
  };

  const doBulkMute = async () => {
    const ids = [...selected];
    if (ids.length === 0 || bulkBusy) return;
    const target = !allMuted;
    setBulkBusy(true);
    try {
      const res = await bulkUpdateThreads(ids, { muted: target });
      const n = res.threads.length || ids.length;
      toast(plural(n, target ? "muted" : "unmuted"), "success");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not update", "error");
    } finally {
      setBulkBusy(false);
    }
  };

  const doBulkMarkRead = async () => {
    const ids = [...selected];
    if (ids.length === 0 || bulkBusy) return;
    setBulkBusy(true);
    try {
      const res = await bulkUpdateThreads(ids, { mark_read: true });
      const n = res.threads.length || ids.length;
      toast(plural(n, "marked read"), "success");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not update", "error");
    } finally {
      setBulkBusy(false);
    }
  };

  const doBulkDelete = async () => {
    const ids = [...selected];
    if (ids.length === 0 || bulkBusy) return;
    setBulkBusy(true);
    try {
      const res = await bulkDeleteThreads(ids);
      const n = res.deleted.length || ids.length;
      const gone = new Set(ids);
      const remaining = list.filter((t) => !gone.has(t.id));
      setSelected(new Set());
      setConfirmBulkDelete(false);
      // Nothing left to act on: leave select mode instead of an empty bar.
      if (remaining.length === 0) setSelectMode(false);
      toast(plural(n, "deleted"));
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not delete", "error");
    } finally {
      setBulkBusy(false);
    }
  };

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
              <span className="threads-label">
                {selectMode ? (
                  <label className="select-all">
                    <input
                      ref={selectAllRef}
                      type="checkbox"
                      className="select-box"
                      aria-label="Select all visible threads"
                      checked={
                        list.length > 0 && selectedIds.length === list.length
                      }
                      onChange={() =>
                        setSelected((prev) =>
                          prev.size === list.length
                            ? new Set()
                            : new Set(list.map((t) => t.id)),
                        )
                      }
                    />
                    {selectedIds.length === 0
                      ? "None selected"
                      : `${selectedIds.length} selected`}
                  </label>
                ) : needsYou ? (
                  "Threads that need you"
                ) : (
                  "Threads"
                )}
              </span>
              {!needsYou && (
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <div
                    className="segmented"
                    style={{ textTransform: "none", letterSpacing: 0 }}
                  >
                    <button
                      type="button"
                      className={tab === "active" ? "active" : ""}
                      onClick={() => switchTab("active")}
                    >
                      Active
                    </button>
                    <button
                      type="button"
                      className={tab === "archived" ? "active" : ""}
                      onClick={() => switchTab("archived")}
                    >
                      Archived
                    </button>
                  </div>
                  <button
                    type="button"
                    className={`btn small ${selectMode ? "primary" : ""}`}
                    aria-pressed={selectMode}
                    onClick={toggleSelectMode}
                  >
                    {selectMode ? "Done" : "Select"}
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
                {list.map((t, i) => (
                  <ThreadRow
                    key={t.id}
                    t={t}
                    showArchived={searching}
                    onMenu={() => setRowMenu(t)}
                    selectMode={selectMode}
                    selected={selected.has(t.id)}
                    onToggleSelect={(e) =>
                      toggleSelectRange(
                        t.id,
                        i,
                        !!(e && (e as { shiftKey?: boolean }).shiftKey),
                        list.map((x) => x.id),
                      )
                    }
                    onEnterSelect={() => enterSelect(t.id)}
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
      {selectMode && (
        <div
          className="bulk-bar"
          role="toolbar"
          aria-label="Bulk actions"
          aria-busy={bulkBusy}
        >
          <div className="bulk-count">
            <span aria-live="polite">
              {selectedIds.length === 0
                ? "Nothing selected"
                : `${selectedIds.length} selected`}
            </span>
            {selectedIds.length === 0 ? (
              <button
                type="button"
                className="btn primary small"
                disabled={bulkBusy}
                aria-disabled={bulkBusy}
                onClick={() => setSelected(new Set(list.map((t) => t.id)))}
              >
                Select all visible
              </button>
            ) : (
              <>
                <button
                  type="button"
                  className="btn ghost small"
                  disabled={bulkBusy}
                  aria-disabled={bulkBusy}
                  onClick={() => setSelected(new Set(list.map((t) => t.id)))}
                >
                  Select all visible
                </button>
                <button
                  type="button"
                  className="btn ghost small"
                  disabled={bulkBusy}
                  aria-disabled={bulkBusy}
                  onClick={() => setSelected(new Set())}
                >
                  Clear
                </button>
              </>
            )}
          </div>
          <div className="bulk-actions">
            <button
              type="button"
              className="btn small"
              disabled={selectedIds.length === 0 || bulkBusy}
              aria-disabled={selectedIds.length === 0 || bulkBusy}
              onClick={() => void doBulkArchive()}
            >
              <IconArchive />
              {bulkBusy ? "Working…" : tab === "archived" ? "Unarchive" : "Archive"}
            </button>
            <button
              type="button"
              className="btn small"
              disabled={selectedIds.length === 0 || bulkBusy}
              aria-disabled={selectedIds.length === 0 || bulkBusy}
              onClick={() => void doBulkMute()}
            >
              {allMuted ? <IconBell /> : <IconBellOff />}
              {allMuted ? "Unmute" : "Mute"}
            </button>
            <button
              type="button"
              className="btn small"
              disabled={selectedIds.length === 0 || bulkBusy}
              aria-disabled={selectedIds.length === 0 || bulkBusy}
              onClick={() => void doBulkMarkRead()}
            >
              <IconCheck />
              Mark read
            </button>
            <button
              type="button"
              className="btn small danger"
              disabled={selectedIds.length === 0 || bulkBusy}
              aria-disabled={selectedIds.length === 0 || bulkBusy}
              onClick={() => setConfirmBulkDelete(true)}
            >
              <IconTrash />
              Delete
            </button>
          </div>
        </div>
      )}
      {confirmBulkDelete && (
        <ConfirmSheet
          title={`Delete ${selectedIds.length} thread${selectedIds.length === 1 ? "" : "s"}?`}
          body="Everything in the selected threads is removed for good. This cannot be undone."
          confirmLabel="Delete threads"
          danger
          onClose={() => setConfirmBulkDelete(false)}
          onConfirm={() => void doBulkDelete()}
        />
      )}
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
  selectMode,
  selected,
  onToggleSelect,
  onEnterSelect,
}: {
  t: Thread;
  showArchived: boolean;
  onMenu: () => void;
  selectMode?: boolean;
  selected?: boolean;
  onToggleSelect?: (e?: { shiftKey?: boolean }) => void;
  onEnterSelect?: () => void;
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
      // Selection owns long-press: enter select mode with this row, or
      // toggle it when already selecting — never open the RowSheet here.
      if (selectMode) onToggleSelect?.();
      else onEnterSelect?.();
    }, 480);
  }, [selectMode, onToggleSelect, onEnterSelect]);
  const endPress = useCallback(
    () => window.clearTimeout(pressTimer.current),
    [],
  );

  return (
    <Link
      href={`/t/${t.id}`}
      className={`thread-row ${unread ? "unread" : ""} ${needs ? "needs-you" : ""} ${activity ? "live" : ""} ${selectMode ? "selecting" : ""} ${selected ? "selected" : ""}`}
      onPointerDown={startPress}
      onPointerUp={endPress}
      onPointerLeave={endPress}
      onPointerCancel={endPress}
      onContextMenu={(e: React.MouseEvent) => {
        e.preventDefault();
        // Right-click enters select mode with this row pre-selected;
        // in select mode it toggles the row instead.
        if (selectMode) onToggleSelect?.();
        else onEnterSelect?.();
      }}
      onDoubleClick={(e: React.MouseEvent) => {
        // Quick actions still live here: double-click opens the RowSheet
        // when not selecting. Long-press / right-click belong to selection.
        if (!selectMode) {
          e.preventDefault();
          onMenu();
        }
      }}
      onClickCapture={(e: React.MouseEvent) => {
        if (suppressClick.current) {
          e.preventDefault();
          e.stopPropagation();
          suppressClick.current = false;
          return;
        }
        // In select mode a tap toggles instead of navigating (shift=tap
        // range on desktop). The checkbox handles its own events below.
        if (selectMode) {
          e.preventDefault();
          e.stopPropagation();
          onToggleSelect?.({ shiftKey: e.shiftKey });
        }
      }}
    >
      {selectMode && (
        <span
          className="select-hit"
          onClick={(e: React.MouseEvent) => {
            e.preventDefault();
            e.stopPropagation();
            // Clicks on the input itself are handled by the input below.
            if ((e.target as HTMLElement).tagName === "INPUT") return;
            onToggleSelect?.();
          }}
        >
          <input
            type="checkbox"
            className="select-box"
            aria-label="Select thread"
            checked={!!selected}
            onClick={(e: React.MouseEvent) => e.stopPropagation()}
            onChange={() => onToggleSelect?.()}
          />
        </span>
      )}
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
