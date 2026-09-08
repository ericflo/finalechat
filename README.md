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
- A live status line per thread ("running the test suite…", shown as a
  typing indicator with a timer) that lapses on its own, so the app never
  shows stale activity.
- Screenshots and files on messages in both directions (up to 8 per
  message, 10 MiB each), stored in a private Backblaze B2 bucket with
  metadata in PostgreSQL; agents can fetch what you send and MCP tools hand
  images to the model directly.
- **Remote mode**: a switch on your phone that makes Claude Code wait for your
  phone replies instead of the terminal.
- **New session from your phone**: when an eagent is running in a project
  (`eagent serve`, a session, or `eagent connector run`), the inbox offers
  **New session**; type the first message and the session starts on that
  machine and opens as a thread.
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
| `docs/TERMS.md`, `docs/PRIVACY.md` | Terms and privacy policy of the hosted service, shown at `/terms` and `/privacy` |

## Develop

```sh
make dev-db          # PostgreSQL 16 in Docker on 127.0.0.1:55432
make build           # web app + server binary in bin/finalechat
make run             # server on http://127.0.0.1:8787 (or: make web-dev for HMR)
make test            # Go integration suite against finalechat_test, web typecheck, CLI checks
```

Registration is controlled by `FINALECHAT_SIGNUP`. The default, `first`, is
right for a personal server: the first account to register becomes the owner
and registration then closes. `open` lets anyone create an account (what
<https://www.finalechat.com> runs); `invite` requires the code in
`FINALECHAT_INVITE_CODE` for every registration, including the first; and
`closed` refuses all of them. Generate push keys once with
`bin/finalechat vapid` and put them in the environment. Set
`FINALECHAT_BLOB_STORE=memory` locally to try attachments without B2.

Every account can use up to `FINALECHAT_ATTACHMENT_QUOTA_BYTES` of attachment
storage (5 GiB by default; `GET /me` reports usage) and can delete itself from
Settings → Account. `docs/TERMS.md` and `docs/PRIVACY.md` describe the hosted
service and are linked from the sign-up form; edit them before running your
own public instance.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `FINALECHAT_ADDR` | `:8080` | Listen address |
| `FINALECHAT_BASE_URL` | `http://localhost:8080` | Public origin used in docs, snippets and push links |
| `FINALECHAT_CANONICAL_HOST` / `FINALECHAT_REDIRECT_HOSTS` | unset | Redirect browser navigations on alias hosts to the canonical one |
| `FINALECHAT_VAPID_PUBLIC_KEY` / `FINALECHAT_VAPID_PRIVATE_KEY` | unset | Web Push keys; push is disabled without them |
| `FINALECHAT_VAPID_SUBJECT` | `mailto:hello@finalechat.com` | Contact sent to push services |
| `FINALECHAT_SIGNUP` | `first` (`invite` when a code is set) | Registration mode: `first`, `open`, `invite` or `closed` |
| `FINALECHAT_INVITE_CODE` | unset | The code registrations must present in `invite` mode |
| `FINALECHAT_ATTACHMENT_QUOTA_BYTES` | `5GiB` | Attachment storage per account (`0` for no cap; accepts `500MiB`, `2G`, or a plain byte count) |
| `FINALECHAT_B2_KEY_ID` / `FINALECHAT_B2_KEY` / `FINALECHAT_B2_BUCKET` | unset | Backblaze B2 bucket-scoped key for attachments; attachments are disabled without them |
| `FINALECHAT_BLOB_STORE` | `b2` when keys are set, else `disabled` | `memory` keeps attachments in process memory for local development |
| `FINALECHAT_SECURE_COOKIES` | `true` | Set `false` for plain-HTTP development |
| `FINALECHAT_TRUST_PROXY` | `true` | Take the client address from `X-Forwarded-For` (set `false` when the server is reached directly) |
| `FINALECHAT_TRUSTED_PROXY_HOPS` | `1` | How many trusted proxies append to `X-Forwarded-For`; the client is the Nth entry from the right, and `X-Real-Ip` is honoured only at `1` |
| `FINALECHAT_SESSION_TTL` | `2160h` | Sliding browser session lifetime |
| `FINALECHAT_SHUTDOWN_DELAY` | `3s` | How long `/readyz` fails before connections close on shutdown, so a load balancer drains first |
| `FINALECHAT_LOG_JSON` / `FINALECHAT_LOG_LEVEL` | `true` / `info` | Logging |

## Native session archives and settings

Eagent, Claude Code and Codex can publish permanent native-session websites and expose
scoped settings controls in FinaleChat. Archives preserve immutable revisions and
downloadable original JSONL files. Settings use a separately paired outbound
connector, a durable command queue, and FinaleChat's own Save button.

See [integration setup, lifecycle, verification and recovery](docs/integrations.md)
and the [artifact protocol](docs/design/session-artifacts.md). Other integrations
can package and publish a prepared website with `finalechat artifact pack` and
`finalechat artifact upload --thread ext:SESSION_ID`; see the integration guide
for source recovery and append-only history options. The standalone CLI
is generated from the integration sources with `make cli`; `make test-cli`
checks that the generated file is synchronized. `make test-native` additionally
exercises installed Claude Code and Codex against local fixture APIs in temporary
homes, including tools, hooks/MCP, exact export, resume and installer preservation.

## Deploy

Every push and pull request runs the GitHub Actions workflow in
`.github/workflows/ci.yml` (Go suite against PostgreSQL, web typecheck and
build, CLI checks). A push to `main` additionally runs the Woodpecker
pipeline in `.woodpecker.yaml` inside the production cluster and publishes
one immutable image from `Dockerfile`, which also gates the image on the same
test suite. Flux in the private epsilon repository promotes the image;
runtime configuration, secrets, ingress and the database live there.

To run your own instance you need PostgreSQL 16, the binary (or the image
from `Dockerfile`) with `DATABASE_URL` set, and optionally VAPID keys for push
and a Backblaze B2 bucket for attachments. Migrations apply on start.

## License

MIT; see `LICENSE`. Security reports: see `SECURITY.md`. Contributions: see
`CONTRIBUTING.md`.
