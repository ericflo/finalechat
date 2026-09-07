import { useEffect, useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { ConfirmSheet, CopyButton, Link, Sheet, Snippet, Toggle } from "../components/Common";
import { IconBell, IconBook, IconChevron, IconLogout, IconPhone, IconPlus, IconTerminal, IconTrash } from "../components/Icons";
import { TopBar } from "../components/TopBar";
import { currentPushEndpoint, getPushState, isIOS, isStandalone, subscribeToPush, unsubscribeFromPush, type PushState } from "../lib/push";
import { navigate } from "../lib/router";
import { setUser, signOut, toast, updateSettings, useStore } from "../lib/store";
import { relativeTime } from "../lib/time";
import type { APIToken, PushSubscriptionInfo } from "../lib/types";

export function SettingsScreen() {
  const user = useStore((s) => s.user);
  const settings = useStore((s) => s.settings);
  const version = useStore((s) => s.version);
  const pushEnabled = useStore((s) => s.pushEnabled);
  const [push, setPush] = useState<PushState | null>(null);

  useEffect(() => {
    void getPushState().then(setPush);
  }, []);

  if (!user) return null;

  const enable = async () => {
    try {
      const st = await subscribeToPush();
      setPush(st);
      if (st === "subscribed") toast("Notifications on", "success");
      else if (st === "denied") toast("Notifications are blocked for this site. Allow them in your browser or system settings.", "error");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not enable notifications", "error");
    }
  };
  const disable = async () => {
    setPush(await unsubscribeFromPush());
    toast("Notifications off on this device");
  };
  const test = async () => {
    try {
      const r = await api.testPush();
      toast(`Test sent to ${r.devices} device${r.devices === 1 ? "" : "s"}`, "success");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not send a test", "error");
    }
  };

  const pushHint = !pushEnabled
    ? "This server has no push keys configured."
    : push === "unsupported"
      ? isIOS() && !isStandalone()
        ? "On iPhone, add Finalechat to your Home Screen (Share → Add to Home Screen) and open it from there to enable notifications."
        : "This browser does not support Web Push."
      : push === "insecure"
        ? "Notifications need HTTPS."
        : push === "denied"
          ? "Blocked for this site. Allow notifications in your browser or system settings, then try again."
          : push === "subscribed"
            ? "This device will be notified for questions and important messages."
            : "Questions and important messages will buzz this device.";

  return (
    <div className="page">
      <TopBar title="Settings" backTo="/" />
      <div className="page-body">
        <div className="section-title">Notifications</div>
        <div className="card settings-list">
          <div className="setting">
            <div>
              <div className="label" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <IconBell style={{ width: 18, height: 18, color: "var(--text-3)" }} /> Push on this device
              </div>
              <div className="desc">{pushHint}</div>
            </div>
            <div>
              {push === "subscribed" ? (
                <Toggle on onChange={disable} label="Push notifications" />
              ) : (
                <Toggle on={false} disabled={!pushEnabled || push === "unsupported" || push === "denied" || push === "insecure" || push === null} onChange={enable} label="Push notifications" />
              )}
            </div>
          </div>
          {push === "subscribed" && (
            <div className="setting">
              <div>
                <div className="label">Send a test</div>
                <div className="desc">Check that notifications reach this device.</div>
              </div>
              <button type="button" className="btn small" onClick={test}>
                Test
              </button>
            </div>
          )}
          <div className="setting">
            <div>
              <div className="label">Notify for every message</div>
              <div className="desc">Off: only questions and messages the agent marks important. On: every agent message.</div>
            </div>
            <Toggle on={settings.notify_all_messages} onChange={(v) => updateSettings({ notify_all_messages: v }).catch((e) => toast(e.message, "error"))} label="Notify for every message" />
          </div>
          <div className="setting">
            <div>
              <div className="label">Remote mode</div>
              <div className="desc">When you step away from the terminal: Claude Code waits for your replies here and uses your phone answers to its questions.</div>
            </div>
            <Toggle on={settings.remote_mode} onChange={(v) => updateSettings({ remote_mode: v }).catch((e) => toast(e.message, "error"))} label="Remote mode" />
          </div>
        </div>

        {pushEnabled && <DevicesCard push={push} />}

        <div className="section-title">Agents</div>
        <div className="card settings-list">
          <Link href="/settings/agents" className="setting link">
            <div>
              <div className="label" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <IconTerminal style={{ width: 18, height: 18, color: "var(--text-3)" }} /> Connect an agent
              </div>
              <div className="desc">API tokens, the CLI, Claude Code hooks and MCP.</div>
            </div>
            <IconChevron className="chev" />
          </Link>
          <a href="/api/" className="setting link">
            <div>
              <div className="label" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <IconBook style={{ width: 18, height: 18, color: "var(--text-3)" }} /> API reference
              </div>
              <div className="desc">Everything an agent needs is also at /AGENTS.md.</div>
            </div>
            <IconChevron className="chev" />
          </a>
        </div>

        <div className="section-title">Account</div>
        <AccountCard />

        <div className="section-title">About</div>
        <div className="card settings-list">
          <div className="setting">
            <div>
              <div className="label">Finalechat</div>
              <div className="desc">
                Version {version || "dev"} · Signed in as {user.email}
              </div>
            </div>
          </div>
          <button
            type="button"
            className="setting link"
            onClick={() =>
              signOut().then((ok) => ok && navigate("/login", { replace: true }))
            }
          >
            <div>
              <div className="label" style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--red)" }}>
                <IconLogout style={{ width: 18, height: 18 }} /> Sign out
              </div>
            </div>
          </button>
        </div>
      </div>
    </div>
  );
}

