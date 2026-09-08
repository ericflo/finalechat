# Session archives and settings connectors

FinaleChat stores integration websites as immutable artifacts beside a conversation. An artifact contains a self-contained HTML viewer, native source files, and a manifest with SHA-256 hashes and bounded content-addressed chunks. Repeated publication preserves history and reuses unchanged bytes. Downloaded archives remain usable after the original process or machine disappears.

Settings control is a separate opt-in. A local companion publishes a typed settings descriptor and snapshots, then receives durable commands over outbound HTTPS. Pair its requested scopes in FinaleChat's Settings screen. The integration page can stage changes; FinaleChat's own Save button submits the displayed proposal. A publisher token cannot submit human settings commands, and the iframe receives neither token nor general API access.

Custom editors use `finale.settings.read()` for their bounded resource view and `finale.settings.propose(proposal)` to stage a versioned, typed change. Call `finale.settings.clear()` when subsequent form edits invalidate that proposal; this clears the host draft without submitting a command. `finale.settings.onResult(callback)` receives durable command outcomes. Match each result to its proposal and preserve newer local edits when acknowledgements arrive late. Eagent's archive embeds its actual local configuration editor through this transport, including model pickers, prompt and named-configuration controls, and reviewed route tests.

Opening **Edit current settings** creates a 30-minute editing session for that exact current artifact and resource. New transcript revisions can be published while the editor stays open. Historical pages cannot start a new editing session; expiry, revocation, deletion or a changed runtime generation ends the authorization. FinaleChat retains the editing identifier outside the iframe and records the original source revision on each command. Settings version checks still prevent stale writes, and background refreshes never silently rebase an existing edit set.

Deleting an artifact or its thread also cancels queued commands submitted from that artifact. An already claimed command becomes `unknown`, and its lease and later acknowledgements are fenced: deletion cannot guarantee that a local write had not already happened. Completed results and their audit remain with the settings resource. Commands submitted independently from the resource keep their own authorization.

An adapter may return a `finalechat.settings-undo/v1` review in a successful command's `result.undo`, containing the original `command_id`, a `restore_sha256`, and either the inverse configuration `edits` or a named `resource` review. When its descriptor advertises `settings.undo`, FinaleChat offers **Review undo**. This stages a new command with the current settings version and the reviewed command/digest; submission still requires the trusted action button. The adapter must match that review to its private journal, check the affected values and current grants, and apply the reversal conditionally. Undo never grants permission to overwrite unrelated changes or silently replay a paid action. Eagent implements this contract for settings, prompts and bundles.

## Websites from other integrations

Prepare a directory containing a self-contained HTML entrypoint and the data it reads. Then publish it to the integration's exact session thread:

```sh
finalechat artifact upload ./prepared-website --thread ext:my-agent:SESSION_ID \
  --key inspector --title "Session explorer" --source session.jsonl \
  --dataset ./dataset.json --append-only-sources --json
```

`dataset.json` is an object describing the source format and identity, for example `{"format":"my-agent.events/v1","session_id":"SESSION_ID"}`. Repeat `--source` for recoverable source files and `--context` for provenance files. Add `--settings-entrypoint settings/index.html` when the integration also supplies a settings page. These paths are relative to the prepared directory. The remaining files are retained as assets. Uploading a settings page does not pair a connector or authorize it to write settings.

The CLI copies the prepared files into private staging, computes whole-file and chunk hashes, uploads missing chunks, and commits a revision. It rejects symlinks, unsafe paths, invalid manifests, and files changing during capture. Prepare a dedicated output directory: every file in an unmanifested input directory is included. HTML must already embed its executable JavaScript and CSS and use the [artifact SDK](../sdk/finale-artifact.js) for data access; this command packages files but does not bundle module imports or rewrite a site's network dependencies.

Run the same command again whenever the integration has an update. An unchanged capture reuses its revision. Failed uploads retain the exact capture and idempotency key for retry. `--append-only-sources` prevents a new capture from dropping or rewriting previously published source bytes; omit it for datasets whose files are intentionally replaced. A deleted remote artifact stays deleted until the same upload command includes `--recreate`. The explicit thread, server URL, and artifact key identify the durable publisher state; there is no per-directory chat-thread fallback.

