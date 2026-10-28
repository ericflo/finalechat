import { useEffect, useMemo, useRef, useState } from "react";
import { Avatar, Sheet } from "../components/Common";
import { IconChevron, IconClock, IconFolder, IconSend, IconSettings } from "../components/Icons";
import { directoryChoices, relayName, relayProfiles, startSession, useSessionStarters } from "../components/NewSession";
import { RunningRelays, agentName, runningRelays } from "../components/Relay";
import { ResourceSettingsSheet } from "../components/ThreadSettings";
import { TopBar } from "../components/TopBar";
import { navigate } from "../lib/router";

const home = (p: string) => p.replace(/^\/(?:home|Users)\/[^/]+/, "~");
/** "~/x" typed on the phone, read on the machine whose project is root. */
const fromHome = (p: string, root: string) => {
  const h = root.match(/^\/(?:home|Users)\/[^/]+/)?.[0];
  return h && /^~(\/|$)/.test(p) ? h + p.slice(1) : p;
};
const base = (p: string) => p.replace(/\/+$/, "").split("/").pop() || p;

/** An agent and folder a relay session started with from this device. */
type RecentRelay = { relay: string; dir: string };

function readRecent(key: string): RecentRelay[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(key) ?? "[]");
    if (!Array.isArray(raw)) return [];
    return raw.filter((r): r is RecentRelay => !!r && typeof r.relay === "string" && r.relay !== "" && typeof r.dir === "string" && r.dir !== "");
  } catch {
    return [];
  }
}

/** Keeps the last five distinct agent and folder pairs, newest first. */
function rememberRecent(key: string, r: RecentRelay) {
  try {
    const next = [r, ...readRecent(key).filter((x) => x.relay !== r.relay || x.dir !== r.dir)].slice(0, 5);
    localStorage.setItem(key, JSON.stringify(next));
  } catch {
    // ignore
  }
}

/** A new session before it exists: it looks like a thread, the folder chip
 * picks where the session will start, the agent chips pick who does the work
 * (eagent itself, or an agent eagent starts and relays to), Settings edits
 * the project's defaults, and the first message is what actually starts the
 * session, so it is born with everything already chosen. Nothing is created
 * until then. */
