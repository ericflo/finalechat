import { useEffect, useMemo, useRef, useState } from "react";
import { Sheet } from "../components/Common";
import { IconChevron, IconFolder, IconSend, IconSettings } from "../components/Icons";
import { ProjectPickerSheet, directoryChoices, relayName, relayProfiles, startSession, starterHost, startsWithoutRelay, useSessionStarters, type SessionStarter } from "../components/NewSession";
import { RunningRelays, agentName, runningRelays } from "../components/Relay";
import { ResourceSettingsSheet } from "../components/ThreadSettings";
import { SettingsForm, formSummary, startModel, startParameters, type FormModel } from "../components/SettingsForm";
import { TopBar } from "../components/TopBar";
import { navigate } from "../lib/router";
import { pickDefaultStarter, pickableStarters, starterDisplayName } from "../lib/starters";

const STARTER_KEY = "fc.draft.starter";
const home = (p: string) => p.replace(/^\/(?:home|Users)\/[^/]+/, "~");
/** "~/x" typed on the phone, read on the machine whose project is root. */
const fromHome = (p: string, root: string) => {
  const h = root.match(/^\/(?:home|Users)\/[^/]+/)?.[0];
  return h && /^~(\/|$)/.test(p) ? h + p.slice(1) : p;
};
/** "eagent" stays as written; "the agent" starts a sentence as "The agent". */
const cap = (s: string) => (s.startsWith("the ") ? "T" + s.slice(1) : s);
const base = (p: string) => p.replace(/\/+$/, "").split("/").pop() || p;
const readStorage = (key: string): string | null => {
  if (!key) return null;
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
};
const writeStorage = (key: string, value: string) => {
  if (!key) return;
  try {
    localStorage.setItem(key, value);
  } catch {
    // ignore
  }
};
const readOptions = (key: string): Record<string, unknown> => {
  const text = readStorage(key);
  if (!text) return {};
  try {
    const raw: unknown = JSON.parse(text);
    return raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : {};
  } catch {
    return {};
  }
};

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

/** A new session before it exists. The page is a short form: who does the
 * work (an agent the integration starts and relays to, or, when the
 * integration offers it, the integration itself), what it runs with, and
 * the folder it starts in; under it, what is already running on that
 * machine, so the user can go there instead. The message box at the
 * bottom is all that stays in reach of the thumb, and the first message is
 * what starts the session, born with everything already chosen. Nothing is
 * created until then. */
