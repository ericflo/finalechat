import { StrictMode, useEffect } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import { ToastHost } from "./components/Common";
import { match, navigate, useRoute } from "./lib/router";
import { bootstrap, toast, useStore } from "./lib/store";
import { AuthScreen } from "./screens/Auth";
import { DocsScreen } from "./screens/Docs";
import { Inbox } from "./screens/Inbox";
import { AgentsScreen, SettingsScreen } from "./screens/Settings";
import { ThreadScreen } from "./screens/Thread";

function App() {
  const route = useRoute();
  const user = useStore((s) => s.user);

  useEffect(() => {
    void bootstrap();
  }, []);

  // Service worker messages: notification taps navigate inside the app.
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const onMessage = (ev: MessageEvent) => {
      const data = ev.data as { type?: string; url?: string } | undefined;
      if (data?.type === "navigate" && data.url) navigate(data.url);
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, []);

  const path = route.path;
  const isDocs = path === "/api/" || path === "/api";

  if (isDocs) return <DocsScreen path="/api/?format=md" title="API reference" />;
  if (path === "/agents" || path === "/agents/") return <DocsScreen path="/AGENTS.md" title="Agent guide" />;

  if (user === null) {
    return (
      <div className="centered">
        <span className="spinner" />
      </div>
    );
  }

  if (user === false) {
    if (path === "/register") return <AuthScreen mode="register" />;
    if (path !== "/login") {
      // Remember where the user wanted to go.
      try {
        sessionStorage.setItem("fc.next", path + route.search.toString().replace(/^(.)/, "?$1"));
      } catch {
        // ignore
      }
    }
    return <AuthScreen mode="login" />;
  }

  // Signed in: honour a stored destination once.
  if (path === "/login" || path === "/register") {
    let next = "/";
    try {
      next = sessionStorage.getItem("fc.next") || "/";
      sessionStorage.removeItem("fc.next");
    } catch {
      // ignore
    }
    navigate(next, { replace: true });
    return null;
  }

  const thread = match("/t/:id", path);
  if (thread) return <ThreadScreen id={thread.id as string} highlightQuestion={route.search.get("q")} />;
  if (path === "/settings") return <SettingsScreen />;
  if (path === "/settings/agents") return <AgentsScreen />;
  return <Inbox filter={route.search.get("filter")} />;
}

function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js", { scope: "/" })
      .then((reg) => {
        reg.addEventListener("updatefound", () => {
          const sw = reg.installing;
          if (!sw) return;
          sw.addEventListener("statechange", () => {
            if (sw.state === "installed" && navigator.serviceWorker.controller) {
              toast("Finalechat updated. Reopen to use the new version.");
            }
          });
        });
      })
      .catch(() => {
        // Service worker is an enhancement; the app works without it.
      });
  });
}

registerServiceWorker();

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    <App />
    <ToastHost />
  </StrictMode>,
);
