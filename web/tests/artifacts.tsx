// Test-only entrypoint. Production Vite builds do not include this directory.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { GenericSettingsForm, SettingsControls } from "../src/components/IntegrationSettings";
import { ArtifactScreen } from "../src/screens/Artifacts";
import { type ResourceView, type SettingsProposal } from "../src/lib/artifacts";

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
window.fixture = fixture;

function Controls() {
  const [view, setView] = useState(fixture);
  const [proposal, setProposal] = useState<SettingsProposal | null>(null);
  window.replaceFixture = setView;
  window.staged = proposal;
  return <><GenericSettingsForm resource={view.resource} proposal={proposal} onProposal={setProposal} /><SettingsControls view={view} proposal={proposal} onProposal={setProposal} /></>;
}

createRoot(document.getElementById("root")!).render(new URLSearchParams(location.search).has("artifact") ? <ArtifactScreen id="artifact" thread="thread" selectedRevision={null} surface="settings" /> : <Controls />);
