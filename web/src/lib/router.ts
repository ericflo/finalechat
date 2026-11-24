import { useEffect, useState, type MouseEvent } from "react";
import { flushSync } from "react-dom";

export interface Route {
  path: string;
  search: URLSearchParams;
}

const listeners = new Set<() => void>();

function current(): Route {
  return { path: window.location.pathname, search: new URLSearchParams(window.location.search) };
}

/* History works like an app's navigation stack. Each entry this app pushes
   carries a key (its scroll position is remembered under it) and the trail
   of entries before it, so Back can pop to the screen you came from, at the
   place you left it, instead of pushing a fresh copy of that screen. */

interface NavState {
  key: string;
  trail: string[];
}

const maxTrail = 50;

/** Where a screen was left: its offset, and the first visible element that
 * marks itself with data-scroll-anchor (an inbox row) and how far down the
 * screen it sat. Coming back puts that element where it was, so a card
 * that loads late above the list, or rows that moved, do not shift what
 * you were looking at. */
interface Spot {
  y: number;
  anchor?: string;
  offset?: number;
}
const positions = new Map<string, Spot>();

function navState(): NavState | null {
  const s = window.history.state as Partial<NavState> | null;
  return s && typeof s.key === "string" && Array.isArray(s.trail) ? (s as NavState) : null;
}

function newKey(): string {
  return Math.random().toString(36).slice(2, 10);
}

const pathOf = (to: string) => new URL(to, window.location.href).pathname;
const here = () => window.location.pathname + window.location.search;
// A thread places itself: at the "New" divider, a linked message, or the end.
const placesItself = (path: string) => /^\/t\/[^/]+$/.test(path);

if ("scrollRestoration" in window.history) window.history.scrollRestoration = "manual";
if (!navState()) window.history.replaceState({ key: newKey(), trail: [] } satisfies NavState, "");
let currentKey = (navState() as NavState).key;

/** The page's scroll position, also while a sheet pins the body. */
function scrollPosition(): number {
  const body = document.body.style;
  return body.position === "fixed" ? Math.max(0, -parseFloat(body.top || "0") || 0) : window.scrollY;
}

/** `leaving` is where the reader is going: that thread's own row is not an
 * anchor, since new activity there is what moves it up the list. */
function remember(leaving?: string) {
  const spot: Spot = { y: scrollPosition() };
  const header = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--topbar-h")) || 0;
  const skip = leaving ? pathOf(leaving) : null;
  for (const el of document.querySelectorAll<HTMLElement>("[data-scroll-anchor]")) {
    const r = el.getBoundingClientRect();
    if (r.bottom <= header + 1) continue;
    if (r.top >= window.innerHeight) break;
    const key = el.dataset.scrollAnchor as string;
    if (pathOf(key) === skip) continue;
    spot.anchor = key;
    spot.offset = r.top;
    break;
  }
  positions.set(currentKey, spot);
}

// Restoring holds the spot for a moment while late content settles (a card
// loading above the list, which Safari does not compensate for), but never
// fights the reader: any touch, wheel or key ends it.
let cancelRestore: (() => void) | null = null;

function restore(spot: Spot) {
  cancelRestore?.();
  const target = () => {
    if (spot.anchor !== undefined) {
      const el = document.querySelector(`[data-scroll-anchor="${CSS.escape(spot.anchor)}"]`);
      if (el) return window.scrollY + el.getBoundingClientRect().top - (spot.offset ?? 0);
    }
    return spot.y;
  };
  const apply = () => {
    const y = Math.max(0, Math.round(target()));
    if (Math.abs(window.scrollY - y) > 1) window.scrollTo(0, y);
  };
  apply();
  const until = performance.now() + 1000;
  let raf = 0;
  const events = ["touchstart", "wheel", "keydown", "pointerdown"] as const;
  const stop = () => {
    cancelAnimationFrame(raf);
    events.forEach((e) => window.removeEventListener(e, stop, true));
    cancelRestore = null;
  };
  const tick = () => {
    apply();
    if (performance.now() > until) stop();
    else raf = requestAnimationFrame(tick);
  };
  events.forEach((e) => window.addEventListener(e, stop, { capture: true, passive: true }));
  cancelRestore = stop;
  raf = requestAnimationFrame(tick);
}

