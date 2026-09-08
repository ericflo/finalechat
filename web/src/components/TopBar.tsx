import { useEffect, useRef, useState, type ReactNode } from "react";
import { navigate } from "../lib/router";
import { retryConnection, useStore } from "../lib/store";
import { IconBack } from "./Icons";

export function TopBar({ title, subtitle, backTo, right, big, below }: { title: ReactNode; subtitle?: ReactNode; backTo?: string; right?: ReactNode; big?: boolean; below?: ReactNode }) {
  const showBack = !!backTo;
  const ref = useRef<HTMLElement>(null);
  // Anything that scrolls into view (the "New" divider, a highlighted
  // question) must clear the sticky header, whatever it contains.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const apply = () => document.documentElement.style.setProperty("--topbar-h", `${el.offsetHeight}px`);
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return (
    <header ref={ref} className="topbar">
      <div className={`topbar-inner ${big ? "topbar-big" : ""}`}>
        {showBack && (
          <button type="button" className="icon-btn" aria-label="Back" title={backTo === "/settings" ? "Settings" : "Inbox"} onClick={() => navigate(backTo || "/")}>
            <IconBack />
          </button>
        )}
        <div className="topbar-title" style={{ paddingLeft: showBack ? 0 : 6 }}>
          <h1>{title}</h1>
          {subtitle && <div className="sub">{subtitle}</div>}
        </div>
        {right}
      </div>
      {below}
      <ConnectionStrip />
    </header>
  );
}

/** Shown on every screen: the live connection is the product, so its loss is never silent. */
function ConnectionStrip() {
  const connection = useStore((s) => s.connection);
  const user = useStore((s) => s.user);
  const [online, setOnline] = useState(() => (typeof navigator === "undefined" ? true : navigator.onLine));
  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);
  if (!user || connection !== "offline") return null;
  return (
    <button type="button" className="status-strip" onClick={retryConnection}>
      {online ? "Reconnecting…" : "No connection"} · tap to retry
    </button>
  );
}
