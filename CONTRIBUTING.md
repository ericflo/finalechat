# Contributing

Thanks for helping. Finalechat is small on purpose, so the bar is "does this
make the app or the agent surface better without making either bigger than it
needs to be".

## Before you start

Open an issue for anything beyond a small fix so the shape can be agreed
first. Bug reports with a reproduction (a `curl` transcript, a screenshot, or
a failing test) are the fastest to act on.

## Developing

```sh
make dev-db     # PostgreSQL 16 in Docker on 127.0.0.1:55432
make build      # web app + server binary in bin/finalechat
make run        # http://127.0.0.1:8787 (make web-dev for hot reload)
make check      # gofmt, go vet, the Go suite, web typecheck, CLI checks
```

`README.md` describes the layout and `CLAUDE.md` the conventions the code
follows: SQL only in `internal/store`, schema changes as new numbered
migrations, agent-facing documents kept in sync with the handlers, and a CLI
that stays one dependency-free Python file.

## Pull requests

- Keep each PR to one change. Include tests for behaviour that can be tested
  against the database or the HTTP surface; the suite in `internal/api` runs
  real SQL, so that is where most tests belong.
- Run `make check` before pushing. CI runs the same checks on every pull
  request.
- Update `AGENTS.md`, `docs/API.md` and `docs/openapi.json` when a route or
  field changes; agents read those documents directly.
- Write commit messages that say what changed and why in the first line.

By contributing you agree that your contribution is licensed under the MIT
License in `LICENSE`.