/** Every device registered for notifications, with its delivery health. */
function DevicesCard({ push }: { push: PushState | null }) {
  const [devices, setDevices] = useState<PushSubscriptionInfo[] | null>(null);
  const [mine, setMine] = useState<string | null>(null);
  useEffect(() => {
    api
      .listPushSubscriptions()
      .then((r) => setDevices(r.subscriptions))
      .catch(() => setDevices([]));
    void currentPushEndpoint().then(setMine);
  }, [push]);
  if (devices === null || devices.length === 0) return null;
  const remove = async (d: PushSubscriptionInfo) => {
    try {
      await api.unsubscribePush(d.endpoint);
      setDevices((cur) => (cur ?? []).filter((x) => x.id !== d.id));
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not remove", "error");
    }
  };
  return (
    <>
      <div className="section-title">Devices</div>
      <div className="card settings-list">
        {devices.map((d) => {
          const failing = d.failure_count > 0;
          return (
            <div key={d.id} className="device-row">
              <div>
                <div className="name">
                  <IconPhone style={{ width: 16, height: 16, color: "var(--text-3)" }} />
                  {describeDevice(d.user_agent)}
                  {d.endpoint === mine && <span className="pill accent">This device</span>}
                </div>
                <div className={`meta ${failing ? "bad" : ""}`}>
                  {failing
                    ? `${d.failure_count} failed deliver${d.failure_count === 1 ? "y" : "ies"} since the last success`
                    : d.last_success_at
                      ? `Last delivered ${relativeTime(d.last_success_at)}`
                      : `Registered ${relativeTime(d.created_at)} · nothing delivered yet`}
                </div>
              </div>
              <button type="button" className="icon-btn" aria-label="Remove device" onClick={() => remove(d)}>
                <IconTrash />
              </button>
            </div>
          );
        })}
      </div>
    </>
  );
}