export function DraftSessionScreen({ resourceId }: { resourceId: string | null }) {
  const { starters, loaded } = useSessionStarters();
  const [rememberedStarter, setRememberedStarter] = useState<string | null>(() => readStorage(STARTER_KEY));
  const starter = resourceId ? starters.find((s) => s.resource.id === resourceId) : pickDefaultStarter(starters, rememberedStarter);
  const activeId = starter?.resource.id ?? "";
  const pickable = useMemo(() => pickableStarters(starters, starter?.resource.id ?? null), [starters, starter?.resource.id]);
  const choices = useMemo(() => (starter ? directoryChoices(starter.resource) : null), [starter]);
  const key = activeId ? `fc.draft.${activeId}.cwd` : "";
  const [cwdByKey, setCwdByKey] = useState<{ key: string; value: string | null }>(() => ({ key, value: readStorage(key) }));
  const cwd = cwdByKey.key === key ? cwdByKey.value : readStorage(key);
  const [pickingStarter, setPickingStarter] = useState(false);
  const [picking, setPicking] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelled = useRef(false);
  const ref = useRef<HTMLTextAreaElement>(null);
  // Who does the work: a relay profile's name, or "" for the integration
  // itself when its session.start accepts no relay. The last choice is
  // remembered per connector.
  const profiles = useMemo(() => (starter ? relayProfiles(starter.resource) : []), [starter]);
  const host = starter ? starterHost(starter) : "the agent";
  const canSelf = starter ? startsWithoutRelay(starter.resource) : true;
  // Agents already running on that machine, so the user can go to one
  // instead of starting another.
  const running = useMemo(() => (starter ? runningRelays(starter.resource) : []), [starter]);
  const agentKey = starter ? `fc.draft.${starter.connector.id}.agent` : "";
  const remembered = useMemo(() => readStorage(agentKey) ?? "", [agentKey]);
  const [pickedByConn, setPickedByConn] = useState<{ conn: string; name: string } | null>(null);
  const picked = pickedByConn && pickedByConn.conn === starter?.connector.id ? pickedByConn.name : null;
  // An integration that always relays starts the remembered profile, else
  // the first it lists (its default).
  const relay = profiles.find((p) => p.name === (picked ?? remembered)) ?? (canSelf ? null : (profiles[0] ?? null));
  const who = relay ? relayName(relay) : host;
  const pickAgent = (name: string) => {
    if (starter) setPickedByConn({ conn: starter.connector.id, name });
    writeStorage(agentKey, name);
  };
  const agents = [...(canSelf ? [{ name: "", short: host, title: `${host} does the work` }] : []), ...profiles.map((p) => ({ name: p.name, short: relayName(p), title: `${host} starts ${p.label} and passes your messages on` }))];
  // Agent and folder pairs this device started relays with, still offered.
  const recentKey = starter ? `fc.draft.${starter.connector.id}.recent` : "";
  const recent = useMemo(
    () =>
      (recentKey ? readRecent(recentKey) : []).flatMap((r) => {
        const p = profiles.find((x) => x.name === r.relay);
        return p ? [{ ...r, agent: agentName(p.agent, relayName(p)), folder: base(r.dir), title: `${p.label} in ${home(r.dir)}` }] : [];
      }),
    [recentKey, profiles],
  );
  // The options the agent asks for when it starts a session (its
  // configuration and models, or the model of the agent it relays to),
  // remembered per connector. Each input says which agent choice it applies
  // to; only those are offered.
  const startAction = starter?.resource.descriptor.actions?.find((a) => a.operation === "session.start");
  const optionsKey = starter ? `fc.draft.${starter.connector.id}.options` : "";
  const [optionsByKey, setOptionsByKey] = useState<{ key: string; values: Record<string, unknown> }>(() => ({ key: optionsKey, values: readOptions(optionsKey) }));
  const options = optionsByKey.key === optionsKey ? optionsByKey.values : readOptions(optionsKey);
  const saveOptions = (next: Record<string, unknown>) => {
    setOptionsByKey({ key: optionsKey, values: next });
    writeStorage(optionsKey, JSON.stringify(next));
  };
  const [optionsOpen, setOptionsOpen] = useState(false);
  const built = starter && startAction?.inputs?.length ? startModel(starter.resource.descriptor, startAction, options, saveOptions, { relay: relay?.name ?? "" }) : null;
  const form = built && built.items.length ? built : null;
  const summary = form ? formSummary(form) : [];
  // A relayed agent may start with nothing to do yet; without one the
  // integration needs a task.
  const ready = !!text.trim() || !!relay;
  // Set again on mount: StrictMode's dev remount would otherwise leave it true.
  useEffect(() => {
    cancelled.current = false;
    return () => {
      cancelled.current = true;
    };
  }, []);
  // With a keyboard the message is the next thing to type. On a phone the
  // form comes first, and focus would only raise the keyboard over it.
  useEffect(() => {
    if (window.matchMedia("(hover: hover) and (pointer: fine)").matches) ref.current?.focus();
  }, [loaded]);

  const dir = cwd ?? choices?.root ?? "";
  const choose = (path: string) => {
    setCwdByKey({ key, value: path });
    setPicking(false);
    writeStorage(key, path);
  };
  const chooseStarter = (s: SessionStarter) => {
    setRememberedStarter(s.resource.id);
    writeStorage(STARTER_KEY, s.resource.id);
    setPickingStarter(false);
    setPicking(false);
    setOptionsOpen(false);
    setSettingsOpen(false);
    setError(null);
    navigate(`/new/${s.resource.id}`, { replace: true });
  };

  const send = async () => {
    if (!starter || busy || !ready) return;
    setBusy(true);
    setError(null);
    writeStorage(STARTER_KEY, starter.resource.id);
    try {
      const threadId = await startSession(starter, text, dir || null, relay, form ? startParameters(form) : {}, setStatus, () => cancelled.current);
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

  // One row per thing it will run with. A relayed agent has one (its model
  // and effort), under the agent's own name, which the picker above already
  // says: call that row what it is.
  const rows = summary.map((l) => ({ ...l, label: relay && summary.length === 1 ? "Model" : l.title }));
  const optionsTitle = relay ? `${who}'s model and reasoning effort for this session` : "Configuration and models for this session";
  return (
    <div className="page thread">
      <TopBar
        title="New session"
        subtitle={starter ? `${starter.connector.name}${starter.online ? "" : " · offline"}` : loaded ? "not connected" : "loading…"}
        backTo="/"
        right={
          starter ? (
            <button type="button" className="icon-btn" aria-label="Settings" title="Project settings" onClick={() => setSettingsOpen(true)}>
              <IconSettings aria-hidden="true" />
            </button>
          ) : null
        }
      />
      <div className="draft">
        {!loaded && <div className="skeleton" style={{ height: 150 }} />}
        {loaded && !starter && (
          <div className="empty">
            <h2>Not connected</h2>
            <p>Start the agent&rsquo;s connector in the project and come back.</p>
            {pickable.length > 0 && (
              <button type="button" className="btn primary" style={{ marginTop: 12 }} onClick={() => setPickingStarter(true)}>
                Choose a connected host
              </button>
            )}
          </div>
        )}
        {starter && !status && (
          <>
            {pickable.length > 1 && (
              <section>
                <div className="setup-list" role="group" aria-label="Which host">
                  <button
                    type="button"
                    className="setup-row"
                    aria-label={`Host: ${starterDisplayName(starter)}. Change`}
                    title={`${starter.connector.name}${starter.online ? "" : " · offline"}. Tap to change.`}
                    disabled={busy}
                    onClick={() => setPickingStarter(true)}
                  >
                    <span className="k">Host</span>
                    <span className="v starter-v">
                      <span className={`starter-dot${starter.online ? " online" : ""}`} aria-hidden="true" />
                      <span className="starter-name">{starterDisplayName(starter)}</span>
                    </span>
                    <IconChevron aria-hidden="true" />
                  </button>
                </div>
              </section>
            )}
            {!starter.online && <p className="draft-warn">This project&rsquo;s {host} is offline right now. Start it there, then send.</p>}
            {profiles.length > 0 && (
              <section>
                <h2 className="draft-label" id="draft-agent">
                  Agent
                </h2>
                <div className="agent-picker" role="group" aria-labelledby="draft-agent">
                  {agents.map((o) => {
                    const active = (relay?.name ?? "") === o.name;
                    return (
                      <button key={o.name || "-self"} type="button" className={`agent-option${active ? " active" : ""}`} aria-pressed={active} title={o.title} disabled={busy} onClick={() => pickAgent(o.name)}>
                        {o.short}
                      </button>
                    );
                  })}
                </div>
              </section>
            )}
            <section>
              <div className="setup-list" role="group" aria-label="What it will run with">
                {rows.map((l) => (
                  <button key={l.title} type="button" className={`setup-row${l.changed ? " changed" : ""}`} title={optionsTitle} disabled={busy} onClick={() => setOptionsOpen(true)}>
                    <span className="k">{l.label}</span>
                    <span className="v">{l.text || "—"}</span>
                    <IconChevron aria-hidden="true" />
                  </button>
                ))}
                <button type="button" className="setup-row" title={`${relay ? `${who} works` : "Starts"} in ${home(dir)}. Tap to change.`} aria-label={`Folder: ${home(dir)}. Change`} disabled={busy} onClick={() => setPicking(true)}>
                  <span className="k">Folder</span>
                  <span className="v mono">{home(dir)}</span>
                  <IconChevron aria-hidden="true" />
                </button>
              </div>
              <p className="draft-note">{relay ? `${cap(host)} starts ${who} in this folder and passes your messages on.` : `${cap(host)} does the work itself, starting in this folder.`}</p>
            </section>
            {recent.length > 0 && (
              <section>
                <h2 className="draft-label" id="draft-recent">
                  Recent
                </h2>
                <div className="recent-picker" role="group" aria-labelledby="draft-recent">
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
                        {r.agent} <span className="in">in</span> <span className="dir">{r.folder}</span>
                      </button>
                    );
                  })}
                </div>
              </section>
            )}
            {running.length > 0 && <RunningRelays relays={running} />}
          </>
        )}
        {status && (
          <div className="empty">
            <span className="spinner" />
            <p style={{ marginTop: 12 }}>{status}</p>
          </div>
        )}
        {error && <p className="draft-warn error">{error}</p>}
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
              placeholder={relay ? `Message ${who} (optional)` : "What should it do?"}
              aria-label={relay ? `First message to ${who}, optional` : "What should it do?"}
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
      {pickingStarter && pickable.length > 0 && <ProjectPickerSheet starters={pickable} current={starter?.resource.id ?? null} onSelect={chooseStarter} onClose={() => setPickingStarter(false)} />}
      {picking && choices && <DirectorySheet choices={choices} current={dir} who={relay ? who : null} host={host} onChoose={choose} onClose={() => setPicking(false)} />}
      {settingsOpen && starter && <ResourceSettingsSheet resourceId={starter.resource.id} onClose={() => setSettingsOpen(false)} />}
      {optionsOpen && form && <StartOptionsSheet form={form} onReset={() => form.reset(form.items.map((i) => i.key))} onClose={() => setOptionsOpen(false)} />}
    </div>
  );
}

