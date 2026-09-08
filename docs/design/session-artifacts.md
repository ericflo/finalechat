**Design: durable, versioned session artifacts**

Status: implemented locally; see [validation and implementation limits](implementation-progress.md). This replaces the proposed live proxy between FinaleChat and eagent. Paths below refer to `/home/ericflo/Development/finalechat` and `/home/ericflo/Development/stream-agent-duel/eagent_final`.

The optional [interactive settings extension](interactive-settings.md) adds custom settings artifacts, scoped live bindings and a durable command channel. Historical inspection remains available independently of remote control.

The user opens **Under the hood** in a FinaleChat thread and sees an interactive record of the session, including after the original machine is unavailable. Integrations optionally publish a portable website and its source data. FinaleChat stores immutable revisions in its existing private B2 storage and supplies a common container for opening, downloading, and navigating them.

The enduring object is the dataset. A viewer is a versioned interpretation of that dataset. Publishing a new viewer can reuse all existing data; publishing new data can reuse the existing viewer.

**Starting point when this design was written**

- The running eagent session `1788827736789` explicitly identifies FinaleChat thread `01a07e71-1c61-700c-9f0f-fd91b2e5e89e` in its `phone` metadata. Its project is `SoundboxingWork`. No heuristic matching is needed.
- `eagent/internal/harness/phone.go` already publishes narrator messages with `meta.seq`, and records a `phone.thread` event locally. These are useful anchors for opening a particular moment from chat.
- `eagent/internal/web/static/app.js` already renders chat, task details, a timeline, usage, and tool traffic. It currently fetches session details from Go and events from HTTP/SSE; it is not yet a standalone JSONL viewer.
- `eagent/internal/state` is the authoritative Go reducer. `internal/web/server.go` turns that state into presentation data and additionally checks live processes. Historical presentation must separate those two sources.
- Sessions can span multiple original JSONL files. `internal/store/store.go` preserves source filename/line and ignores an incomplete final line. `attachments/` and `outputs/` contain data not fully present in the events. The example contained about 2.36 MB of JSONL and 219 KB of separate outputs when inspected.
- FinaleChat already has a private `blob.Store` abstraction with B2 and memory implementations. Existing attachments have a different lifecycle: 10 MiB per upload, up to eight per message, removal with messages, and cleanup of unattached uploads after 24 hours. Their delivery policy deliberately prevents scripts from running. Reuse the storage implementation, but introduce an artifact resource with its own lifecycle and preview route.

**1. Standardize packaging and hosting; integrations retain their schemas**

Introduce a generic `website` artifact attached to a thread. An integration chooses a stable external key, such as `session-inspector`, and updates that artifact throughout the session. A thread may have several artifacts. Registration is idempotent within the thread; identity also includes the owning account and the thread UUID, rather than assuming a timestamp is globally unique.

A logical eagent download would contain:

```text
manifest.json
index.html                         # self-contained viewer; inline CSS/JS
session/1788827736789.jsonl         # original bytes through a committed boundary
session/<later-subsession>.jsonl   # all subsequent rollover files, in order
session/attachments/...
session/outputs/...
context.json                       # available config/prompt/pricing provenance
derived/summary.json               # optional, disposable presentation cache
README.txt                         # opening, schema, and recovery instructions
```

The generic manifest identifies the format version, producer/version, entrypoint, dataset schema, logical files, MIME types, lengths and SHA-256 hashes. Each file has a role such as `viewer`, `source`, `asset`, or `derived`. Files refer to immutable stored chunks; downloads reconstruct ordinary files and do not require a FinaleChat SDK.

An integration-specific section records eagent's session identity, ordered subsessions, committed sequence frontier, capture timestamp and completeness. Preserve native JSONL fields, unknown event payloads, original filenames and line boundaries. A concatenated `events.jsonl` may be offered as a convenience, but it is not a replacement for the original subsession layout.

