import { useEffect, useMemo, useRef, useState } from "react";
import { Avatar, Sheet } from "../components/Common";
import { IconFolder, IconSend, IconSettings } from "../components/Icons";
import { directoryChoices, startSession, useSessionStarters } from "../components/NewSession";
import { TopBar } from "../components/TopBar";
import { navigate } from "../lib/router";

const home = (p: string) => p.replace(/^\/(?:home|Users)\/[^/]+/, "~");

/** A new session before it exists: it looks like a thread, the folder chip
 * picks where the session will start, Settings edits the project's
 * defaults, and the first message is what actually starts the session, so it
 * is born with everything already chosen. Nothing is created until then. */
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
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelled = useRef(false);
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => () => { cancelled.current = true; }, []);
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
    if (!starter || busy || !text.trim()) return;
    setBusy(true);
    setError(null);
    try {
      const threadId = await startSession(starter, text, dir || null, setStatus, () => cancelled.current);
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
            <Avatar name="eagent" small />
            <span style={{ minWidth: 0, flex: "0 1 auto", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
          </span>
        }
        subtitle={starter ? `new session · ${starter.connector.name}${starter.online ? "" : " · offline"}` : loaded ? "not connected" : "loading…"}
        backTo="/"
        right={
          starter ? (
            <button type="button" className="icon-btn" aria-label="Settings" title="Project settings" onClick={() => navigate(`/settings/resources/${resourceId}?back=${encodeURIComponent(`/new/${resourceId}`)}`)}>
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
            <h2>No eagent here</h2>
            <p>Start eagent in the project (serve, a session, or connector run) and come back.</p>
          </div>
        )}
        {starter && !status && (
          <div className="empty">
            <h2>New session</h2>
            <p>
              It will start in <code>{home(dir)}</code> with this project&rsquo;s settings. Change either above, then say what to do.
            </p>
            {!starter.online && <p>This project&rsquo;s eagent is offline right now; the message will wait for it.</p>}
          </div>
        )}
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
              placeholder="What should it do?"
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
            <button type="submit" className="send-btn" aria-label="Start the session" disabled={busy || !text.trim()}>
              {busy ? <span className="spinner" style={{ borderTopColor: "#fff", borderColor: "rgba(255,255,255,0.35)" }} /> : <IconSend />}
            </button>
          </form>
        </div>
      )}
      {picking && choices && <DirectorySheet choices={choices} current={dir} onChoose={choose} onClose={() => setPicking(false)} />}
    </div>
  );
}

function DirectorySheet({ choices, current, onChoose, onClose }: { choices: ReturnType<typeof directoryChoices>; current: string; onChoose: (p: string) => void; onClose: () => void }) {
  const [custom, setCustom] = useState("");
  const groups: { title: string; paths: string[] }[] = [
    { title: "Project", paths: [choices.root] },
    { title: "Recent", paths: choices.recent.filter((p) => p !== choices.root) },
    { title: "Inside the project", paths: choices.children },
    { title: "Next to the project", paths: choices.siblings },
  ];
  return (
    <Sheet onClose={onClose} label="Start in">
      <div style={{ padding: "4px 6px" }}>
        <h2 style={{ fontSize: 18, marginBottom: 6 }}>Start in</h2>
        <p style={{ marginBottom: 10 }}>The session&rsquo;s first commands run here; its settings and logs stay with the project.</p>
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
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (custom.trim().startsWith("/")) onChoose(custom.trim());
          }}
          style={{ display: "flex", gap: 8, marginTop: 10 }}
        >
          <input value={custom} placeholder="/absolute/path" onChange={(e) => setCustom(e.target.value)} style={{ flex: 1 }} />
          <button type="submit" className="btn" disabled={!custom.trim().startsWith("/")}>
            Use
          </button>
        </form>
      </div>
    </Sheet>
  );
}