function DirectorySheet({ choices, current, who, host, onChoose, onClose }: { choices: ReturnType<typeof directoryChoices>; current: string; who: string | null; host: string; onChoose: (p: string) => void; onClose: () => void }) {
  const [custom, setCustom] = useState("");
  // An absolute path, or ~/… (read as the home folder on the project's machine).
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
        <p style={{ marginBottom: 10 }}>{who ? `${cap(host)} starts ${who} in this folder; the session\u2019s settings and logs stay with the project.` : "The session\u2019s first commands run here; its settings and logs stay with the project."}</p>
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

/** The inputs the agent declares for starting a session, as a form: each
 * left alone comes from the project (or from the configuration chosen). */
function StartOptionsSheet({ form, onReset, onClose }: { form: FormModel; onReset: () => void; onClose: () => void }) {
  const any = form.items.some((i) => form.state(i.key).set);
  // The first choice (the configuration) stays in view while the rest scroll.
  const [first] = formSummary(form);
  return (
    <Sheet onClose={onClose} label="Session options" className="start-sheet">
      <div className="start-sheet-head">
        <h2>This session</h2>
        {first && first.text ? (
          <p className={`start-sheet-status${first.changed ? " changed" : ""}`} aria-live="polite">
            {first.title}: <strong>{first.text}</strong>
          </p>
        ) : null}
        <p>This device keeps your choices for the next new session here. Anything you leave alone keeps its default.</p>
      </div>
      <div className="start-sheet-body">
        <SettingsForm model={form} />
      </div>
      <div className="start-sheet-foot">
        <button type="button" className="btn" disabled={!any} onClick={onReset}>
          Reset all
        </button>
        <button type="button" className="btn primary" onClick={onClose}>
          Done
        </button>
      </div>
    </Sheet>
  );
}
