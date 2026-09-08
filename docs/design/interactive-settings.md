**Proposal: settings artifacts with a durable command channel**

Status: implementation in progress; see [the implementation checklist](implementation-progress.md). Extends [durable session artifacts](session-artifacts.md).

Implementation refinement: the trusted host opens a 30-minute resource-bound editing lease while a settings revision is current. It can keep that same editor active across proactive transcript publications. Newly opened historical documents remain read-only. Commands retain the original source revision and recheck owner, binding, active connector, runtime generation, grants, schema and expected settings version; the iframe never receives the editing lease identifier. This prevents routine archive publication from interrupting a user mid-edit without granting historical pages ambient control.

Each integration can publish a self-contained settings website, its schema, and snapshots of its configuration. FinaleChat hosts that page in the artifact sandbox and provides a small JavaScript bridge. A paired integration process receives structured commands through outbound HTTPS, validates them against the actual local state, performs supported operations, and reports the result. Historical pages and source data survive independently of that process.

Settings changes run as integration code. They are not chat messages asking a model to edit a configuration file. The model is not in the path from Save to configuration persistence.

**1. Separate the saved surface, the target resource and the execution capability**

Eagent also exposes a separate explicitly paired session resource in the trusted generic form. `GET /threads/{thread}/settings-resources` discovers account-owned session resources whose snapshot declares `details.thread_external_id`; this is a navigation hint, not a binding or grant. Live task concurrency and narrator timing changes are acknowledged by the actual harness loop and recorded in its native event stream. A new run receives a new generation and loads project defaults.

A settings artifact contains a bundled `settings/index.html`, a versioned settings schema, saved/effective values with provenance, and references to its context. Its script can render custom pickers, forms, route tests, prompt editors and explanations. Shared viewer assets can be reused across sessions.

The live binding is a separate server record. It identifies an authorized connector installation, resource, adapter version, capabilities, and any particular running-session instance. Uploading an HTML file or declaring a capability in its manifest does not grant the script authority to change settings.

Distinguish these identities:

| Identity | Meaning |
| --- | --- |
| Thread/session | The conversation from which the user opened settings |
| Artifact revision | The immutable page and historical data being displayed |
| Connector installation | A paired integration process on a particular machine/account |
| Settings resource | A project config, named profile, user config, or session override |
| Runtime instance | One particular running process/session generation, if relevant |
| Resource version | The version of the saved settings and their relevant resolution context |

This matters immediately for eagent: `#/config` edits one project's defaults, while several sessions can use that project. The example session's config page targets `SoundboxingWork`; it is not a private configuration file for session `1788827736789`. Model/default changes currently apply to sessions started or resumed after saving, as `internal/web/static/config.js` states.

A resource shared by several threads has one identity and one serialized write stream. The trusted FinaleChat container labels the target and scope, for example **SoundboxingWork · Project defaults · Used by new or resumed sessions**. The iframe cannot replace that target with an arbitrary filesystem path, hostname, session or account.

**2. Define a settings descriptor, not a universal settings vocabulary**

Each adapter publishes typed fields and supported operations. JSON Schema can describe values; a small protocol extension describes behavior. FinaleChat understands the envelope, validation boundaries and results; it does not need to know what every provider's setting means.

Field metadata includes:

- Stable key, label, type, allowed values/ranges and validation details.
- Saved value, effective value, default/inherited value, and source layer.
- Writable scopes and a reason when a field is locked or unsupported.
- Effect timing: immediate, next turn, next task, new/resumed session, restart required, or unknown.
- A classification for credential references, permission changes, executable configuration, or cost-bearing actions.
- Dependency/capability information, including adapter and installed product versions.

Publish both saved settings and active runtime configuration. They can differ. An adapter must not substitute the connector's own environment for the environment of a separate running agent when claiming a value is effective. Include a resolution-context identifier and return unknown where a runtime cannot be observed.

Model catalogs, preset descriptions and other picker inputs can be ordinary artifact data files. Available values remain subject to fresh adapter validation at execution time. Credentials appear as presence/reference information; their values stay out of HTML, snapshots, drafts, command payloads and audit records.

Use explicit `set` and `unset` edits rather than ambiguous null handling. Unset means remove the override and inherit from another layer. Arrays and nested maps follow the adapter's own semantics, so Claude permission merging is not accidentally treated as a scalar replacement.

**3. Keep ordinary saving to one deliberate user action**

