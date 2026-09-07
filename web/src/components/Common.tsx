import { Component, useEffect, useRef, useState, type ErrorInfo, type ReactNode } from "react";
import { onLinkClick } from "../lib/router";
import { dismissToast, useStore } from "../lib/store";
import { IconCheck, IconClose, IconCopy } from "./Icons";

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

/** Toasts stack (newest at the bottom), may carry one action, and live in a
 * region that always exists so assistive tech announces changes. */
export function ToastHost() {
  const toasts = useStore((s) => s.toasts);
  return (
    <div className="toast-host" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast ${t.kind}`} role={t.kind === "error" ? "alert" : undefined}>
          <span className="toast-text">{t.text}</span>
          {t.action && (
            <button
              type="button"
              className="toast-action"
              onClick={() => {
                dismissToast(t.id);
                t.action?.onClick();
              }}
            >
              {t.action.label}
            </button>
          )}
          <button type="button" className="toast-close" aria-label="Dismiss" onClick={() => dismissToast(t.id)}>
            <IconClose />
          </button>
        </div>
      ))}
    </div>
  );
}

export function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button type="button" role="switch" aria-checked={on} aria-label={label} className={`toggle ${on ? "on" : ""}`} disabled={disabled} onClick={() => onChange(!on)} />
  );
}

export function CopyButton({ text, className = "copy", label = "Copy" }: { text: string; className?: string; label?: string }) {
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
      aria-label={done ? "Copied" : label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
        } catch {
          // Clipboard blocked; nothing else to do here.
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
      <div className="snippet-wrap">
        <pre className="snippet">{code}</pre>
        <CopyButton text={code} />
      </div>
    </>
  );
}

// Twelve hues that stay distinct from each other and readable under white or
// dark text; a hash picks one per agent name so every agent keeps its colour.
const hues = [4, 24, 44, 88, 142, 168, 196, 214, 238, 262, 292, 330];

/** Deterministic colour per agent name so each agent is recognisable at a glance. */
export function Avatar({ name, small }: { name: string; small?: boolean }) {
  const label = name.trim() || "?";
  let h = 0;
  for (const ch of label.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  const hue = hues[h % hues.length] as number;
  const initials = label
    .split(/[\s\-_/:.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? "")
    .join("");
  // Yellow-green and cyan bands are too bright for white text; use ink there.
  const light = (hue >= 40 && hue <= 100) || (hue >= 160 && hue <= 200);
  const style = {
    "--a1": `hsl(${hue} 60% ${light ? 60 : 50}%)`,
    "--a2": `hsl(${(hue + 30) % 360} 68% ${light ? 66 : 56}%)`,
    color: light ? "#16161a" : "#fff",
  } as React.CSSProperties;
  return (
    <div className={`avatar ${small ? "small" : ""}`} style={style} aria-hidden>
      {initials || "?"}
    </div>
  );
}

/** Bottom sheet: locks the page behind it, traps focus, restores it on close. */
export function Sheet({ onClose, children, label }: { onClose: () => void; children: ReactNode; label?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const scrollY = window.scrollY;
    const { overflow, position, top, width } = document.body.style;
    document.body.style.overflow = "hidden";
    document.body.style.position = "fixed";
    document.body.style.top = `-${scrollY}px`;
    document.body.style.width = "100%";
    const first = ref.current?.querySelector<HTMLElement>("input, textarea, button, [href], [tabindex]:not([tabindex='-1'])");
    first?.focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "Tab" && ref.current) {
        const items = Array.from(ref.current.querySelectorAll<HTMLElement>("input, textarea, button, [href], [tabindex]:not([tabindex='-1'])")).filter((el) => !el.hasAttribute("disabled"));
        if (items.length === 0) return;
        const firstEl = items[0] as HTMLElement;
        const lastEl = items[items.length - 1] as HTMLElement;
        if (e.shiftKey && document.activeElement === firstEl) {
          e.preventDefault();
          lastEl.focus();
        } else if (!e.shiftKey && document.activeElement === lastEl) {
          e.preventDefault();
          firstEl.focus();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      document.body.style.position = position;
      document.body.style.top = top;
      document.body.style.width = width;
      window.scrollTo(0, scrollY);
      previous?.focus?.({ preventScroll: true });
    };
  }, [onClose]);
  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div ref={ref} className="sheet" role="dialog" aria-modal="true" aria-label={label} onClick={(e) => e.stopPropagation()}>
        <div className="grabber" />
        {children}
      </div>
    </div>
  );
}

/** A confirmation that looks like the rest of the app instead of a system alert. */
export function ConfirmSheet({
  title,
  body,
  confirmLabel,
  danger,
  onConfirm,
  onClose,
}: {
  title: string;
  body?: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Sheet onClose={onClose} label={title}>
      <div className="confirm">
        <h2>{title}</h2>
        {body && <p>{body}</p>}
        <div className="confirm-actions">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className={`btn ${danger ? "danger" : "primary"}`}
            onClick={() => {
              onClose();
              onConfirm();
            }}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </Sheet>
  );
}

export function Spinner() {
  return <span className="spinner" aria-label="Loading" />;
}

/** Catches a render error so one bad message cannot blank an installed app. */
export class ErrorBoundary extends Component<{ children: ReactNode; fallback?: (reset: () => void) => ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("render failed", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      const reset = () => this.setState({ error: null });
      if (this.props.fallback) return this.props.fallback(reset);
      return (
        <div className="centered">
          <div className="card auth-card">
            <h1 style={{ fontSize: 20 }}>Something broke</h1>
            <p style={{ color: "var(--text-2)", margin: "8px 0 16px" }}>{this.state.error.message}</p>
            <button type="button" className="btn primary block" onClick={() => window.location.reload()}>
              Reload
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
