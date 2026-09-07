import { api } from "./api";

export type PushState = "unsupported" | "insecure" | "denied" | "prompt" | "subscribed" | "unsubscribed";

export function isStandalone(): boolean {
  return window.matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;
}

export function isIOS(): boolean {
  return /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

export function pushSupported(): boolean {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

/** Resolves the active registration, or null when none activates in time (the
 * promise `serviceWorker.ready` never settles when registration failed). */
async function registration(timeoutMs = 4000): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) return null;
  return Promise.race([
    navigator.serviceWorker.ready,
    new Promise<null>((resolve) => window.setTimeout(() => resolve(null), timeoutMs)),
  ]);
}

export async function getPushState(): Promise<PushState> {
  if (!pushSupported()) return "unsupported";
  if (!window.isSecureContext) return "insecure";
  if (Notification.permission === "denied") return "denied";
  const reg = await registration();
  if (!reg) return "unsupported";
  const sub = await reg.pushManager.getSubscription();
  if (sub) return "subscribed";
  return Notification.permission === "granted" ? "unsubscribed" : "prompt";
}

function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const b64 = (base64 + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

export async function subscribeToPush(): Promise<PushState> {
  if (!pushSupported()) return "unsupported";
  const permission = await Notification.requestPermission();
  if (permission !== "granted") return permission === "denied" ? "denied" : "prompt";
  const { public_key } = await api.vapid();
  const reg = await navigator.serviceWorker.ready;
  let sub = await reg.pushManager.getSubscription();
  if (!sub) {
    sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(public_key) });
  }
  await api.subscribePush(sub.toJSON());
  return "subscribed";
}

export async function unsubscribeFromPush(): Promise<PushState> {
  if (!pushSupported()) return "unsupported";
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.getSubscription();
  if (sub) {
    try {
      await api.unsubscribePush(sub.endpoint);
    } catch {
      // Server-side cleanup is best-effort; the browser subscription is what matters here.
    }
    await sub.unsubscribe();
  }
  return Notification.permission === "granted" ? "unsubscribed" : "prompt";
}

/** Re-sends the current subscription so a fresh login on this device is linked. */
export async function resyncPushSubscription(): Promise<void> {
  if (!pushSupported()) return;
  try {
    const reg = await registration();
    const sub = await reg?.pushManager.getSubscription();
    if (sub) await api.subscribePush(sub.toJSON());
  } catch {
    // Not fatal.
  }
}

/** Drops this device's subscription on sign-out so the next account (or a
 * stranger with the phone) does not keep receiving this account's pushes. */
export async function forgetPushSubscription(): Promise<void> {
  if (!pushSupported()) return;
  try {
    const reg = await registration(1500);
    const sub = await reg?.pushManager.getSubscription();
    if (!sub) return;
    await api.unsubscribePush(sub.endpoint).catch(() => {});
    await sub.unsubscribe();
  } catch {
    // Not fatal.
  }
}

/** This device's push endpoint, for matching against the server's device list. */
export async function currentPushEndpoint(): Promise<string | null> {
  if (!pushSupported()) return null;
  try {
    const reg = await registration(1500);
    const sub = await reg?.pushManager.getSubscription();
    return sub?.endpoint ?? null;
  } catch {
    return null;
  }
}

export function setBadge(count: number) {
  const nav = navigator as Navigator & { setAppBadge?: (n?: number) => Promise<void>; clearAppBadge?: () => Promise<void> };
  try {
    if (count > 0) nav.setAppBadge?.(count)?.catch(() => {});
    else nav.clearAppBadge?.()?.catch(() => {});
  } catch {
    // Badging unsupported.
  }
}