export function navigate(to: string, opts: { replace?: boolean; transition?: boolean } = {}) {
  cancelRestore?.();
  if (!opts.replace) remember(to);
  const trail = navState()?.trail ?? [];
  const moved = pathOf(to) !== window.location.pathname;
  const next: NavState = opts.replace ? { key: newKey(), trail } : { key: newKey(), trail: [...trail, here()].slice(-maxTrail) };
  if (opts.replace) window.history.replaceState(next, "", to);
  else window.history.pushState(next, "", to);
  currentKey = next.key;
  if (opts.transition === false) {
    listeners.forEach((l) => l());
    return;
  }
  // A new screen starts at the top; a change within one (a filter, a
  // panel) leaves the reader where they are.
  const top = () => {
    if (moved && !placesItself(window.location.pathname)) window.scrollTo(0, 0);
  };
  notify(() => {
    if (moved) window.scrollTo(0, 0);
  }, top);
}

// Set while Back pops several entries: if the trail was stale and the pop
// lands somewhere else, go to the intended screen instead.
let backTarget: string | null = null;

/** The header's Back: pop to the nearest earlier entry on `to`'s screen
 * (where you came from, scrolled where you left it), or replace this entry
 * with `to` when there is none (a thread opened from a notification). */
export function goBack(to: string) {
  const trail = navState()?.trail ?? [];
  const want = pathOf(to);
  for (let i = trail.length - 1; i >= 0; i--) {
    if (pathOf(trail[i] as string) === want) {
      backTarget = to;
      window.history.go(i - trail.length);
      return;
    }
  }
  navigate(to, { replace: true });
}

window.addEventListener("popstate", () => {
  cancelRestore?.();
  // The location is already the destination; the page is still the one left.
  remember(window.location.href);
  const target = backTarget;
  backTarget = null;
  if (target && pathOf(target) !== window.location.pathname) {
    navigate(target, { replace: true });
    return;
  }
  let s = navState();
  if (!s) {
    s = { key: newKey(), trail: [] };
    window.history.replaceState(s, "");
  }
  currentKey = s.key;
  const spot = positions.get(s.key) ?? { y: 0 };
  notify(undefined, () => {
    if (!placesItself(window.location.pathname)) restore(spot);
  });
});

/** Swaps screens inside a view transition where the browser has them, so a
 * tap on a thread does not replace the whole viewport in one frame. The
 * scroll hooks run on either side of the swap, inside the transition, so
 * neither the old nor the new screen is seen at the wrong position. */
function notify(before?: () => void, after?: () => void) {
  const swap = () => {
    before?.();
    flushSync(() => listeners.forEach((l) => l()));
    after?.();
  };
  type Transition = { ready: Promise<void>; finished: Promise<void>; updateCallbackDone: Promise<void> };
  const doc = document as Document & { startViewTransition?: (fn: () => void) => Transition };
  if (typeof doc.startViewTransition === "function" && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    // A second navigation skips the first transition; that rejection is
    // routine, not an error.
    const t = doc.startViewTransition(swap);
    const quiet = () => {};
    t.ready.catch(quiet);
    t.finished.catch(quiet);
    t.updateCallbackDone.catch(quiet);
  } else {
    // Out of the caller's stack: a navigation can start during a render
    // (the post-login redirect), where React cannot flush synchronously.
    queueMicrotask(swap);
  }
}

export function useRoute(): Route {
  const [route, setRoute] = useState(current);
  useEffect(() => {
    const update = () => setRoute(current());
    listeners.add(update);
    return () => {
      listeners.delete(update);
    };
  }, []);
  return route;
}

export function onLinkClick(e: MouseEvent<HTMLAnchorElement>) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const href = e.currentTarget.getAttribute("href");
  if (!href || !href.startsWith("/") || e.currentTarget.target === "_blank") return;
  e.preventDefault();
  navigate(href);
}

/** Matches `/t/:id` style patterns. */
export function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split("/").filter(Boolean);
  const s = path.split("/").filter(Boolean);
  if (p.length !== s.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    const seg = p[i] as string;
    const val = s[i] as string;
    if (seg.startsWith(":")) {
      try {
        params[seg.slice(1)] = decodeURIComponent(val);
      } catch {
        params[seg.slice(1)] = val;
      }
    }
    else if (seg !== val) return null;
  }
  return params;
}
