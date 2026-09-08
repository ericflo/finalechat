# Session archives and settings connectors

FinaleChat stores integration websites as immutable artifacts beside a conversation. An artifact contains a self-contained HTML viewer, native source files, and a manifest with SHA-256 hashes and bounded content-addressed chunks. Repeated publication preserves history and reuses unchanged bytes. Downloaded archives remain usable after the original process or machine disappears.

Settings control is a separate opt-in. A local companion publishes a typed settings descriptor and snapshots, then receives durable commands over outbound HTTPS. Pair its requested scopes in FinaleChat's Settings screen. The integration page can stage changes; FinaleChat's own Save button submits the displayed proposal. A publisher token cannot submit human settings commands, and the iframe receives neither token nor general API access.

Opening **Edit current settings** creates a 30-minute editing session for that exact current artifact and resource. New transcript revisions can be published while the editor stays open. Historical pages cannot start a new editing session; expiry, revocation, deletion or a changed runtime generation ends the authorization. FinaleChat retains the editing identifier outside the iframe and records the original source revision on each command. Settings version checks still prevent stale writes, and background refreshes never silently rebase an existing edit set.

Deleting an artifact or its thread also cancels queued commands submitted from that artifact. An already claimed command becomes `unknown`, and its lease and later acknowledgements are fenced: deletion cannot guarantee that a local write had not already happened. Completed results and their audit remain with the settings resource. Commands submitted independently from the resource keep their own authorization.

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

The installer registers the existing FinaleChat MCP tools in Codex. Artifact publication reads native rollout JSONL files for the explicitly opted-in project, matching their native session ID and working directory before capture. It observes both active and archived rollout directories, including existing sessions for that project. MCP chat tools and transcript archival are independent capabilities.

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

Verify checks all file and chunk digests without executing the website. Restore copies verified **source** files into a fresh directory and records a recovery manifest. It does not install settings, restore credentials, recreate external tool effects, resume a process, or restore a live settings binding. Native session recovery is distinct from replaying the original workflow's side effects.

## Development

The distributed CLI remains a single Python 3.9+ file using only the standard library. Edit `cli/integrations.py` and `cli/assets/`, then run `python3 scripts/build-cli-integrations.py`. The build embeds those sources and the artifact SDK into `cli/finalechat`; the installed file needs no sibling modules or assets. `make test-cli` checks synchronization and runs the offline adapter tests. Set `FINALECHAT_TEST_NATIVE_CODEX=1` to additionally test the installed Codex configuration APIs using an isolated temporary native home, without model calls.

`make test-browser` runs the artifact SDK, opaque iframe, trusted settings controls and offline corruption checks in Chromium. It starts Vite on a temporary loopback port and mocks every API request, so it needs no account, database or existing server. Install Playwright and its Chromium browser in a separate tools directory, then set `FINALECHAT_PLAYWRIGHT_MODULE` to that installation's `node_modules/playwright/index.mjs`. The test also verifies that iframe staging never submits a command, invalid input clears a previous proposal, long text stays intact, stale edits require explicit review, and delayed acknowledgements preserve newer input.
