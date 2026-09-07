# Finalechat

Read `README.md` for the layout and `AGENTS.md` for the product surface agents
use. Key facts for working in this repo:

- Go server (`cmd/finalechat`, `internal/`), PostgreSQL via pgx, no ORM; every
  SQL statement lives in `internal/store`. Schema changes are new numbered
  files in `internal/db/migrations/`; never edit an applied migration.
- Attachment bytes never touch PostgreSQL: `internal/blob` (B2 native API,
  v4 endpoints; bucket-scoped keys are only accepted by v4) stores objects
  under `a/<attachment id>/…`; the `attachments` table holds metadata and
  object keys. Tests use the in-memory store; `internal/blob/b2_test.go` runs
  live when `FINALECHAT_B2_TEST_*` is set.
- Real-time fan-out uses PostgreSQL NOTIFY (`internal/bus`); handlers publish
  after their transaction commits. Long-poll handlers subscribe *before* they
  query so nothing is missed.
- The web app (`web/`, Vite + React + TypeScript, no UI framework) builds into
  `internal/webassets/dist`, which the Go binary embeds. `web/sw/sw.ts` is the
  service worker and has its own tsconfig (WebWorker lib).
- `AGENTS.md`, `docs/API.md`, `docs/openapi.json`, `cli/finalechat`,
  `cli/install.sh` and `skill/finalechat/SKILL.md` are embedded and served by
  the server (`assets.go`); the literal origin `https://www.finalechat.com` in
  them is rewritten to `FINALECHAT_BASE_URL` at serve time. Keep them in sync
  with the handlers in `internal/api/server.go` when routes change.
- `cli/finalechat` must stay a single Python 3.9+ file using only the standard
  library; it is also the Claude Code hook handler and the MCP server.
- Tests: `make dev-db` then `make test`. The Go suite in `internal/api` runs
  against `FINALECHAT_TEST_DATABASE_URL` and resets that database's `public`
  schema. `make check` mirrors the CI gate.
- Delivery: push to `main` → Woodpecker (`.woodpecker.yaml`) → immutable
  image → Flux promotion in the epsilon repo. Never add cluster credentials,
  `kubectl`, or production manifests here.
- Style: gofmt; small handlers with explicit validation and stable JSON error
  codes; UI copy is short and plain; timestamps are UTC in the API and local
  in the app.