export function DraftSessionScreen({ resourceId }: { resourceId: string }) {
  const { starters, loaded } = useSessionStarters();
  const starter = starters.find((s) => s.resource.id === resourceId);
  const choices = useMemo(() => (starter ? directoryChoices(starter.resource) : null), [starter]);
  const key = `fc.draft.${resourceId}.cwd`;
  const [cwd, setCwd] = useState<string | null>(() => {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  });
  const [picking, setPicking] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelled = useRef(false);
  const ref = useRef<HTMLTextAreaElement>(null);
  // Who does the work: "" is eagent, otherwise a relay profile's name. The
  // last choice is remembered per connector.
  const profiles = useMemo(() => (starter ? relayProfiles(starter.resource) : []), [starter]);
  // Agents already running on that machine, so the user can go to one
  // instead of starting another.
  const running = useMemo(() => (starter ? runningRelays(starter.resource) : []), [starter]);
  const agentKey = starter ? `fc.draft.${starter.connector.id}.agent` : "";
  const remembered = useMemo(() => {
    try {
      return agentKey ? (localStorage.getItem(agentKey) ?? "") : "";
    } catch {
      return "";
    }
  }, [agentKey]);
  const [picked, setPicked] = useState<string | null>(null);
  const relay = profiles.find((p) => p.name === (picked ?? remembered)) ?? null;
  const who = relay ? relayName(relay) : "eagent";
  const pickAgent = (name: string) => {
    setPicked(name);
    try {
      if (agentKey) localStorage.setItem(agentKey, name);
    } catch {
      // ignore
    }
  };
  const agents = [{ name: "", short: "eagent", title: "eagent does the work" }, ...profiles.map((p) => ({ name: p.name, short: relayName(p), title: `eagent starts ${p.label} and passes your messages on` }))];
  // Agent and folder pairs this device started relays with, still offered.
  const recentKey = starter ? `fc.draft.${starter.connector.id}.recent` : "";
  const recent = useMemo(
    () =>
      (recentKey ? readRecent(recentKey) : []).flatMap((r) => {
        const p = profiles.find((x) => x.name === r.relay);
        return p ? [{ ...r, label: `${agentName(p.agent, relayName(p))} · ${base(r.dir)}`, title: `${p.label} in ${home(r.dir)}` }] : [];
      }),
    [recentKey, profiles],
  );
  // A relayed agent may start with nothing to do yet; eagent needs a task.
  const ready = !!text.trim() || !!relay;
  // Set again on mount: StrictMode's dev remount would otherwise leave it true.
  useEffect(() => {
    cancelled.current = false;
    return () => {
      cancelled.current = true;
    };
  }, []);
  useEffect(() => {
    ref.current?.focus();
  }, [loaded]);

  const dir = cwd ?? choices?.root ?? "";
  const choose = (path: string) => {
    setCwd(path);
    setPicking(false);
    try {
      localStorage.setItem(key, path);
    } catch {
      // ignore
    }
  };

  const send = async () => {
    if (!starter || busy || !ready) return;
    setBusy(true);
    setError(null);
    try {
      const threadId = await startSession(starter, text, dir || null, relay, setStatus, () => cancelled.current);
      if (relay && dir) rememberRecent(recentKey, { relay: relay.name, dir });
      navigate(`/t/${threadId}`, { replace: true });
    } catch (err) {
      if (!cancelled.current) {
        setError(err instanceof Error ? err.message : "Could not start the session.");
        setStatus(null);
        setBusy(false);
      }
    }
  };

  const title = starter ? starter.resource.label : "New session";
  return (
    <div className="page thread">
      <TopBar
        title={
          <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, maxWidth: "100%" }}>
            <Avatar name={who} small />
            <span style={{ minWidth: 0, flex: "0 1 auto", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
          </span>
        }
        subtitle={starter ? `new ${relay ? `${who} ` : ""}session · ${starter.connector.name}${starter.online ? "" : " · offline"}` : loaded ? "not connected" : "loading…"}
        backTo="/"
        right={
          starter ? (
            <button type="button" className="icon-btn" aria-label="Settings" title="Project settings" onClick={() => setSettingsOpen(true)}>
              <IconSettings aria-hidden="true" />
            </button>
          ) : null
        }
        below={
          starter ? (
            <div className="meta-strip">
              <button type="button" className="meta-chip" title={`Start in ${dir}. Tap to change.`} onClick={() => setPicking(true)} disabled={busy}>
                <IconFolder /> {home(dir)}
              </button>
            </div>
          ) : null
        }
      />
      <div className="messages">
        {!loaded && <div className="skeleton" style={{ height: 90, width: "80%" }} />}
        {loaded && !starter && (
          <div className="empty">
            <h2>Not connected</h2>
            <p>Start eagent in the project (serve, a session, or connector run) and come back.</p>
          </div>
        )}
        {starter && !status && (
          <div className="empty">
            <h2>New session</h2>
            {relay ? (
              <p>
                {relay.label} will work in <code>{home(dir)}</code>. eagent starts it and passes your messages on. Say what to do, or just start it.
              </p>
            ) : (
              <p>
                It will start in <code>{home(dir)}</code> with this project&rsquo;s settings. Change either above, then say what to do.
              </p>
            )}
            {!starter.online && <p>This project&rsquo;s eagent is offline right now. Start it there, then send.</p>}
          </div>
        )}
        {starter && !status && running.length > 0 && <RunningRelays relays={running} />}
        {status && (
          <div className="empty">
            <span className="spinner" />
            <p style={{ marginTop: 12 }}>{status}</p>
          </div>
        )}
        {error && (
          <div className="empty">
            <p className="form-error">{error}</p>
          </div>
        )}
      </div>
      {starter && (
        <div className="composer-wrap">
          {profiles.length > 0 && (
            <div className="agent-picker" role="group" aria-label="Who does the work">
              {agents.map((o) => {
                const active = (relay?.name ?? "") === o.name;
                return (
                  <button key={o.name || "eagent"} type="button" className={`agent-chip${active ? " active" : ""}`} aria-pressed={active} title={o.title} disabled={busy} onClick={() => pickAgent(o.name)}>
                    {o.short}
                  </button>
                );
              })}
            </div>
          )}
          {profiles.length > 0 && (
            // Where it works, right under who does the work.
            <div className="folder-row">
              <button type="button" className="folder-chip" title={`${relay ? `${who} works` : "Starts"} in ${home(dir)}. Tap to change.`} aria-label={`Folder: ${home(dir)}. Change`} disabled={busy} onClick={() => setPicking(true)}>
                <IconFolder aria-hidden="true" />
                <span>{home(dir)}</span>
                <IconChevron aria-hidden="true" className="folder-chip-more" />
              </button>
            </div>
          )}
          {recent.length > 0 && (
            <div className="recent-picker" role="group" aria-label="Recent">
              <IconClock className="recent-icon" aria-hidden="true" />
              {recent.map((r) => {
                const active = relay?.name === r.relay && dir === r.dir;
                return (
                  <button
                    key={`${r.relay}:${r.dir}`}
                    type="button"
                    className={`recent-chip${active ? " active" : ""}`}
                    aria-pressed={active}
                    title={r.title}
                    disabled={busy}
                    onClick={() => {
                      pickAgent(r.relay);
                      choose(r.dir);
                    }}
                  >
                    {r.label}
                  </button>
                );
              })}
            </div>
          )}
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
              placeholder={relay ? `What should ${who} do? (optional)` : "What should it do?"}
              value={text}
              disabled={busy}
              maxLength={32768}
              onChange={(e) => {
                setText(e.target.value);
                const el = e.target;
                el.style.height = "auto";
                el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  void send();
                }
              }}
            />
            <button type="submit" className={`send-btn${!busy && !text.trim() && relay ? " label" : ""}`} aria-label="Start the session" disabled={busy || !ready}>
              {busy ? <span className="spinner" style={{ borderTopColor: "#fff", borderColor: "rgba(255,255,255,0.35)" }} /> : text.trim() || !relay ? <IconSend /> : "Start"}
            </button>
          </form>
        </div>
      )}
      {picking && choices && <DirectorySheet choices={choices} current={dir} who={relay ? who : null} onChoose={choose} onClose={() => setPicking(false)} />}
      {settingsOpen && <ResourceSettingsSheet resourceId={resourceId} onClose={() => setSettingsOpen(false)} />}
    </div>
  );
}