function describeDevice(ua: string): string {
  if (/iPhone/.test(ua)) return "iPhone";
  if (/iPad/.test(ua)) return "iPad";
  if (/Android/.test(ua)) return "Android";
  if (/Macintosh/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows";
  if (/Linux/.test(ua)) return "Linux";
  return "Device";
}

function AccountCard() {
  const user = useStore((s) => s.user);
  const [name, setName] = useState(user ? user.display_name : "");
  const [changing, setChanging] = useState(false);
  if (!user) return null;

  const saveName = async () => {
    if (name.trim() === user.display_name) return;
    try {
      const r = await api.updateMe({ display_name: name.trim() });
      setUser(r.user);
      toast("Saved", "success");
    } catch (e) {
      toast(e instanceof Error ? e.message : "Could not save", "error");
    }
  };

  return (
    <div className="card settings-list">
      <div className="setting stack">
        <div className="field" style={{ marginBottom: 0 }}>
          <label htmlFor="display-name">Your name</label>
          <input id="display-name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} onBlur={saveName} onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()} />
        </div>
      </div>
      <button type="button" className="setting link" onClick={() => setChanging(true)}>
        <div>
          <div className="label">Change password</div>
        </div>
        <IconChevron className="chev" />
      </button>
      {changing && <PasswordSheet onClose={() => setChanging(false)} />}
    </div>
  );
}

function PasswordSheet({ onClose }: { onClose: () => void }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.updateMe({ current_password: current, new_password: next });
      toast("Password changed", "success");
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not change password");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet onClose={onClose}>
      <form onSubmit={submit} style={{ padding: "4px 6px" }}>
        <h2 style={{ fontSize: 18, marginBottom: 12 }}>Change password</h2>
        {error && <div className="form-error">{error}</div>}
        <div className="field">
          <label htmlFor="pw-current">Current password</label>
          <input id="pw-current" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="pw-next">New password</label>
          <input id="pw-next" type="password" autoComplete="new-password" required minLength={10} value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={busy}>
            Save
          </button>
        </div>
      </form>
    </Sheet>
  );
}