The artifact SDK extends the existing MessageChannel bridge. An illustrative API is:

```js
const state = await finale.settings.read();

// Called as the custom form changes. This stages edits in the host UI.
await finale.settings.propose({
  schemaVersion: state.schemaVersion,
  expectedVersion: state.version,
  edits: [
    { op: "set", key: "task_concurrency", value: 4 },
    { op: "set", key: "narrator_tick_seconds", value: 60 }
  ]
});

finale.settings.onResult(result => renderSavedState(result));
```

`read` returns an explicitly labeled snapshot and availability, not a promise of a fresh local read. An optional refresh requests one from the connector. The host binds every call to this mounted surface and authorized resource; scripts do not supply arbitrary targets or API endpoints.

The custom page stages proposals. The final **Save** button and scope/effect summary belong to trusted FinaleChat UI. The host freezes the displayed proposal on that click, including its digest and expected version, and submits exactly that content. No queued draft executes merely because the iframe loads or emits a message. A changed proposal requires a new click. Ordinary model, effort and numeric preference edits need no additional confirmation modal.

Validation can happen while editing or as part of Save. The adapter returns a canonical diff and field errors. If normalization changes the meaning of the user's proposal, or a wider scope is required, show that revised proposal before submitting it. Distinct actions such as **Test route**, **Save bundle**, **Reset override**, and **Undo** use the same trusted action mechanism with their own typed payloads. Testing a route is a real provider call, not part of harmless form validation.

A generic settings form rendered by FinaleChat can be a fallback for integrations that publish a schema without custom HTML. Custom pages and generic forms use the same command protocol.

**4. Use a durable command queue with explicit outcomes**

The browser submits an authorized command to FinaleChat. The connector keeps an outbound long-poll open and receives work as soon as it exists. This uses the application's existing HTTPS and database/event-bus patterns; it does not require a public local port or a live iframe-to-localhost connection.

Suggested protocol endpoints:

| Endpoint | Role |
| --- | --- |
| `POST /api/v1/connectors` | Begin pairing and record the integration installation |
| `PUT /api/v1/connectors/{id}/resources/{key}` | Advertise authorized settings resources, descriptors and capability versions |
| `GET /api/v1/settings-resources/{id}` | Latest published settings snapshot and connector availability |
| `POST /api/v1/settings-resources/{id}/commands` | Browser creates a structured command after the trusted Save/action |
| `POST /api/v1/connectors/{id}/commands/claim?wait=...` | Connector waits for and claims pending commands under a lease |
| `POST /api/v1/commands/{id}/result` | Connector records an outcome using its current claim token |
| `GET /api/v1/commands/{id}` | Browser retrieves durable status/results |
| `POST /api/v1/commands/{id}/cancel` | Cancel work not yet executed, or request cancellation if the operation supports it |

A command envelope records its ID, idempotency key, authenticated user identity, resource, runtime generation where relevant, operation/schema version, expected resource version, exact edits, creation/expiry time, and originating surface revision. The server supplies identity and grants; it does not accept the iframe's claim that the user approved something.

Use PostgreSQL tables for connectors/resources, commands, claims/results and compact audit entries. Subscribe-before-query long-polling avoids missed wakeups. The existing bus carries only a notification that durable state changed. Replica restarts or missed notifications cannot lose a command.

Expose these states accurately: queued, executing, succeeded, rejected, conflicted, expired, cancelled, and outcome unknown. A successful result contains effect details rather than just `ok: true`:

```json
{
  "status": "succeeded",
  "command_id": "<id>",
  "previous_version": "<old>",
  "version": "<new>",
  "effects": [
    {
      "key": "task_concurrency",
      "saved": true,
      "runtime_applied": false,
      "effective_when": "new_or_resumed_session"
    }
  ],
  "snapshot_publication": "pending"
}
```

The UI can then say **Saved. Takes effect when a session starts or resumes.** An HTTP response accepting a command only means it was queued. A file-write acknowledgement does not prove a running process adopted the new value. Native-runtime acknowledgement, when available, is recorded separately.

**5. Handle stale pages and duplicate delivery at the actual writer**

Require an expected version for mutations. The adapter re-reads local state immediately before execution and validates both the file version and relevant schema/resolution context. If a terminal edit, a second browser, an environment override or managed policy invalidated the proposal, return a conflict with current values. Never force an old page's full config over the current file.

