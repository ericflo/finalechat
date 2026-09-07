import { useEffect, useState, type ReactNode } from "react";
import { onLinkClick } from "../lib/router";
import { dismissToast, useStore } from "../lib/store";
import { IconCheck, IconCopy } from "./Icons";

export function Link({ href, children, className, onClick, ...rest }: { href: string; children: ReactNode; className?: string; onClick?: () => void } & Record<string, unknown>) {
  return (
    <a
      href={href}
      className={className}
      onClick={(e) => {
        onClick?.();
        onLinkClick(e);
      }}
      {...rest}
    >
      {children}
    </a>
  );
}

export function ToastHost() {
  const t = useStore((s) => s.toast);
  if (!t) return null;
  return (
    <div className="toast-host" role="status" aria-live="polite">
      <div className={`toast ${t.kind}`} onClick={dismissToast}>
        {t.text}
      </div>
    </div>
  );
}

export function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button type="button" role="switch" aria-checked={on} aria-label={label} className={`toggle ${on ? "on" : ""}`} disabled={disabled} onClick={() => onChange(!on)} />
  );
}

export function CopyButton({ text, className = "copy" }: { text: string; className?: string }) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const t = setTimeout(() => setDone(false), 1600);
    return () => clearTimeout(t);
  }, [done]);
  return (
    <button
      type="button"
      className={className}
      aria-label="Copy"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
        } catch {
          // Clipboard blocked; fall back to selecting is not possible here.
        }
      }}
    >
      {done ? <IconCheck /> : <IconCopy />}
    </button>
  );
}

export function Snippet({ title, code }: { title?: string; code: string }) {
  return (
    <>
      {title && <div className="snippet-title">{title}</div>}
      <div className="snippet">
        {code}
        <CopyButton text={code} />
      </div>
    </>
  );
}

/** Deterministic gradient per agent name so each agent has a stable colour. */
export function Avatar({ name, small }: { name: string; small?: boolean }) {
  const label = name.trim() || "?";
  let h = 0;
  for (const ch of label.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  const hue = h % 360;
  const initials = label
    .split(/[\s\-_/]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? "")
    .join("");
  const style = { "--a1": `hsl(${hue} 62% 52%)`, "--a2": `hsl(${(hue + 40) % 360} 70% 58%)` } as React.CSSProperties;
  return (
    <div className={`avatar ${small ? "small" : ""}`} style={style} aria-hidden>
      {initials || "?"}
    </div>
  );
}

export function Sheet({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" onClick={(e) => e.stopPropagation()}>
        <div className="grabber" />
        {children}
      </div>
    </div>
  );
}

export function Spinner() {
  return <span className="spinner" aria-label="Loading" />;
}
