import { useEffect, useState } from "react";
import { serverNow } from "./time";
import type { Activity, ActivityKind } from "./types";

/**
 * Returns the agent's status while it is live and re-renders the moment it
 * lapses, so a status can never linger past its expiry even if no event
 * arrives to replace it. Expiry is judged against the server's clock.
 */
export function useLiveActivity(activity: Activity | null | undefined): Activity | null {
  const expires = activity ? new Date(activity.expires_at).getTime() : 0;
  const [, bump] = useState(0);
  useEffect(() => {
    if (!activity) return;
    const remaining = expires - serverNow();
    if (remaining <= 0) return;
    const t = window.setTimeout(() => bump((n) => n + 1), remaining + 30);
    return () => window.clearTimeout(t);
  }, [activity, expires]);
  if (!activity || expires <= serverNow()) return null;
  return activity;
}

/** Whole seconds since `iso`, ticking once a second while mounted. */
export function useElapsed(iso: string | null): number {
  const start = iso ? new Date(iso).getTime() : 0;
  const [now, setNow] = useState(() => serverNow());
  useEffect(() => {
    if (!iso) return;
    setNow(serverNow());
    const t = window.setInterval(() => setNow(serverNow()), 1000);
    return () => window.clearInterval(t);
  }, [iso]);
  if (!iso) return 0;
  return Math.max(0, Math.floor((now - start) / 1000));
}

export function formatElapsed(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ${String(seconds % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${String(m % 60).padStart(2, "0")}m`;
}

export function activityLabel(kind: ActivityKind): string {
  switch (kind) {
    case "thinking":
      return "Thinking";
    case "typing":
      return "Typing";
    case "waiting":
      return "Waiting";
    case "tool":
      return "Working";
    default:
      return "Working";
  }
}