Serialize mutations per resource, including the local eagent UI and remote commands. Eagent currently checks an etag and uses an atomic file replacement; its read/check/write sequence is not itself protected by a cross-process transaction. Add a shared resource lock for cooperating writers and perform the comparison under that lock. Preserve the limitation that an unrelated editor ignoring that lock can still race a filesystem write; detect/reconcile observed changes and do not claim a general filesystem compare-and-swap guarantee.

Delivery is at least once. The connector needs a durable operation journal and per-resource execution ownership. Record intent, desired hashes and result so that retries of a completed command return the original result. After a crash between writing a file and reporting success, reconcile against recorded before/after versions. If the outcome cannot be established, report it as unknown. Claim/lease fencing must prevent an old worker from reporting or continuing a superseded operation; adapters that cannot fence an external action must reconcile instead of launching a duplicate.

Do not automatically rerun a potentially billable route test or other non-idempotent action just because its acknowledgement was lost. Expiry/cancellation before execution prevents the action; after execution starts it is not a rollback guarantee. Undo is a new conditional command against the current revision, not permission to overwrite everything changed since the original save.

**6. Preserve useful offline behavior without delayed surprises**

Settings artifacts remain readable after the connector disappears. Historical snapshots are read-only by default. A separate **Edit current settings** action resolves a current resource binding, gets current capabilities, and starts a new proposal. Selecting an old artifact revision never recreates its old runtime or restores its settings implicitly.

When the connector is offline, save an editable draft in FinaleChat. The default is to ask the user to review/send it when connectivity returns. An explicit **Send when connected** option may queue project-default changes with a bounded expiry and expected version. Ephemeral session changes and route tests should fail promptly when the particular runtime is unavailable. Old queued commands must not attach to the next process merely because a session name or project path matches.

Use heartbeat leases for connector availability; the age of a B2 artifact alone is not a liveness signal. Revoke grants and pending commands when the connector is unpaired, the account loses access, or the target is deleted. A restored artifact starts with no live command binding.

A long-lived connector is necessary to apply settings while the model is idle or no session is running. Eagent can host it in `serve` and in normal running sessions, with one elected owner for each shared resource. A separate optional service can keep project-default editing available afterward. Claude Code's current short-lived hooks and the existing stateless CLI need an optional supervised companion loop; hook callbacks can wake it but cannot be the only delivery opportunity. It establishes outbound connections only and reports actual availability.

Keep chat mirroring, artifact publication and command delivery as distinct capabilities. Turning off mirrored messages must not silently disable settings control. An explicit disable-connector operation reports its result before disconnecting, with a durable local result available for reconciliation if that acknowledgement is lost.

**7. Preserve the current local safeguards and make grants narrow**

Remote control is a separate opt-in capability from chat mirroring and archive upload. During connector setup, select which scopes and operations may be controlled. Use a connector-specific credential that can claim only its own commands and publish only its authorized resource snapshots/results. A general artifact publisher token cannot enqueue human settings mutations. Browser command creation remains session-authenticated and protected by same-origin checks.

Keep the opaque sandbox and MessageChannel source/generation checks from the artifact design. Extend its allowed messages deliberately; do not give it cookies, a general fetch proxy, filesystem access, shell execution, or the ability to answer unrelated approvals. Enforce operation/schema/path/size constraints again on the server and locally. Arbitrary uploaded viewers do not get the settings bridge by default.

Expose the breadth of each integration's settings, including locked values and their reasons. Permission/sandbox changes, executable hooks/plugins and credential-routing changes carry distinct capabilities and a clear trusted summary of their effects. Managed restrictions remain authoritative. Ordinary settings should stay easy to save. This is a product permission boundary, not a new approval step for preparing this design.

For eagent, retain the current rule that new provider endpoints and key-variable names are established locally. Continue exposing credential presence rather than values. Moving validation into a shared service must not weaken `checkRoutes` or merely fake localhost headers to bypass the current HTTP guard. Prompts and raw configuration also require typed, bounded operations against registered resources; they are not an arbitrary file-write escape hatch.

**8. Provider adapters define what works and when**

