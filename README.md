# Finalechat

**Your agents message you.** Finalechat is a small, fast web app you install
on your phone. Every coding agent you run (Claude Code, Codex, anything that
can make an HTTP request) posts the messages it would otherwise only print in
a terminal, asks you multiple-choice questions, and can wait for your reply.
Questions and important messages arrive as push notifications.

- One thread per agent session, addressed by an id the agent chooses.
- Markdown messages, `important` flag, per-thread mute, archive.
- Questions with options, descriptions, multi-select and free text; the agent
  long-polls for the answer.
- Screenshots and files on messages in both directions (up to 8 per
  message, 10 MiB each), stored in a private Backblaze B2 bucket with
  metadata in PostgreSQL; agents can fetch what you send and MCP tools hand
  images to the model directly.
- **Remote mode**: a switch on your phone that makes Claude Code wait for your
  phone replies instead of the terminal.
- Real-time updates over server-sent events; Web Push with VAPID; installable
  PWA with app badge.
- Agent integrations: raw HTTPS (`/AGENTS.md`), a zero-dependency Python CLI
  (`curl -fsSL https://www.finalechat.com/install.sh | sh`), Claude Code hooks
  and an MCP server (`finalechat install claude-code`), and an Agent Skill.

Production: <https://www.finalechat.com>. Agents start at
<https://www.finalechat.com/AGENTS.md>; the API reference is
<https://www.finalechat.com/api/>.

## Layout

| Path | What |
| --- | --- |
| `cmd/finalechat` | Server entry point (`serve`, `vapid`, `version`) |
| `internal/api` | HTTP API, SSE, static delivery, notification policy |
| `internal/store` | PostgreSQL queries and types |
| `internal/bus` | LISTEN/NOTIFY event fan-out across replicas |
| `internal/push` | Web Push sender |
| `internal/blob` | Attachment bytes: Backblaze B2 native API client and an in-memory store |
| `internal/imaging` | Image decoding and thumbnails |
| `internal/db` | Pool and embedded migrations |
| `web/` | Vite + React PWA (built into `internal/webassets/dist`) |
| `cli/finalechat` | CLI, Claude Code hook handler, MCP server (Python, stdlib) |
| `skill/finalechat` | Claude Code Agent Skill |
| `AGENTS.md`, `docs/API.md`, `docs/openapi.json` | Agent-facing docs, served by the server |

## Develop

```sh
make dev-db          # PostgreSQL 16 in Docker on 127.0.0.1:55432
make build           # web app + server binary in bin/finalechat
make run             # server on http://127.0.0.1:8787 (or: make web-dev for HMR)
make test            # Go integration suite against finalechat_test, web typecheck, CLI checks
```

The first account to register becomes the owner; registration then closes
unless `FINALECHAT_INVITE_CODE` is set (when it is set, even the first
account needs it). Generate push keys once with `bin/finalechat vapid` and
put them in the environment. Set `FINALECHAT_BLOB_STORE=memory` locally to
try attachments without B2.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `FINALECHAT_ADDR` | `:8080` | Listen address |
| `FINALECHAT_BASE_URL` | `http://localhost:8080` | Public origin used in docs, snippets and push links |
| `FINALECHAT_CANONICAL_HOST` / `FINALECHAT_REDIRECT_HOSTS` | unset | Redirect browser navigations on alias hosts to the canonical one |
| `FINALECHAT_VAPID_PUBLIC_KEY` / `FINALECHAT_VAPID_PRIVATE_KEY` | unset | Web Push keys; push is disabled without them |
| `FINALECHAT_VAPID_SUBJECT` | `mailto:hello@finalechat.com` | Contact sent to push services |
| `FINALECHAT_INVITE_CODE` | unset | Gates registration (including the first account) behind a code |
| `FINALECHAT_B2_KEY_ID` / `FINALECHAT_B2_KEY` / `FINALECHAT_B2_BUCKET` | unset | Backblaze B2 bucket-scoped key for attachments; attachments are disabled without them |
| `FINALECHAT_BLOB_STORE` | `b2` when keys are set, else `disabled` | `memory` keeps attachments in process memory for local development |
| `FINALECHAT_SECURE_COOKIES` | `true` | Set `false` for plain-HTTP development |
| `FINALECHAT_TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `FINALECHAT_SESSION_TTL` | `2160h` | Sliding browser session lifetime |
| `FINALECHAT_LOG_JSON` / `FINALECHAT_LOG_LEVEL` | `true` / `info` | Logging |

## Deploy

A push to `main` runs the Woodpecker pipeline in `.woodpecker.yaml` (Go suite
against a throwaway PostgreSQL, web typecheck and build, CLI checks) and then
publishes one immutable image from `Dockerfile`. Flux in the
[epsilon](https://github.com/ericflo/epsilon) repository promotes the image;
runtime configuration, secrets, ingress and the database live there under
`apps/finalechat/`.