Illustrative manifest fields, with file/chunk descriptors omitted:

```json
{
  "format": "finalechat.website/v1",
  "producer": {"name": "eagent", "version": "<build>"},
  "entrypoint": "index.html",
  "dataset": {
    "schema": "eagent.session/v1",
    "session_id": "1788827736789",
    "through_seq": 2480,
    "state_at_capture": "idle",
    "capture_complete": true
  },
  "viewer": {"id": "eagent-inspector", "version": "1"},
  "captured_at": "<UTC timestamp>",
  "files": []
}
```

`capture_complete` means the declared checkpoint and included files were captured successfully. It does not mean the agent finished, that the machine remains online, or that later work has been backed up. The published manifest must distinguish omitted files, missing files and redacted data.

FinaleChat validates the container and ownership. It does not interpret eagent task events or require other integrations to adopt eagent's schema. Version the host protocol separately from the producer's dataset schema.

**2. Publish immutable revisions with an atomic current pointer**

Use PostgreSQL for artifact identities, revision manifests, ownership and object references; use B2 for file bytes. Suggested records are `artifacts`, `artifact_revisions`, `artifact_blobs`, and revision-to-blob references. Every successful publication creates an immutable revision. `artifacts.current_revision_id` is the only moving pointer.

The publication sequence is:

1. Read the current revision and prepare a consistent local checkpoint.
2. Ask which referenced hashes are missing; upload those objects.
3. Commit a manifest with an idempotency key and the expected previous revision.
4. In one database transaction, validate ownership and completed object references, insert the immutable revision, and advance the pointer.
5. Publish `artifact.updated` through the existing bus after commit.

Conflicting writers receive a conflict and reconcile. A stale publisher must not replace newer data; eagent additionally checks its sequence frontier before retrying. A deliberate rollback is a separate user operation. Thread deletion/recreation cannot silently resurrect an artifact bound to a deleted thread UUID.

All objects use immutable, owner-scoped keys. A client-supplied hash is verified during upload, and knowing a hash never grants access to somebody else's blob. Concurrent uploads of the same content require reservation/deduplication and cleanup so B2 versions do not accumulate unnoticed.

For append-only files, upload only newly committed byte spans, split into bounded chunks, and reuse prior spans. Chunk boundaries may fall inside a JSON line; the reconstructed checkpoint must end at a complete line. A 1 MiB initial chunk bound works with the existing in-memory `blob.Store.Put` API. Changed non-append files get new content-addressed objects. The viewer is uploaded only when its bytes change. This avoids repeatedly uploading an ever-growing session.

Upload limits, file counts, manifest size and account storage quotas must be explicit and independent of message attachment limits. Exceeding a quota leaves the previous valid revision accessible and reports backup lag; it never silently truncates the record.

Failed publication leaves the last revision usable. Uncommitted objects can expire after a grace period, but a committed artifact remains live without a message attachment and without a publisher heartbeat. Keep committed revisions until explicit deletion or an explicitly selected retention policy. Reclaim blobs only when no committed revision or active upload references them. Record B2 object version IDs and use a durable deletion queue with retries, including for thread/account deletion.

**3. A small API that any integration can implement**

Proposed routes, subject to the final naming pass:

| Route | Purpose |
| --- | --- |
| `PUT /api/v1/threads/{ref}/artifacts/{key}` | Register/update artifact identity and title, idempotently |
| `GET /api/v1/threads/{ref}/artifacts` | Discover artifacts attached to this thread |
| `POST /api/v1/artifacts/{id}/blobs/check` | Find missing hashes within the caller's authorized artifact |
| `PUT /api/v1/artifacts/{id}/blobs/{sha256}` | Upload one bounded object; verify length and digest |
| `POST /api/v1/artifacts/{id}/revisions` | Commit an immutable manifest with an expected predecessor |
| `GET /api/v1/artifacts/{id}/revisions` | Revision history |
| `GET /api/v1/artifacts/{id}/revisions/{revision}` | Manifest for a fixed revision |
| `GET /api/v1/artifacts/{id}/revisions/{revision}/files/{path...}` | Reconstruct an authorized logical file |
| `GET /api/v1/artifacts/{id}/revisions/{revision}/preview` | Sandboxed HTML entrypoint |
| `GET /api/v1/artifacts/{id}/revisions/{revision}/download` | Portable archive |
| `DELETE /api/v1/artifacts/{id}` | Revoke access and schedule removal of unreferenced bytes |

