import { useEffect, useState } from "react";

let clockOffset = 0;

/** Records the server's idea of "now" so expiries are judged on its clock, not the phone's. */
export function syncServerTime(iso: string | undefined) {
  if (!iso) return;
  const t = new Date(iso).getTime();
  if (Number.isFinite(t)) clockOffset = t - Date.now();
}

/** Milliseconds since the epoch on the server's clock. */
export function serverNow(): number {
  return Date.now() + clockOffset;
}

// ---------------------------------------------------------------------------
// One shared ticker per cadence. Relative times, countdowns and elapsed
// timers all read from it, so a screen full of rows costs one interval, and
// nothing ticks while the app is hidden.

interface Ticker {
  subs: Set<() => void>;
  timer: number | undefined;
  interval: number;
}

const tickers = new Map<number, Ticker>();

function startTicker(t: Ticker) {
  if (t.timer !== undefined || t.subs.size === 0) return;
  if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
  t.timer = window.setInterval(() => t.subs.forEach((fn) => fn()), t.interval);
}

function stopTicker(t: Ticker) {
  if (t.timer === undefined) return;
  window.clearInterval(t.timer);
  t.timer = undefined;
}

if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    for (const t of tickers.values()) {
      if (document.visibilityState === "hidden") stopTicker(t);
      else {
        t.subs.forEach((fn) => fn());
        startTicker(t);
      }
    }
  });
}

/** The current server time, re-rendering every `intervalMs` while mounted and visible. */
export function useNow(intervalMs = 30000): number {
  const [now, setNow] = useState(() => serverNow());
  useEffect(() => {
    let t = tickers.get(intervalMs);
    if (!t) {
      t = { subs: new Set(), timer: undefined, interval: intervalMs };
      tickers.set(intervalMs, t);
    }
    const ticker = t;
    const fn = () => setNow(serverNow());
    ticker.subs.add(fn);
    fn();
    startTicker(ticker);
    return () => {
      ticker.subs.delete(fn);
      if (ticker.subs.size === 0) stopTicker(ticker);
    };
  }, [intervalMs]);
  return now;
}

// ---------------------------------------------------------------------------

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

/** Long form for places with room: "12 minutes ago", "yesterday". */
export function relativeTime(iso: string, now = serverNow()): string {
  const then = new Date(iso).getTime();
  const diff = Math.round((then - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 45) return "now";
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  if (abs < 7 * 86400) return rtf.format(Math.round(diff / 86400), "day");
  const d = new Date(iso);
  const sameYear = d.getFullYear() === new Date(now).getFullYear();
  return d.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { year: "numeric", month: "short", day: "numeric" });
}

/** Compact form for list rows: "now", "4m", "2h", "Tue", "Mar 3", "3/24/25". */
export function compactTime(iso: string, now = serverNow()): string {
  const then = new Date(iso).getTime();
  const secs = Math.max(0, Math.round((now - then) / 1000));
  if (secs < 45) return "now";
  if (secs < 3600) return `${Math.max(1, Math.round(secs / 60))}m`;
  if (secs < 86400) return `${Math.round(secs / 3600)}h`;
  const d = new Date(iso);
  if (secs < 7 * 86400) return d.toLocaleDateString(undefined, { weekday: "short" });
  const sameYear = d.getFullYear() === new Date(now).getFullYear();
  return d.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { year: "2-digit", month: "numeric", day: "numeric" });
}

/** Time remaining as "12:30", "1h 05m" or "3d". Negative values read as "0:00". */
export function countdown(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 3600) return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

export function shortTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

export function dayLabel(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const startOfThat = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((startOfToday - startOfThat) / 86400000);
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  if (days < 7) return d.toLocaleDateString(undefined, { weekday: "long" });
  return d.toLocaleDateString(undefined, { weekday: "short", month: "long", day: "numeric" });
}

export function sameDay(a: string, b: string): boolean {
  const da = new Date(a);
  const db = new Date(b);
  return da.getFullYear() === db.getFullYear() && da.getMonth() === db.getMonth() && da.getDate() === db.getDate();
}

export function fullDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
