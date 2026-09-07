/// <reference lib="webworker" />
// Finalechat service worker: push notifications, badge, notification taps,
// and an app-shell cache so the installed app opens instantly.

declare const __APP_VERSION__: string;
const sw = self as unknown as ServiceWorkerGlobalScope;

interface NotificationAction {
  action: string;
  title: string;
  icon?: string;
}

const VERSION = __APP_VERSION__;
const SHELL_CACHE = `fc-shell-${VERSION}`;
const ASSET_CACHE = "fc-assets-v1";

interface PushPayload {
  type: "message" | "question" | "test";
  title: string;
  body: string;
  url: string;
  tag: string;
  thread_id?: string;
  message_id?: string;
  question_id?: string;
  options?: string[];
  important?: boolean;
  badge?: number;
}

sw.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.addAll(["/", "/manifest.webmanifest", "/icons/icon-192.png", "/icons/badge-96.png"]))
      .then(() => sw.skipWaiting()),
  );
});

sw.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(keys.filter((k) => k.startsWith("fc-shell-") && k !== SHELL_CACHE).map((k) => caches.delete(k)));
      await sw.clients.claim();
    })(),
  );
});

sw.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== sw.location.origin) return;
  // API and docs are always live.
  if (url.pathname.startsWith("/api/") || url.pathname === "/AGENTS.md" || url.pathname === "/healthz") return;

  // Hashed assets: cache first, forever.
  if (url.pathname.startsWith("/assets/") || url.pathname.startsWith("/icons/")) {
    event.respondWith(
      caches.open(ASSET_CACHE).then(async (cache) => {
        const hit = await cache.match(req);
        if (hit) return hit;
        const res = await fetch(req);
        if (res.ok) void cache.put(req, res.clone());
        return res;
      }),
    );
    return;
  }

  // Navigations: network first with a cached shell fallback so the app opens
  // offline and shows its own "reconnecting" state.
  if (req.mode === "navigate") {
    event.respondWith(
      (async () => {
        try {
          const res = await fetch(req);
          if (res.ok) {
            const cache = await caches.open(SHELL_CACHE);
            void cache.put("/", res.clone());
          }
          return res;
        } catch {
          const cache = await caches.open(SHELL_CACHE);
          return (await cache.match("/")) ?? Response.error();
        }
      })(),
    );
  }
});

sw.addEventListener("push", (event) => {
  let payload: PushPayload | null = null;
  try {
    payload = event.data ? (event.data.json() as PushPayload) : null;
  } catch {
    payload = null;
  }
  if (!payload) {
    payload = { type: "message", title: "Finalechat", body: event.data?.text() ?? "New activity", url: "/", tag: "generic" };
  }
  event.waitUntil(showNotification(payload));
});

async function showNotification(p: PushPayload) {
  const actions: NotificationAction[] = [];
  if (p.type === "question" && p.options && p.options.length > 0 && p.options.length <= 3) {
    for (const label of p.options.slice(0, 2)) actions.push({ action: `answer:${label}`, title: label });
  }
  if (p.type === "message" && p.thread_id) actions.push({ action: "open", title: "Open" });

  const options: NotificationOptions & { renotify?: boolean; vibrate?: number[]; actions?: NotificationAction[]; timestamp?: number } = {
    body: p.body,
    tag: p.tag,
    icon: "/icons/icon-192.png",
    badge: "/icons/badge-96.png",
    data: p,
    renotify: true,
    requireInteraction: p.type === "question",
    silent: false,
    actions,
    timestamp: Date.now(),
  };
  if (p.type === "question" || p.important) options.vibrate = [80, 40, 80];

  const title = p.type === "question" ? `❓ ${p.title}` : p.important ? `❗ ${p.title}` : p.title;
  await sw.registration.showNotification(title, options);
  if (typeof p.badge === "number") await setBadge(p.badge);
  const clients = await sw.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const c of clients) c.postMessage({ type: "refresh", payload: p });
}

async function setBadge(n: number) {
  const nav = sw.navigator as WorkerNavigator & { setAppBadge?: (n?: number) => Promise<void>; clearAppBadge?: () => Promise<void> };
  try {
    if (n > 0) await nav.setAppBadge?.(n);
    else await nav.clearAppBadge?.();
  } catch {
    // unsupported
  }
}

sw.addEventListener("notificationclick", (event) => {
  const p = event.notification.data as PushPayload | undefined;
  event.notification.close();
  const action = event.action;
  event.waitUntil(
    (async () => {
      if (action.startsWith("answer:") && p?.question_id) {
        const label = action.slice("answer:".length);
        try {
          const res = await fetch(`/api/v1/questions/${p.question_id}/answer`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "Sec-Fetch-Site": "same-origin" },
            credentials: "same-origin",
            body: JSON.stringify({ selected: [label] }),
          });
          if (res.ok) {
            await sw.registration.showNotification("Answered", { body: `${label} · ${p.title}`, tag: p.tag, icon: "/icons/icon-192.png", badge: "/icons/badge-96.png", silent: true });
            const clients = await sw.clients.matchAll({ type: "window", includeUncontrolled: true });
            for (const c of clients) c.postMessage({ type: "refresh" });
            return;
          }
        } catch {
          // fall through to opening the app
        }
      }
      await focusOrOpen(p?.url ?? "/");
    })(),
  );
});

async function focusOrOpen(url: string) {
  const target = new URL(url, sw.location.origin);
  const clients = await sw.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const c of clients) {
    if (new URL(c.url).origin === sw.location.origin) {
      await c.focus();
      c.postMessage({ type: "navigate", url: target.pathname + target.search });
      return;
    }
  }
  await sw.clients.openWindow(target.href);
}

sw.addEventListener("pushsubscriptionchange", (event) => {
  const e = event as ExtendableEvent & { oldSubscription?: PushSubscription | null; newSubscription?: PushSubscription | null };
  e.waitUntil(
    (async () => {
      const applicationServerKey = e.oldSubscription?.options.applicationServerKey ?? undefined;
      const sub = e.newSubscription ?? (await sw.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey }));
      await fetch("/api/v1/push/subscriptions", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Sec-Fetch-Site": "same-origin" },
        credentials: "same-origin",
        body: JSON.stringify(sub.toJSON()),
      });
    })(),
  );
});

sw.addEventListener("message", (event) => {
  if ((event.data as { type?: string })?.type === "skip-waiting") void sw.skipWaiting();
});