Use existing agent bearer authentication for publication and browser-session authentication for opening artifacts. Preserve account/thread ownership checks on every resource. Serve manifests, indexes, and file ranges with bounded memory. Start with ordinary server-mediated uploads through `blob.Store`; direct B2 credentials in integrations are unnecessary.

Expose an `artifacts` feature flag through the existing capabilities response. An older FinaleChat deployment continues receiving ordinary chat. An integration that does not support artifacts continues working unchanged. Document the protocol in `docs/API.md`, `docs/openapi.json`, `AGENTS.md` and the integration helper documentation. A directory-publishing CLI helper should hide hash calculation, missing-object checks and commit retries from authors.

**4. Run uploaded viewers in a restricted iframe**

An uploaded viewer is executable integration code. Give it a dedicated preview response with server-enforced `Content-Security-Policy: sandbox allow-scripts`, and an iframe with `sandbox="allow-scripts"`. Omit `allow-same-origin`. The resulting opaque origin cannot access FinaleChat's DOM, cookies or storage. Keep existing inert attachment responses unchanged. Only the preview route permits its intended scripts, and it overrides the global frame denial with a policy allowing the FinaleChat parent.

The preview policy allows bundled inline scripts/styles and necessary data/blob images, and denies fetch/XHR/WebSocket connections, external resources, forms, nested frames, popups and parent navigation. Render the preview as its own HTTP document; putting it directly into the application's `srcdoc` would inherit the app's restrictive inline-script policy. Add WebAssembly execution permission only if that viewer capability is supported. Review self-navigation separately: iframe/CSP restrictions are not a complete outbound firewall, and the design must not claim they make arbitrary uploaded JavaScript incapable of disclosing data given to it.

The parent fetches authorized artifact files and transfers bytes through a small `MessageChannel` protocol. The viewer receives no API token, session cookie or general-purpose HTTP proxy. During bootstrap validate the exact iframe window, load generation and a per-instance nonce; opaque frames have origin `null`, so an origin check alone is insufficient. Revoke the channel on navigation, close, revision change and logout. Bind reads to the selected artifact/revision, canonicalize paths, reject traversal and limit request sizes and concurrency.

The inspection bridge supports only manifest/file reads, ready/error signals, presentation settings and navigation hints such as event sequence. A navigation hint cannot authorize access or execute an action. The trusted parent owns downloads, thread navigation, and refresh UI. New-tab viewing opens the trusted artifact container again rather than an unrestricted uploaded page. The separately granted interactive settings extension stages typed proposals and requires a deliberate Save/action in the trusted parent; ordinary viewers do not gain mutation access.