To inspect the portable package before uploading, or publish a manifest an integration already produced:

```sh
finalechat artifact pack ./prepared-website -o ./new-package --source session.jsonl --dataset ./dataset.json
finalechat artifact verify ./new-package
finalechat artifact upload ./new-package --thread ext:my-agent:SESSION_ID --key inspector --append-only-sources
finalechat artifact restore ./new-package -o ./new-source-recovery
```

An existing `manifest.json` is preserved, and only its declared files are copied. Its metadata and roles take precedence, so omit packaging overrides when uploading or repacking it. Source recovery verifies bytes and writes a new directory; it never executes the website or starts an agent.

## Claude Code

The existing hooks/MCP integration remains available:

```sh
finalechat install claude-code
```

To archive a particular project's sessions proactively, run from that project:

```sh
finalechat install claude-code --artifacts --project .
finalechat connector pair claude-code --project . --scope project
```

The installer adds SessionStart, Stop, SessionEnd, SubagentStop, PreCompact and ConfigChange capture notifications alongside the existing chat/status hooks. Hooks register the native transcript and wake a companion; uploading never blocks the hook. The companion periodically copies complete native JSONL records, including the parent's subagent files. An incomplete trailing record is retained locally until it is complete and never corrupts the published revision.

Add `--scope user` or `--scope project_local` to pairing to expose those distinct resources; repeat the flag to pair several. The first scope supplies a session artifact's settings binding. Every paired resource is also accessible directly from FinaleChat's Settings screen. Permission/sandbox fields have a separate capability class. Credentials and executable helper values are excluded from snapshots. Unknown fields are listed as unsupported and preserved in local files.

The adapter distinguishes saved values from defaults resolved through observed user/project/local/managed files. It does not claim to observe cloud/MDM policy or the environment and loaded configuration of another Claude process. Saved model/effort changes are reported for new or resumed sessions, and fields requiring a restart are labeled separately. Claude's native managed policy remains authoritative.

`FINALECHAT_MIRROR=off` disables hook chat mirroring independently of archive registration and the settings companion. Existing remote-mode behavior remains controlled by FinaleChat's remote-mode setting.

## Codex

```sh
finalechat install codex
finalechat artifact enable codex --project .
finalechat connector pair codex --project .
```

The installer registers the existing FinaleChat MCP tools through native, conditional configuration edits. Reinstalling preserves unrelated servers and the existing FinaleChat tool approval policy. It forwards the names `FINALECHAT_TOKEN`, `FINALECHAT_URL`, `FINALECHAT_THREAD`, `FINALECHAT_AGENT`, `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` through Codex's MCP environment allowlist, without writing their values into `config.toml`.

