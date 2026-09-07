import type { ReactNode } from "react";
import { back } from "../lib/router";
import { IconBack } from "./Icons";

export function TopBar({ title, subtitle, onBack, backTo, right, big }: { title: ReactNode; subtitle?: ReactNode; onBack?: () => void; backTo?: string; right?: ReactNode; big?: boolean }) {
  const showBack = onBack || backTo;
  return (
    <header className="topbar">
      <div className={`topbar-inner ${big ? "topbar-big" : ""}`}>
        {showBack && (
          <button type="button" className="icon-btn" aria-label="Back" onClick={() => (onBack ? onBack() : back(backTo))}>
            <IconBack />
          </button>
        )}
        <div className="topbar-title" style={{ paddingLeft: showBack ? 0 : 6 }}>
          <h1>{title}</h1>
          {subtitle && <div className="sub">{subtitle}</div>}
        </div>
        {right}
      </div>
    </header>
  );
}
