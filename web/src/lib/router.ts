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

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (opts.replace) window.history.replaceState(null, "", to);
  else {
    window.history.pushState(null, "", to);
    depth++;
  }
  notify();
}

/** Swaps screens inside a view transition where the browser has them, so a
 * tap on a thread does not replace the whole viewport in one frame. */
function notify() {
  type Transition = { ready: Promise<void>; finished: Promise<void>; updateCallbackDone: Promise<void> };
  const doc = document as Document & { startViewTransition?: (fn: () => void) => Transition };
  if (typeof doc.startViewTransition === "function" && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    // A second navigation skips the first transition; that rejection is
    // routine, not an error.
    const t = doc.startViewTransition(() => flushSync(() => listeners.forEach((l) => l())));
    const quiet = () => {};
    t.ready.catch(quiet);
    t.finished.catch(quiet);
    t.updateCallbackDone.catch(quiet);
  } else {
    listeners.forEach((l) => l());
  }
}

let depth = 0;

export function back(fallback = "/") {
  // Only walk history we created; a deep link opened from a notification has
  // nowhere sensible to go back to except the inbox.
  if (depth > 0) {
    depth--;
    window.history.back();
  } else {
    navigate(fallback, { replace: true });
  }
}

export function useRoute(): Route {
  const [route, setRoute] = useState(current);
  useEffect(() => {
    const update = () => setRoute(current());
    const onPop = () => {
      if (depth > 0) depth--;
      update();
    };
    listeners.add(update);
    window.addEventListener("popstate", onPop);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", onPop);
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
