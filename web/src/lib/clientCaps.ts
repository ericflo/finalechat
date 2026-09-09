import type { EagentClientCapsule } from "./types";

/**
 * Gathers the eagent client-capability capsule (v1) once per page load.
 *
 * Only the documented handshake keys are collected — timezone, locale, a
 * coarse device class, the app name/version, and screen dimensions. Nothing
 * fingerprint-y (no userAgent string, no canvas, no battery) ever leaves
 * the browser, and every probe is guarded so private-mode or odd browsers
 * simply yield a smaller capsule (or none at all) instead of throwing.
 */

function readTimezone(): string | undefined {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (typeof tz === "string" && tz.trim() !== "") return tz;
  } catch {
    // Intl unavailable or denied: leave the field out.
  }
  return undefined;
}

function readLocale(): string | undefined {
  try {
    const lang = navigator.language;
    if (typeof lang === "string" && lang.trim() !== "") return lang;
  } catch {
    // navigator.language denied: leave the field out.
  }
  return undefined;
}

function readScreen(): { text: string; minSide: number } | undefined {
  try {
    const w = window.screen?.width;
    const h = window.screen?.height;
    if (typeof w === "number" && typeof h === "number" && w > 0 && h > 0) {
      return { text: `${w}x${h}`, minSide: Math.min(w, h) };
    }
  } catch {
    // Screen dimensions denied: leave the field out.
  }
  return undefined;
}

function readDevice(minSide?: number): string | undefined {
  try {
    const uaData = (navigator as Navigator & { userAgentData?: { mobile?: boolean } }).userAgentData;
    const mobile = uaData?.mobile;
    const coarse =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(pointer: coarse)").matches;
    if (mobile === true) return minSide !== undefined && minSide >= 700 ? "tablet" : "phone";
    if (mobile === false) return coarse ? "tablet" : "desktop";
    if (coarse) return minSide !== undefined && minSide >= 700 ? "tablet" : "phone";
    return "desktop";
  } catch {
    return undefined;
  }
}

function readApp(): string | undefined {
  try {
    const version = typeof __APP_VERSION__ !== "undefined" ? __APP_VERSION__ : "";
    return `finalechat-web/${version || "unknown"}`;
  } catch {
    return undefined;
  }
}

function gather(): EagentClientCapsule | null {
  let capsule: EagentClientCapsule | null = null;
  try {
    const timezone = readTimezone();
    const locale = readLocale();
    const screen = readScreen();
    const device = readDevice(screen?.minSide);
    const app = readApp();
    const supplies: string[] = [];
    if (timezone) supplies.push("tz");
    if (locale) supplies.push("locale");
    if (screen) supplies.push("screen");
    if (!timezone && !locale && !device && !screen) return null;
    capsule = { supplies };
    if (timezone) capsule.timezone = timezone;
    if (locale) capsule.locale = locale;
    if (device) capsule.device = device;
    if (app) capsule.app = app;
    if (screen) capsule.screen = screen.text;
  } catch {
    return null;
  }
  return capsule;
}

let cached: EagentClientCapsule | null | undefined;

/** The handshake capsule, gathered once; null when nothing could be read. Never throws. */
export function getClientCapsule(): EagentClientCapsule | null {
  if (cached === undefined) {
    try {
      cached = gather();
    } catch {
      cached = null;
    }
  }
  return cached;
}

/** `{"eagent.client": capsule}`, or undefined when there is nothing to declare. Never throws. */
export function clientCapsMeta(): Record<string, unknown> | undefined {
  try {
    const capsule = getClientCapsule();
    if (!capsule) return undefined;
    return { "eagent.client": capsule };
  } catch {
    return undefined;
  }
}

/** Test-only reset for the once-per-load cache. */
export function resetClientCapsuleForTests(): void {
  cached = undefined;
}