These choices follow the browser behavior documented for [iframe sandboxing](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/iframe), [CSP sandbox](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/sandbox), and [postMessage](https://developer.mozilla.org/en-US/docs/Web/API/Window/postMessage). A separate cookieless preview origin can be added as defense in depth, but is not needed to introduce an opaque sandbox with a server-enforced policy.

**5. Make eagent's viewer portable without creating a second runtime**

Extract a deterministic `internal/sessionview` projection from `internal/web/server.go`. It accepts reduced state, a capture time and explicit pricing/provenance inputs. It must not inspect process locks, consult the current wall clock or label an archived process as currently alive. Both the existing web server and the exporter call this projection; the live server layers current availability onto it separately.

Refactor the UI's data access behind a source interface: local HTTP/SSE, published artifact files, and locally selected files. Reuse the existing read-only chat, tasks, timeline, charts and tool renderers. The artifact source hides composers, question-answer actions, Stop/Resume, session creation and configuration writes. It shows configuration provenance as historical information.

For the first working export, package all source JSONL plus a `derived/summary.json` produced by the existing Go reducer. That cache makes the current UI usable without a server and avoids immediately implementing a competing JavaScript reducer. Stamp it with the source digest, sequence frontier and reducer version; discard it if they do not match. It is always rebuildable from the preserved source and declared context.

For browser-side replay to an arbitrary event, add a small Go-to-WebAssembly wrapper around the reducer/projection after extracting any unnecessary provider-client dependencies. Bundle its runtime with the versioned viewer, optionally embedded in `index.html`. This is the preferred extension over maintaining separate Go and JavaScript interpretations of task state. Measure compressed size and phone performance before making replay required; the exported summary provides an immediate first view. A WebAssembly module is a static viewer asset, not a running server.

Record price-table/version provenance if historical dollar estimates matter; today's catalog must not silently reprice yesterday's run. Include available resolved configuration and prompt provenance without credential values, and explicitly mark context unavailable in older sessions. Preserve unknown events and expose their raw data even when an old viewer does not recognize them.

Offline downloads must really open without FinaleChat or eagent. Bundle the viewer and ordinary files. Use a File API picker/drop adapter when opening `index.html` locally; do not assume `file://` fetches of sibling JSONL work. Offer a generated single-file HTML export with embedded data as a convenience for smaller sessions. The downloadable archive retains separate raw files regardless of that convenience export.

**6. Publish from the harness lifecycle**

Add an independent artifact publisher rather than tying publication to `eagent serve` or its HTTP connection. It can run during terminal, batch and browser-hosted sessions. It resolves the thread through the existing phone integration and also supports a one-shot export/publish command for old sessions.

Make artifact publication a separate explicit configuration opt-in, because the current chat mirror promises to send only user-visible conversation. Exact session backups additionally contain tool output, source excerpts and provider replay payloads. Do not silently turn on full-trace upload for everyone who already has a FinaleChat token. A redacted export must be labeled as such rather than claimed to be a byte-exact backup. The initial exact-backup artifact remains private to its owning account; public sharing is a separate feature.

Initial cadence: publish a changed checkpoint approximately every 30 seconds; request earlier publication after important milestones, rollover and shutdown. Coalesce requests on one asynchronous worker. No periodic upload is needed when data has not changed. Persist the last acknowledged revision and offsets locally, outside the exported dataset. Retry with backoff after network errors and reconcile on restart. A bounded shutdown flush is best effort; failed final publication remains pending for the next run or explicit publish command.

Capture a committed sequence frontier through the session writer and copy complete bytes consistently across rollover. Referenced output/attachment files must be finalized before inclusion or have their captured lengths/hashes recorded. If a referenced file is unavailable, keep the prior complete revision or publish an explicitly incomplete checkpoint; never claim it was included. Do not mutate or repair the live session merely to export it.

The UI displays **Saved through event N at time T** and, when appropriate, **Newer data available**. An archived open turn means it was open at capture time. Absence of `session.end` cannot prove the agent is still running or finished. A hard crash can lose work since the last acknowledged checkpoint; the timestamp makes the boundary visible.

**7. Make the artifact useful from chat**

Add an Under the hood action to `web/src/screens/Thread.tsx`, backed by a generic artifact container. On phones it opens a full-height sheet/page; on desktop it can open beside chat. The trusted container shows title, producer, saved time, revision selection and download controls. The integration supplies the actual visualization.

Preserve scroll position and filters during updates. Pin inspection to one immutable revision; offer Follow latest for users watching an active run. Do not silently swap a viewer version while someone reads. A newer compatible viewer can be explicitly paired with the same dataset while the original viewer remains available through its historical revision.

Use existing message `meta.seq` to add **Inspect this moment**. Validate the anchor against the artifact's session and sequence coverage, then focus its event/task. Older messages lacking an anchor simply open the session. A future standardized source reference can carry dataset identity, schema, event sequence and task ID without FinaleChat parsing events.

Eagent's initial approachable view should summarize the goal, milestones, tasks, elapsed time and model usage, with expandable commands, results, rollover dossiers and raw events. This gives curious readers an entry point while retaining detailed evidence for experienced users.

**8. Recovery and limits of the backup**

Preserve exact source files and verify them against the manifest on download/import. A recovery command should restore into a new directory, validate the complete sequence and show an offline replay first. Archive viewing never starts a session or executes tools.

Restoring a session record does not restore its working repository, unrecorded files, API credentials, installed tools, services or running processes. Attachments and full tool outputs are separate manifest entries precisely because JSONL alone is not the whole record. Exclude `.lock`, pending inbox commands, publisher state and secrets from the archive. Record the omission policy.

An eventual resume-as-fork command must treat old PIDs and process handles as historical, map paths explicitly, supply current configuration/credentials locally, and create new local/session/thread identities. Existing `closeInterrupted` can attempt to reap previous processes, so it must distinguish imported archives from a restart on the original machine. Preserve the original archive as evidence rather than rewriting its raw log in place.

**9. Claude Code and Codex use the same artifact platform**

The existing integrations have different coverage. The repository's `cli/finalechat` contains a dedicated Claude Code installer/uninstaller, hook handler, transcript parser and generic MCP server. Its installer accepts only `claude-code`; there is no dedicated Codex installer or automatic Codex archive publisher. README mentions of Codex describe generic CLI/API usage. The CLI's existing offline `selftest` passed during this review, but that does not establish that a real installed Claude Code session works end to end. The user has not tried these integrations.

| Integration | Existing capture | Artifact adapter |
| --- | --- | --- |
| eagent | Native JSONL, reducer, outputs and attachments; automatic chat mirror | Harness-managed checkpoint publisher and eagent viewer |
| Claude Code | Lifecycle hooks and main-transcript parsing for chat; MCP tools | Extend hooks to register transcript sources with the common publisher; package native transcripts and a Claude-specific viewer |
| Codex | Generic CLI/API/MCP access; no dedicated installer in this repository | Add explicit Codex setup and a separate supported capture/import adapter; package Codex data and a Codex-specific viewer |
| Other integrations | Optional | Publish the same manifest/files contract with their own renderer and schema |

Claude Code already passes `transcript_path` to hooks, and its `SubagentStop` hook provides a separate `agent_transcript_path`. Extend the current hook registration to account for subagent transcripts and compaction boundaries. Hooks enqueue work for one publisher per session; they do not upload the whole archive synchronously on every tool call. Preserve source UUIDs and subagent relationships, and reuse the current `meta.transcript_uuid` message anchor. The transcript layout is producer-version-dependent, so test it using versioned fixtures and preserve unknown fields. [Claude Code hook reference](https://code.claude.com/docs/en/hooks).

Keep the common uploader in the existing standard-library Python CLI, with small adapter functions for source discovery, capture, identity and viewer selection. Ship reusable viewer assets as versioned integration resources; do not ask a model to regenerate HTML on every update. Explicit session references must take priority over the CLI's per-directory fallback, especially when Claude Code and Codex run concurrently in the same checkout.

Codex supports stdio MCP servers, so the generic FinaleChat tool interface can be registered through its supported MCP setup. This makes tools available to the model; it is not an automatic feed of every session event. A dedicated `install codex` would be new work. [OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

For Codex capture, evaluate App Server against the installed version: it provides stored-thread reads without resuming and structured events for sessions the client manages. Use those interfaces for a first supported reader/recorder where available. Do not assume that an MCP connection or a new App Server process can passively observe every existing desktop/CLI session. An explicit local session import is a separate adapter with version checks and a user-selected scope. [OpenAI App Server documentation](https://learn.chatgpt.com/docs/app-server).

Distinguish a recording of exposed Codex events from a complete native continuation archive. Declare `inspection` and `native_restore` capabilities independently, with documented coverage. Apply the same rule to Claude Code: possession of transcript JSONL alone does not establish that another installed version can resume it. Keep native source files when the integration can capture them, and treat any normalized cross-provider timeline as derived data.

Before advertising either integration as ready, exercise installation in an isolated settings directory, preservation of unrelated hooks/MCP entries, one real session's prompt/tool/final-message flow, restart/continuation, and uninstall. Test remote mode separately from ordinary chat mirroring and archival publication. Claude's current remote question path injects the phone answer by denying `AskUserQuestion` with an explanatory reason, so its actual behavior needs an end-to-end check. Live trials should use a deliberately chosen test thread; this review did not install integrations, alter user settings or send messages to FinaleChat.

Use eagent as the first artifact producer and Claude Code as the second before freezing version 1 of the generic format. Their different event schemas and nested-session structures will reveal accidental eagent assumptions early. Add Codex capture after its supported source and coverage are verified; it does not need to block durable artifacts for the existing integrations.

**Implementation sequence and acceptance criteria**

| Stage | Deliverable | Required proof |
| --- | --- | --- |
| 1. Portable eagent export | Extract projection/source adapter; export viewer, complete logs, references and derived cache | Turn off local server/network; inspect the example archive with matching tasks, events and token totals |
| 2. FinaleChat artifact resources | New migration/store/API, immutable blobs/revisions, atomic commit, download and restricted preview | Failed upload cannot break current revision; another user cannot read it; ordinary attachment cleanup cannot remove it |
| 3. Product integration and publisher | Thread action, event anchors, proactive eagent updates, lag/status, capability detection | Inspect an evolving session, disconnect publisher, reload FinaleChat and still inspect the last saved revision; reconnect without duplication or regression |
| 3b. Second producer and integration validation | Claude Code publisher/viewer, isolated install tests and a real-session trial | Main/subagent transcripts remain linked, capture continues across compaction, ordinary chat still works, archive opens after Claude exits |
| 4. Replay and recovery | Shared reducer in browser where appropriate, viewer replacement, verified import and offline replay | New viewer reuses unchanged source; old viewer remains accessible; restored source hashes and replay results match |
| 5. Codex adapter | Dedicated setup plus a verified capture/import source and declared coverage | Inspect a captured session; demonstrate separately whatever native restoration the adapter claims |

For eagent, primary changes belong in `internal/web/server.go`, `internal/web/static/app.js`, `internal/web/static/index.html`, new `internal/sessionview` and export/publisher packages, `internal/harness/phone.go` and lifecycle hooks, `internal/finalechat`, `internal/config`, and `cmd/eagent/main.go`. Add narrowly scoped tests for committed boundaries, rollover, missing referenced files, exact reconstruction and publisher retries.

For FinaleChat, primary changes belong in a new numbered `internal/db/migrations` file, new artifact store/API files, `internal/blob` only where storage needs extension, `internal/api/server.go`, the preview-specific policy in `internal/api/middleware.go`, `internal/bus/bus.go`, `web/src/screens/Thread.tsx`, a new artifact container, `web/src/lib/types.ts` and store/API helpers, and the API/CLI documentation. Avoid caching private previews or credentials in the service worker; any explicit offline artifact cache needs logout and deletion handling.

Validation includes concurrent/out-of-order publications, B2 failures and deletion retries, path traversal and cross-account hash references, malicious preview scripts and forged bridge messages, multiple subsessions and torn final lines, large files and unknown event types, real phone/browser layout, and a browser test with all external network access disabled. Run each repository's existing checks for implementation changes; use a dedicated test database for FinaleChat. This review used source inspection, read-only requests and the existing offline CLI selftest.
