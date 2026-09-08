// Test-only entrypoint. Production Vite builds do not include this directory.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { GenericSettingsForm, SettingsControls } from "../src/components/IntegrationSettings";
import { ArtifactScreen, ThreadArtifacts } from "../src/screens/Artifacts";
import { type ResourceView, type SettingsProposal } from "../src/lib/artifacts";
import { useRoute } from "../src/lib/router";
import { ResourceSettingsScreen } from "../src/screens/Connectors";
import { InspectMessage } from "../src/components/InspectMessage";
import { ThreadScreen } from "../src/screens/Thread";
import type { Thread, Message } from "../src/lib/types";

const fixture: ResourceView = {
  online: true,
  connector: { id: "connector", name: "Fixture connector", provider: "fixture", state: "active", requested_grants: [], grants: [], last_seen_at: null, expires_at: "2030-01-01T00:00:00Z" },
  resource: {
    id: "resource", connector_id: "connector", key: "project", label: "Fixture project", scope: "project", generation: "", updated_at: "2026-09-07T00:00:00Z",
    descriptor: { format: "finalechat.settings/v1", schema_version: "fixture/1", adapter_version: "1", fields: [
      { key: "/count", label: "Concurrency", schema: { type: "integer", minimum: 1, maximum: 16 }, writable: true, unset: true, class: "preference", effective_when: "new_or_resumed_session" },
      { key: "/text", label: "Long prompt", schema: { type: "string", max_length: 32768 }, writable: true, unset: true, class: "preference", effective_when: "new_or_resumed_session" },
    ], actions: [{ operation: "prompt.set", label: "Save prompt override", class: "preference", parameters: { type: "object", properties: { name: { type: "string", enum: ["narrator"] }, text: { type: "string", max_length: 32768 } }, required: ["name", "text"] } }, { operation: "settings.undo", label: "Undo reviewed settings change", class: "preference", parameters: { type: "object", properties: { command_id: { type: "string", max_length: 80 }, restore_sha256: { type: "string", max_length: 64 } }, required: ["command_id", "restore_sha256"] } }] },
    snapshot: { version: "v1", context: "fixture", saved: { "/count": 2, "/text": "A".repeat(4096) }, effective: {}, runtime_known: false },
  },
};

declare global { interface Window { fixture: ResourceView; replaceFixture: (v: ResourceView) => void; staged: SettingsProposal | null } }
if (new URLSearchParams(location.search).has("runtime")) {
  fixture.resource.scope = "session"; fixture.resource.generation = "runtime-one"; fixture.resource.label = "Fixture live session";
  fixture.resource.snapshot.runtime_known = true;
  fixture.resource.snapshot.details = { persistence: "This process only. Future sessions load project defaults." };
  fixture.resource.descriptor.fields = fixture.resource.descriptor.fields.slice(0, 1).map(f => ({ ...f, unset: false, effective_when: "next_task" }));
  fixture.resource.descriptor.actions = [];
}
window.fixture = fixture;

function Controls() {
  const [view, setView] = useState(fixture);
  const [proposal, setProposal] = useState<SettingsProposal | null>(null);
  window.replaceFixture = setView;
  window.staged = proposal;
  return <><GenericSettingsForm resource={view.resource} proposal={proposal} onProposal={setProposal} /><SettingsControls view={view} proposal={proposal} onProposal={setProposal} /></>;
}

const initial = new URLSearchParams(location.search);
function ArtifactFixture() {
  const route = useRoute();
  const surface = route.search.get("surface") || (route.path === "/tests/artifacts.html" ? "settings" : null);
  return <ArtifactScreen id="artifact" thread="thread" selectedRevision={route.search.get("revision")} selectedViewer={route.search.get("viewer")} initialAnchor={route.search.get("anchor")} surface={surface} />;
}
const threadFixture = { id: "thread", external_id: "codex:fixture-session", title: "Fixture session", agent: "Codex", meta: {}, unread_count: 0, pending_count: 0, last_read_at: "2026-09-07T00:00:00Z", created_at: "2026-09-07T00:00:00Z" } as Thread;
const messageFixture = { id: "chat-message", thread_id: "thread", sender: "agent", origin: "token", body: "The mirrored native answer", format: "text", importance: "normal", meta: { source_anchor: { dataset_format: "codex.rollout/v1", session_id: "fixture-session", event_id: "native-123" } }, created_at: "2026-09-07T00:00:00Z", attachments: [] } as Message;
if (initial.has("mcp")) { messageFixture.id = "01a08000-0000-7000-8000-000000000123"; messageFixture.meta = {}; }
Object.assign(window, { threadFixture, messageFixture });
function NavigationFixture() {
  const route = useRoute();
  if (route.path.includes("/artifacts/")) return <ArtifactFixture />;
  if (route.path === "/t/thread") return <ThreadScreen id="thread" highlightQuestion={null} highlightMessage={route.search.get("m")} />;
  return <InspectMessage message={messageFixture} thread={threadFixture} />;
}
function RuntimeFixture() {
  const route = useRoute();
  if (route.path === "/settings/resources/resource") return <ResourceSettingsScreen id="resource" />;
  return <ThreadArtifacts thread="thread" />;
}
createRoot(document.getElementById("root")!).render(initial.has("runtime") ? <RuntimeFixture /> : initial.has("navigation") ? <NavigationFixture /> : initial.has("artifact") ? <ArtifactFixture /> : <Controls />);
