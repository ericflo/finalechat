import { useEffect, useState, type MouseEvent } from "react";

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
  else window.history.pushState(null, "", to);
  listeners.forEach((l) => l());
}

export function back(fallback = "/") {
  if (window.history.length > 1 && document.referrer !== "" ? true : window.history.state !== null) {
    window.history.back();
  } else {
    navigate(fallback, { replace: true });
  }
}

export function useRoute(): Route {
  const [route, setRoute] = useState(current);
  useEffect(() => {
    const update = () => setRoute(current());
    listeners.add(update);
    window.addEventListener("popstate", update);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", update);
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
    if (seg.startsWith(":")) params[seg.slice(1)] = decodeURIComponent(val);
    else if (seg !== val) return null;
  }
  return params;
}
