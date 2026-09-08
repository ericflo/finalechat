import { Component, useEffect, useRef, useState, type ErrorInfo, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { onLinkClick } from "../lib/router";
import { dismissToast, toast, useStore } from "../lib/store";
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
      title={done ? "Copied" : label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
        } catch {
          toast("Could not copy. Select the text to copy it manually.", "error");
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
  // Ink is chosen from the brighter gradient stop's luminance, so initials
  // stay readable on every hue.
  const a1 = { h: hue, s: 60, l: 50 };
  const a2 = { h: (hue + 30) % 360, s: 68, l: 56 };
  const light = luminance(a2) > 0.32 || luminance(a1) > 0.32;
  const style = {
    "--a1": `hsl(${a1.h} ${a1.s}% ${a1.l}%)`,
    "--a2": `hsl(${a2.h} ${a2.s}% ${a2.l}%)`,
    color: light ? "#16161a" : "#fff",
  } as React.CSSProperties;
  return (
    <div className={`avatar ${small ? "small" : ""}`} style={style} aria-hidden>
      {initials || "?"}
    </div>
  );
}

/** Relative luminance of an HSL colour, for picking readable text. */
function luminance({ h, s, l }: { h: number; s: number; l: number }): number {
  const sat = s / 100;
  const lig = l / 100;
  const c = (1 - Math.abs(2 * lig - 1)) * sat;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = lig - c / 2;
  const [r, g, b] = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
  const lin = (v: number) => {
    const s = v + m;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** How many sheets hold the page still; the thread's scroll tracking ignores
 * scroll events while one is open. */
export let sheetsOpen = 0;

/** Pointers currently down anywhere. A sheet opened while one is held (a
 * long press) waits for it to lift before it takes taps; one opened from
 * the keyboard or after a completed tap is live at once. */
let pointersDown = 0;
if (typeof window !== "undefined") {
  window.addEventListener("pointerdown", () => pointersDown++, true);
  const up = () => {
    pointersDown = Math.max(0, pointersDown - 1);
  };
  window.addEventListener("pointerup", up, true);
  window.addEventListener("pointercancel", up, true);
}

/** Bottom sheet: locks the page behind it, traps focus, restores it on close.
 * A sheet opened by a long press ignores the click that the finger's lift
 * produces, so the gesture can neither dismiss it nor pick an item. */
export function Sheet({ onClose, children, label, className = "" }: { onClose: () => void; children: ReactNode; label?: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const [armed, setArmed] = useState(false);

  useEffect(() => {
    if (pointersDown === 0) {
      setArmed(true);
      return;
    }
    // Opened under a held finger: arm shortly after it lifts, or at the next
    // press, which is a new gesture by definition. Never on a clock alone.
    let timer: number | undefined;
    const lifted = () => {
      timer = window.setTimeout(() => setArmed(true), 120);
    };
    const pressed = () => setArmed(true);
    window.addEventListener("pointerup", lifted, { once: true, capture: true });
    window.addEventListener("pointercancel", lifted, { once: true, capture: true });
    window.addEventListener("pointerdown", pressed, { once: true, capture: true });
    return () => {
      window.removeEventListener("pointerup", lifted, true);
      window.removeEventListener("pointercancel", lifted, true);
      window.removeEventListener("pointerdown", pressed, true);
      window.clearTimeout(timer);
    };
  }, []);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const scrollY = window.scrollY;
    const { overflow, position, top, width } = document.body.style;
    sheetsOpen++;
    document.body.style.overflow = "hidden";
    document.body.style.position = "fixed";
    document.body.style.top = `-${scrollY}px`;
    document.body.style.width = "100%";
    const first = ref.current?.querySelector<HTMLElement>("input, textarea, button, [href], [tabindex]:not([tabindex='-1'])");
    first?.focus({ preventScroll: true });
    return () => {
      sheetsOpen--;
      document.body.style.overflow = overflow;
      document.body.style.position = position;
      document.body.style.top = top;
      document.body.style.width = width;
      window.scrollTo(0, scrollY);
      previous?.focus?.({ preventScroll: true });
    };
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCloseRef.current();
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
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const swallowUnarmed = (e: ReactPointerEvent | React.MouseEvent) => {
    if (!armed) {
      e.preventDefault();
      e.stopPropagation();
    }
  };
  return (
    <div
      className={`sheet-backdrop ${className}`}
      onClickCapture={swallowUnarmed}
      onPointerDown={(e) => {
        if (armed && e.target === e.currentTarget) onClose();
      }}
    >
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
