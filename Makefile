# Finalechat developer loop. `make dev-db` once, then `make run` and `make web-dev`.

GO ?= go
NPM ?= npm
BIN := bin/finalechat
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DEV_DB_URL ?= postgres://finalechat:finalechat@127.0.0.1:55432/finalechat?sslmode=disable

.PHONY: all build cli web web-dev run test test-go test-web test-browser test-native test-cli fmt vet check dev-db dev-db-stop icons clean

all: build

## Build the web app into internal/webassets/dist, then the server binary.
build: cli web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/finalechat

cli:
	python3 scripts/build-cli-integrations.py

web: web/node_modules
	cd web && FINALECHAT_VERSION=$(VERSION) $(NPM) run build

web/node_modules: web/package.json web/package-lock.json
	cd web && $(NPM) ci --no-audit --no-fund
	@touch web/node_modules

## Vite dev server with API proxying to a locally running server on :8787.
web-dev: web/node_modules
	cd web && $(NPM) run dev

## Run the server against the local development database.
run:
	DATABASE_URL="$(DEV_DB_URL)" FINALECHAT_ADDR=127.0.0.1:8787 FINALECHAT_BASE_URL=http://127.0.0.1:8787 \
	FINALECHAT_SECURE_COOKIES=false FINALECHAT_LOG_JSON=false $(GO) run ./cmd/finalechat

## Start a throwaway PostgreSQL in Docker for local development and tests.
dev-db:
	docker run -d --name finalechat-pg -e POSTGRES_USER=finalechat -e POSTGRES_PASSWORD=finalechat -e POSTGRES_DB=finalechat \
		-p 127.0.0.1:55432:5432 postgres:16-alpine
	@until docker exec finalechat-pg pg_isready -U finalechat >/dev/null 2>&1; do sleep 1; done
	docker exec finalechat-pg createdb -U finalechat finalechat_test || true

dev-db-stop:
	docker rm -f finalechat-pg

fmt:
	gofmt -w cmd internal assets.go

vet:
	$(GO) vet ./...

## Full test suite. Integration tests need FINALECHAT_TEST_DATABASE_URL.
test: test-go test-web test-cli

test-go:
	FINALECHAT_TEST_DATABASE_URL="$${FINALECHAT_TEST_DATABASE_URL:-postgres://finalechat:finalechat@127.0.0.1:55432/finalechat_test?sslmode=disable}" \
		$(GO) test -count=1 ./...

test-web: web/node_modules
	cd web && $(NPM) run typecheck

## Isolated Chromium fixture checks; install Playwright separately (see docs/integrations.md).
test-browser: web/node_modules
	node scripts/test-artifact-browser.mjs

## Optional installed clients, isolated homes, local fake model and chat APIs.
test-native:
	python3 scripts/build-cli-integrations.py --check
	python3 -I scripts/test-native-claude.py
	python3 -I scripts/test-native-codex.py
	FINALECHAT_TEST_NATIVE_CODEX=1 python3 -m unittest discover -s cli -p test_integrations.py

test-cli:
	python3 scripts/build-cli-integrations.py --check
	python3 -m py_compile cli/finalechat
	python3 cli/finalechat --help >/dev/null
	python3 cli/finalechat selftest
	python3 -m unittest discover -s cli -p 'test_*.py'
	sh -n cli/install.sh
	python3 -c "import json; json.load(open('docs/openapi.json'))"

## Everything CI checks, locally.
check: fmt vet test

icons:
	cd web && python3 scripts/gen-icons.py

clean:
	rm -rf bin internal/webassets/dist/* web/dist
	touch internal/webassets/dist/.gitkeep