/** Settings → Agents: tokens plus copy-paste onboarding for every integration. */
export function AgentsScreen() {
  const [tokens, setTokens] = useState<APIToken[] | null>(null);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [secret, setSecret] = useState<{ token: APIToken; secret: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [revoking, setRevoking] = useState<APIToken | null>(null);
  const origin = window.location.origin;

  const refresh = () =>
    api
      .listTokens()
      .then((r) => setTokens(r.tokens))
      .catch((e) => toast(e.message, "error"));
  useEffect(() => {
    void refresh();
  }, []);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.createToken(name.trim() || defaultTokenName());
      setSecret(r);
      setCreating(false);
      setName("");
      void refresh();
    } catch (err) {
      toast(err instanceof Error ? err.message : "Could not create token", "error");
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (t: APIToken) => {
    try {
      await api.revokeToken(t.id);
      if (secret?.token.id === t.id) setSecret(null);
      void refresh();
      toast("Token revoked");
    } catch (err) {
      toast(err instanceof Error ? err.message : "Could not revoke", "error");
    }
  };

  const tokenValue = secret?.secret ?? "fc_YOUR_TOKEN";

  return (
    <div className="page">
      <TopBar title="Connect an agent" backTo="/settings" />
      <div className="page-body">
        {secret ? (
          <div className="card" style={{ padding: 16 }}>
            <h2 style={{ fontSize: 17 }}>Your new token</h2>
            <p style={{ color: "var(--text-2)", fontSize: 14, margin: "4px 0 10px" }}>
              Copy it now. For your safety it is shown only once.
            </p>
            <div className="token-secret">
              {secret.secret}
              <CopyButton text={secret.secret} />
            </div>
            <div className="callout accent" style={{ marginTop: 12 }}>
              The snippets below already include this token. Paste one into the machine where your agents run.
            </div>
          </div>
        ) : (
          <div className="callout accent">
            Create a token, then paste one of the snippets below into the machine where your agents run. Every snippet fills the token in for you.
          </div>
        )}

        <div className="section-title">Tokens</div>
        <div className="card settings-list">
          {tokens === null ? (
            <div className="setting">
              <div className="skeleton" style={{ height: 20, width: 160 }} />
            </div>
          ) : tokens.length === 0 ? (
            <div className="setting">
              <div className="desc">No tokens yet.</div>
            </div>
          ) : (
            tokens.map((t) => (
              <div key={t.id} className="token-row">
                <div>
                  <div className="name">{t.name}</div>
                  <div className="meta">
                    {t.prefix}… · {t.last_used_at ? `used ${relativeTime(t.last_used_at)}` : "never used"}
                  </div>
                </div>
                <button type="button" className="icon-btn" aria-label={`Revoke ${t.name}`} onClick={() => setRevoking(t)}>
                  <IconTrash />
                </button>
              </div>
            ))
          )}
          {creating ? (
            <form onSubmit={create} className="setting stack">
              <div className="field" style={{ marginBottom: 8 }}>
                <label htmlFor="token-name">Token name</label>
                <input id="token-name" autoFocus placeholder={defaultTokenName()} value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
                <div className="help">Name it after the machine or agent, e.g. “laptop” or “build server”.</div>
              </div>
              <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
                <button type="button" className="btn small" onClick={() => setCreating(false)}>
                  Cancel
                </button>
                <button type="submit" className="btn primary small" disabled={busy}>
                  Create token
                </button>
              </div>
            </form>
          ) : (
            <button type="button" className="setting link" onClick={() => setCreating(true)}>
              <div className="label" style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--accent)" }}>
                <IconPlus style={{ width: 18, height: 18 }} /> New token
              </div>
            </button>
          )}
        </div>

        {revoking && (
          <ConfirmSheet
            title={`Revoke "${revoking.name}"?`}
            body="Agents using this token stop working immediately. Create a new token to reconnect them."
            confirmLabel="Revoke token"
            danger
            onClose={() => setRevoking(null)}
            onConfirm={() => void revoke(revoking)}
          />
        )}

        <div className="section-title">One-line setup</div>
        <div className="card" style={{ padding: "6px 16px 16px" }}>
          <Snippet title="Install the CLI and sign in (any agent, any harness)" code={`export FINALECHAT_TOKEN=${tokenValue}\ncurl -fsSL ${origin}/install.sh | sh`} />
          {secret && (
            <p style={{ fontSize: 12.5, color: "var(--text-3)", marginTop: 6 }}>
              The token lands in your shell history with this line; on a shared machine run <code>finalechat login</code> and paste it at the prompt instead.
            </p>
          )}
          <Snippet title="Then wire up Claude Code (hooks + MCP)" code={`finalechat install claude-code`} />
          <p style={{ fontSize: 13.5, color: "var(--text-2)", marginTop: 10, lineHeight: 1.45 }}>
            After that, every Claude Code session gets its own thread here: its replies, your prompts, and its questions. Turn on <strong>Remote mode</strong> when you leave the desk and Claude will wait for your answers from this app.
          </p>
        </div>

        <div className="section-title">Manual</div>
        <div className="card" style={{ padding: "6px 16px 16px" }}>
          <Snippet title="Environment for any agent" code={`export FINALECHAT_TOKEN=${tokenValue}\nexport FINALECHAT_URL=${origin}`} />
          <Snippet title="Post a message with curl" code={`curl -sS ${origin}/api/v1/threads/ext:my-session/messages \\\n  -H "Authorization: Bearer $FINALECHAT_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '{"title":"My project","agent":"My agent","body":"Hello from the terminal"}'`} />
          <Snippet title="Ask a question and wait for the answer" code={`curl -sS "${origin}/api/v1/threads/ext:my-session/questions?wait=600" \\\n  -H "Authorization: Bearer $FINALECHAT_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '{"prompt":"Deploy to production?","options":[{"label":"Yes"},{"label":"Not yet"}]}'`} />
          <p style={{ fontSize: 13.5, color: "var(--text-2)", marginTop: 12 }}>
            Agents can read the full guide at <a href="/AGENTS.md">{origin}/AGENTS.md</a> and the reference at <a href="/api/">{origin}/api/</a>.
          </p>
        </div>
      </div>
    </div>
  );
}

function defaultTokenName(): string {
  const ua = navigator.userAgent;
  if (/Mac/.test(ua) && !/iPhone|iPad/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows PC";
  if (/Linux/.test(ua) && !/Android/.test(ua)) return "Linux box";
  return "Agent token";
}