| Provider | Persistent settings | Running-session settings | Implementation |
| --- | --- | --- | --- |
| eagent | Project config, presets/bundles, prompt overrides, model/fallback/effort choices, concurrency/timeouts/narration options, mirror/archive preferences | Separate session grant controls live task concurrency and narrator check/quiet timing, with native acknowledgements and process-generation fencing. Other settings remain defaults for new/resumed sessions. | Shared config service and portable `config.js` editor; durable project writer and scheduler-owned session queue. |
| Claude Code | User, project and project-local settings, subject to managed policy and actual merge rules | Reload behavior varies by field. Do not equate a file save with a live model/effort switch. | Extend the existing CLI/hooks integration with a scoped companion, versioned schema and file-change observation. |
| Codex | Supported user config writes and declared project/profile settings | Supported overrides on subsequent turns only when this adapter controls that thread through a compatible API | Add a dedicated adapter; use native config APIs and their validation when available. |

Claude Code documents settings-file watching for many fields; model and effort require their session mechanisms, and some settings take effect after a clear/restart. Its precedence and array merging also vary from a simple last-writer-wins map. Publish behavior per field and tested product version, and observe `ConfigChange` where available rather than inventing a blanket reload promise. [Claude Code settings](https://code.claude.com/docs/en/settings).

Codex App Server documents effective `config/read`, individual writes, atomic `config/batchWrite` to user configuration, and overrides on `turn/start`. Use those where the installed adapter supports them; a new App Server connection must not be assumed to control an unrelated existing desktop session. Reading managed requirements is part of validation. Preserve user-config scope separately from thread overrides. [OpenAI App Server documentation](https://learn.chatgpt.com/docs/app-server), [configuration basics](https://learn.chatgpt.com/docs/config-file/config-basic).

Capability discovery handles missing or different provider versions. An unsupported field remains visible with an explanation or a saved historical value. No adapter should launch a fake empty model turn just to change a setting. Supported next-turn changes can be staged by the owning controller without interrupting current work.

**9. Make settings changes part of the permanent record**

Keep command creation, execution outcome and before/after revisions in an append-only control audit. Attribute them to the authenticated user and source surface. Include effect timing, conflicts and explicit partial/unknown outcomes. Redact sensitive values before persistence.

After an acknowledged change, publish a new settings snapshot artifact revision and include the relevant audit data in the session archive. A running eagent harness can append a `settings.changed` event through its normal single-writer path; project changes without a running harness go into a resource audit log for later linkage. Never let the companion append directly to an active session JSONL behind the harness's back.

Record both settings saved for future sessions and settings actually adopted by a running session. That distinction makes later workflow explanations trustworthy. B2 publication failures should not undo a successful local save: the command result remains durable in PostgreSQL, the UI shows archive publication pending, and the publisher retries. Once saved, historical revisions continue to show what was true then.

**Delivery sequence**

1. Ship the durable artifact foundation and read-only settings snapshots. Extract eagent's config/projection services so local and future remote paths share validation.
2. Add the scoped connector, durable queue, conditional config writes, result acknowledgement and trusted Save UI. Prove eagent project-default changes first using the existing example session/project. Include model/fallback/effort, concurrency/timeouts, narrator settings and bundle/prompt operations; expose unsupported/local-only controls clearly.
3. Add route tests, reset/undo, setting-change audit and snapshot publication. Distinguish saved defaults from running-session values throughout.
4. Validate Claude Code's existing integration, add its companion/settings adapter, and test actual per-field reload behavior. Add Codex settings through a dedicated adapter with verified native API support. Neither provider needs to use eagent's UI or schema.
5. Add selected hot changes to eagent only where the harness has a safe, explicit application boundary. Define next-turn/next-task behavior and preserve running work; never silently restart an agent or current tool.

FinaleChat needs new connector/resource/command/audit store and API files, migrations, bus events, a typed bridge extension, trusted settings controls, and integration documentation. Eagent needs a transport-independent settings service, cooperating-writer locking, a durable command journal, connector lifecycle management, and configuration/audit hooks. The existing Python CLI needs the common companion and provider adapters while preserving its stdlib-only installation contract.

Acceptance tests cover stale artifact pages, simultaneous local/phone edits, two sessions sharing one project, environment and managed overrides, reconnect/restart after save-before-ack, fenced/duplicate commands, failed B2 publication, expired offline commands, spoofed iframe calls, attempts to change target scope, provider reload semantics, secret exclusion and historical playback. For each successful write, prove both the persisted change and the accurately reported runtime effect. This proposal was prepared using source inspection, read-only local requests and current provider documentation; no configuration was saved and no model probe was executed.
