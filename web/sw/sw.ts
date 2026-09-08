/// <reference lib="webworker" />
// Finalechat service worker: push notifications, badge, notification taps,
// an app-shell cache so the installed app opens instantly, and a bounded
// cache of attachment images so a thread reads offline.

declare const __APP_VERSION__: string;
const sw = self as unknown as ServiceWorkerGlobalScope;

interface NotificationAction {
  action: string;
  title: string;
  icon?: string;
}

const VERSION = __APP_VERSION__;
const SHELL_CACHE = `fc-shell-${VERSION}`;
const ASSET_CACHE = `fc-assets-${VERSION}`;
const MEDIA_CACHE = "fc-media-v1";
const MEDIA_LIMIT = 160;
// Only images, and only ones a phone can hold many of; a 10 MiB PDF is
// fetched again rather than evicting sixty screenshots.
const MEDIA_MAX_BYTES = 4 * 1024 * 1024;

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

// Routes the app renders; only these are cached as the shell.
const appRoute = /^\/(?:t\/[^/]+|settings(?:\/agents)?|login|register|agents)?\/?$/;

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
      await Promise.all(keys.filter((k) => (k.startsWith("fc-shell-") && k !== SHELL_CACHE) || (k.startsWith("fc-assets-") && k !== ASSET_CACHE)).map((k) => caches.delete(k)));
      await trimMedia();
      await sw.clients.claim();
    })(),
  );
});

sw.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== sw.location.origin) return;

  // Attachment bytes are immutable and worth keeping for offline reading.
  if (url.pathname.startsWith("/api/v1/attachments/")) {
    event.respondWith(
      caches.open(MEDIA_CACHE).then(async (cache) => {
        const hit = await cache.match(req);
        if (hit) return hit;
        const res = await fetch(req);
        const type = res.headers.get("content-type") ?? "";
        const size = Number(res.headers.get("content-length") ?? "0");
        if (res.ok && res.status === 200 && type.startsWith("image/") && size <= MEDIA_MAX_BYTES) {
          // A full quota fails the put; trim anyway so the next one fits.
          void cache
            .put(req, res.clone())
            .catch(() => {})
            .finally(trimMedia);
        }
        return res;
      }),
    );
    return;
  }
  // Everything else under the API, and the agent-facing documents, is live.
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/cli/") || url.pathname.startsWith("/skill/")) return;
  if (["/AGENTS.md", "/agents.md", "/llms.txt", "/install.sh", "/openapi.json", "/healthz", "/readyz"].includes(url.pathname)) return;

  // Hashed assets: cache first, forever; retry once so a rolling deploy's
  // brief mismatch does not white-screen the app.
  if (url.pathname.startsWith("/assets/") || url.pathname.startsWith("/icons/")) {
    event.respondWith(
      caches.open(ASSET_CACHE).then(async (cache) => {
        const hit = await cache.match(req);
        if (hit) return hit;
        let res = await fetch(req).catch(() => null);
        if (!res || !res.ok) {
          await new Promise((r) => setTimeout(r, 800));
          res = await fetch(req).catch(() => null);
        }
        if (!res) return Response.error();
        if (res.ok) void cache.put(req, res.clone());
        return res;
      }),
    );
    return;
  }

  // Navigations: network first with a cached shell fallback so the app opens
  // offline and shows its own "reconnecting" state. Only the app's own HTML
  // is stored as the shell.
  if (req.mode === "navigate") {
    event.respondWith(
      (async () => {
        try {
          const res = await fetch(req);
          if (res.ok && appRoute.test(url.pathname) && (res.headers.get("content-type") ?? "").includes("text/html")) {
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

async function trimMedia() {
  try {
    const cache = await caches.open(MEDIA_CACHE);
    const keys = await cache.keys();
    if (keys.length <= MEDIA_LIMIT) return;
    // Cache keys come back in insertion order; drop the oldest.
    await Promise.all(keys.slice(0, keys.length - MEDIA_LIMIT).map((k) => cache.delete(k)));
  } catch {
    // ignore
  }
}

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
  const clients = await sw.clients.matchAll({ type: "window", includeUncontrolled: true });
  // The thread is on screen right now: no buzz for what the user is reading.
  // (Web Push still requires a visible notification; keep it silent.)
  const reading = p.thread_id ? clients.some((c) => c.focused && new URL(c.url).pathname === `/t/${p.thread_id}`) : false;

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
    renotify: !reading,
    requireInteraction: p.type === "question" && !reading,
    silent: reading,
    actions,
    timestamp: Date.now(),
  };
  if (!reading && (p.type === "question" || p.important)) options.vibrate = [80, 40, 80];

  const title = p.type === "question" ? `❓ ${p.title}` : p.important ? `❗ ${p.title}` : p.title;
  await sw.registration.showNotification(title, options);
  if (typeof p.badge === "number") await setBadge(p.badge);
  for (const c of clients) c.postMessage({ type: "refresh", payload: p });
  if (reading) {
    // Let it show for a beat so the platform is satisfied, then clear it.
    setTimeout(async () => {
      const shown = await sw.registration.getNotifications({ tag: p.tag });
      shown.forEach((n) => n.close());
    }, 1500);
  }
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
          // The browser supplies Sec-Fetch-Site itself; the cookie rides along.
          const res = await fetch(`/api/v1/questions/${p.question_id}/answer`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
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
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify(sub.toJSON()),
      });
    })(),
  );
});

sw.addEventListener("message", (event) => {
  const data = event.data as { type?: string } | undefined;
  if (data?.type === "skip-waiting") void sw.skipWaiting();
  if (data?.type === "clear-media") void caches.delete(MEDIA_CACHE);
});