function DirectorySheet({ choices, current, who, onChoose, onClose }: { choices: ReturnType<typeof directoryChoices>; current: string; who: string | null; onChoose: (p: string) => void; onClose: () => void }) {
  const [custom, setCustom] = useState("");
  // An absolute path, or ~/… (eagent reads ~ as the machine's home).
  const typed = fromHome(custom.trim(), choices.root).replace(/(.)\/+$/, "$1");
  const isPath = /^(\/|~(\/|$))/.test(typed);
  // Anything else typed narrows the lists: "snow" finds ~/Development/snowdown.
  const q = custom.trim().toLowerCase();
  const matches = (p: string) => !q || (isPath ? p.startsWith(typed) : home(p).toLowerCase().includes(q));
  const groups: { title: string; paths: string[] }[] = [
    { title: "Project", paths: [choices.root] },
    { title: "Recent", paths: choices.recent.filter((p) => p !== choices.root) },
    { title: "Inside the project", paths: choices.children },
    { title: "Next to the project", paths: choices.siblings },
  ].map((g) => ({ ...g, paths: g.paths.filter(matches) }));
  const found = [...new Set(groups.flatMap((g) => g.paths))];
  const pick = isPath ? typed : q && found.length === 1 ? found[0] : null;
  // The Sheet focuses its first field, which on a phone raises the keyboard
  // over the folders. Start on the folders; typing is a tap away.
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>(".item.active, .item")?.focus({ preventScroll: true });
  }, []);
  return (
    <Sheet onClose={onClose} label="Start in">
      <div style={{ padding: "4px 6px" }}>
        <h2 style={{ fontSize: 18, marginBottom: 6 }}>{who ? `Where ${who} works` : "Start in"}</h2>
        <p style={{ marginBottom: 10 }}>{who ? `eagent starts ${who} in this folder; the session\u2019s settings and logs stay with the project.` : "The session\u2019s first commands run here; its settings and logs stay with the project."}</p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (pick) onChoose(pick);
          }}
          style={{ display: "flex", gap: 8, marginBottom: 6 }}
        >
          <input value={custom} placeholder="Find a folder, or ~/path" aria-label="Find a folder or type a path" enterKeyHint="go" autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={(e) => setCustom(e.target.value)} style={{ flex: 1, fontFamily: "var(--mono)" }} />
          <button type="submit" className="btn" disabled={!pick}>
            Use
          </button>
        </form>
        <div ref={list}>
          {groups
            .filter((g) => g.paths.length > 0)
            .map((g) => (
              <div key={g.title}>
                <div className="section-title">{g.title}</div>
                {g.paths.map((p) => (
                  <button key={p} type="button" className={`item${p === current ? " active" : ""}`} onClick={() => onChoose(p)}>
                    <IconFolder /> <span style={{ fontFamily: "var(--mono)", fontSize: 13, overflow: "hidden", textOverflow: "ellipsis" }}>{home(p)}</span>
                  </button>
                ))}
              </div>
            ))}
        </div>
        {q && !isPath && found.length === 0 && <p style={{ marginTop: 10 }}>No folder here matches. Type a full path, like ~/Development/project.</p>}
      </div>
    </Sheet>
  );
}