Codex calls carrying native turn metadata automatically use `ext:codex:THREAD_ID`, so chat messages and the rollout archive land in the same thread. Explicit tool `thread` arguments and `FINALECHAT_THREAD` take precedence; clients without native metadata can use either to choose the archive's thread. The installer preserves Codex's own [per-tool approval settings](https://learn.chatgpt.com/docs/extend/mcp#other-configuration-options). Interactive Codex can request approval for a chat tool; a headless client with approval policy `never` needs an existing explicit tool approval or rejects that call. Registration does not grant that approval.

 Artifact publication reads native rollout JSONL files for the explicitly opted-in project, matching their native session ID and working directory before capture. It observes both active and archived rollout directories, including existing sessions for that project. MCP chat tools and transcript archival are independent capabilities.

Codex settings use the installed App Server's `config/read`, `configRequirements/read`, and conditional `config/batchWrite` APIs. The adapter never rewrites TOML itself. Its current writable scope is **user defaults**; project/profile/managed origins are visible through native provenance and unsupported edits are locked or listed. Model, effort, summary, verbosity, sandbox/approval preferences and other declared fields are typed and validated again locally. Provider choices reference configurations established locally; provider credentials are excluded.

A settings connector does not own unrelated Codex desktop or terminal threads. It reports persistent defaults and unconfirmed runtime adoption, and never sends an empty model turn to change settings. Installed versions lacking the required native APIs fail closed for settings; original exported source files remain independent of those APIs.

## Companion lifecycle

Pairing starts a detached companion after the local scope has been checked. Approval is completed in FinaleChat. For an OS supervisor, pair with `--foreground` and supervise this command instead:

```sh
finalechat connector run claude-code --project /path/to/project
# or
finalechat connector run codex --project /path/to/project
```

`connector start`, `connector stop`, and `connector status` use the same provider/project arguments. A process lock elects one companion per installation. State and scoped credentials are private files under the FinaleChat configuration directory. Network failures retain publication intents and command journals for retry. Stop lets the current operation finish and then disconnects; use **Disconnect and revoke access** in FinaleChat to revoke the credential and pending commands.

Archive uploading can be toggled separately with `artifact enable` / `artifact disable`. Settings commands still work when uploading or chat mirroring is disabled. A continuously running companion is needed for settings changes while the agent is idle. Detached companions are not automatically installed as boot services; an OS supervisor can run the foreground command.

Publishers retain their remote artifact identity. Deleting an artifact or its thread pauses that session's automatic publication; it cannot quietly recreate the record in a new thread. To resume deliberately, use `finalechat artifact publish PROVIDER SESSION_ID --project . --recreate`. The old local publication identity is retained as recovery metadata. `connector status` includes publication conflicts and deletion status.

A lost upload acknowledgement retries the original capture and idempotency key. A competing publisher's newer revision requires a fresh capture that contains every published native source file as an exact byte prefix. Truncated or divergent local histories leave the remote record unchanged. Native archive export/publication continues when settings APIs are unavailable, with the missing settings snapshot explicitly labeled.

## Export, verify and recover native sources

```sh
finalechat artifact track claude-code SESSION_ID --transcript /path/session.jsonl --project .
finalechat artifact track codex THREAD_ID --project .
finalechat artifact publish codex THREAD_ID --project .
finalechat artifact export claude-code SESSION_ID --transcript /path/session.jsonl --project . -o ./new-archive
finalechat artifact export codex THREAD_ID --project . -o ./new-archive
finalechat artifact verify ./new-archive
finalechat artifact restore ./new-archive -o ./new-recovery
```

Codex can resolve a supported stored thread through its native read-only API; an explicit `--transcript` also accepts a matching native rollout. Track registers a session locally; it does not by itself enable proactive uploading. Publish is an explicit one-shot publication and does not change the persistent opt-in.

To view a download offline, extract its ZIP, open `index.html`, and choose the extracted directory. The embedded SDK verifies the chunks it reads using Web Crypto. Native messages, tool inputs/results, subagents, and unknown event types remain available for inspection. The viewer bounds in-memory rendering; the original complete files remain downloadable even when its display limit is reached.

The session container pins the revision you opened. **Follow latest** opts into new snapshots with the same viewer and dataset identity; it pauses for changed viewer files. The shipped viewers retain their section, filters, event position, pagination and scroll across compatible updates. Choose **Viewer for this dataset** to explicitly pair fixed source data with renderer files from another saved revision of the same artifact. Both dataset format and schema must agree. This does not alter stored history: you can download the original archive or a composed archive recording both revision IDs. Composed viewers have no current settings control.

Custom viewers can call `finale.viewState.read()` and `finale.viewState.write(value)` to retain up to 16 KiB of JSON presentation state while the trusted container remains open. Include a state format version and dataset/session identity; validate them before restoring. This state is ephemeral and scoped to the artifact, with no settings or file mutation authority. The SDK caches the immutable manifest for each channel. Built-in viewers display at most 50,000 events, 32 MiB of source records, or 128 source files and label any limited prefix; full files remain downloadable through FinaleChat’s **Saved revisions and files** or **Download archive** controls.

Messages in threads with archives offer **Inspect this moment**. A producer can include `meta.source_anchor` with `dataset_format`, `session_id`, and exactly one of `seq`, `event_id`, `message_id`, or `file` plus `line`. Identifiers are bounded to 200 UTF-8 bytes, and file/line selectors name source files in the selected revision. The SDK's `finale.anchor()` passes the hint into the viewer; an absent or display-limited event is reported explicitly. Eagent and Claude publish native anchors and retain support for older sequence/UUID metadata. Without native metadata, the viewer searches for the exact FinaleChat message ID in captured records, including Codex's recorded MCP send result.

**Find chat message** calls `finale.reveal(anchor)`. FinaleChat resolves it only within the artifact's own thread and offers **Show matching chat message** in trusted UI; the iframe cannot navigate the app on its own. Messages outside the recent page open in a separately labeled selected-message section. Some native events have no mirrored chat message, and some chat messages are absent from a captured transcript; those cases remain explicit.

Verify checks all file and chunk digests without executing the website. Restore copies verified **source** files into a fresh directory and records a recovery manifest. It does not install settings, restore credentials, recreate external tool effects, resume a process, or restore a live settings binding. Native session recovery is distinct from replaying the original workflow's side effects.

## Development

The distributed CLI remains a single Python 3.9+ file using only the standard library. Edit `cli/integrations.py` and `cli/assets/`, then run `python3 scripts/build-cli-integrations.py`. The build embeds those sources and the artifact SDK into `cli/finalechat`; the installed file needs no sibling modules or assets. `make test-cli` checks synchronization and runs the offline adapter tests. Set `FINALECHAT_TEST_NATIVE_CODEX=1` to additionally test the installed Codex configuration APIs using an isolated temporary native home, without model calls.

`make test-browser` runs the artifact SDK, opaque iframe, trusted settings controls and offline corruption checks in Chromium. It starts Vite on a temporary loopback port and mocks every API request, so it needs no account, database or existing server. Install Playwright and its Chromium browser in a separate tools directory, then set `FINALECHAT_PLAYWRIGHT_MODULE` to that installation's `node_modules/playwright/index.mjs`. The test also verifies that iframe staging never submits a command, invalid input clears a previous proposal, long text stays intact, stale edits require explicit review, and delayed acknowledgements preserve newer input.

Eagent additionally supports `eagent connector pair-session SESSION_ID` from the session project. Approve that separate grant to expose **Live session settings** in the chat: task concurrency and narrator check/quiet timing apply through the running harness, with generation/version checks and native acknowledgements. These settings apply only to that process; resumed sessions load project defaults. The native log preserves runtime start/change events, and archives capture runtime settings context. Session controls cannot be drafted or queued for a later connection. Other eagent settings continue to use the full project-default settings website.

## Installed-client regression checks

`make test-native` exercises installed Claude Code and Codex in temporary homes against local fake model and FinaleChat APIs. It checks real local tool execution, Claude hook mirroring and phone answers, Codex MCP authentication and per-call thread routing, exact native source export, resume without rewriting the captured prefix, and preservation of unrelated hooks/settings/MCP registrations on reinstall and uninstall. It also runs the optional native Codex configuration API tests. Missing native clients are skipped. Synthetic provider usage in these fixtures is not a billed model request.

Verified with Claude Code 2.1.263 and Codex 0.153.4. The Claude fixture uses the native streaming input/approval channel to expose `AskUserQuestion`; ordinary print mode does not advertise that interactive tool. A phone answer is delivered as an explained native tool denial, so the model sees the answer and the terminal question does not run. The adapter does not claim to change the running model or effort through saved defaults.

## Capability boundaries

| Integration | Remote defaults | Live controls | Kept local or unavailable |
| --- | --- | --- | --- |
| eagent | Full shared project editor: routes/models/fallbacks, prompts, bundles, scheduling and mirror/archive preferences | Separately paired task concurrency and narrator timing, acknowledged by the running harness | Credentials, arbitrary files, and hot changes to model routes |
| Claude Code | Declared user/project/project-local model, effort, presentation, permission and sandbox preferences | Saved defaults only; native runtime adoption remains unconfirmed | Credentials, hooks/helper commands, unknown fields and unobservable cloud/MDM policy |
| Codex | Declared user model, effort, context, presentation, sandbox/approval and history defaults through native APIs | Saved defaults only; no ownership of another desktop/terminal thread | Credentials/MCP helpers, direct project/profile writes and unsupported native values |

Unknown settings remain in their native files. Permission-related preferences require their own paired capability class and remain subject to the native product's policy. Native version and provenance accompany snapshots; a saved result states when the adapter expects the change to take effect without claiming to have observed another process. Archives preserve original session data independently of native settings API availability. Eagent local export also tolerates invalid project defaults and labels its missing settings context; network publication waits for a valid configuration to resolve the intended destination and opt-in.
