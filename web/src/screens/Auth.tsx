import { useState, type FormEvent } from "react";
import { Link } from "../components/Common";
import { navigate } from "../lib/router";
import { register, signIn, useStore } from "../lib/store";
import { resyncPushSubscription } from "../lib/push";

export function AuthScreen({ mode }: { mode: "login" | "register" }) {
  const signup = useStore((s) => s.signup);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [invite, setInvite] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const registering = mode === "register";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      if (registering) {
        await register({ email, password, display_name: name, invite_code: invite || undefined });
      } else {
        await signIn(email, password);
      }
      void resyncPushSubscription();
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="centered">
      <div className="card auth-card">
        <div className="brand">
          <img src="/icons/icon-192.png" alt="" width={44} height={44} />
          <div>
            <h1>Finalechat</h1>
            <div className="tagline">Your agents message you.</div>
          </div>
        </div>
        {registering && signup === "closed" ? (
          <>
            <p style={{ marginBottom: 14 }}>This Finalechat is private. Sign in with your account.</p>
            <Link href="/login" className="btn primary block">
              Sign in
            </Link>
          </>
        ) : (
          <form onSubmit={submit}>
            {registering && signup === "open" && <div className="callout accent" style={{ marginBottom: 14 }}>You are creating the first account. Registration closes after this.</div>}
            {error && <div className="form-error">{error}</div>}
            <div className="field">
              <label htmlFor="email">Email</label>
              <input id="email" type="email" autoComplete="email" inputMode="email" required value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
            </div>
            <div className="field">
              <label htmlFor="password">Password</label>
              <input id="password" type="password" autoComplete={registering ? "new-password" : "current-password"} required minLength={registering ? 10 : undefined} value={password} onChange={(e) => setPassword(e.target.value)} />
              {registering && <div className="help">At least 10 characters.</div>}
            </div>
            {registering && (
              <div className="field">
                <label htmlFor="name">Your name (optional)</label>
                <input id="name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
              </div>
            )}
            {registering && signup === "invite" && (
              <div className="field">
                <label htmlFor="invite">Invite code</label>
                <input id="invite" required value={invite} onChange={(e) => setInvite(e.target.value)} />
              </div>
            )}
            <button type="submit" className="btn primary block" disabled={busy}>
              {busy ? "One moment…" : registering ? "Create account" : "Sign in"}
            </button>
            <div className="auth-foot">
              {registering ? (
                <>
                  Already have an account? <Link href="/login">Sign in</Link>
                </>
              ) : signup !== "closed" ? (
                <>
                  New here? <Link href="/register">Create an account</Link>
                </>
              ) : (
                <span>
                  Agents read <a href="/AGENTS.md">/AGENTS.md</a>
                </span>
              )}
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
