# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

# ---- Web app ---------------------------------------------------------------
FROM node:22.23.5-bookworm-slim@sha256:be979e5523c76fa4d5ca4ebd3099078f220aa47ec39c48ad5b5df9314debd6eb AS web
RUN apt-get update && apt-get upgrade -y && rm -rf /var/lib/apt/lists/*
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,id=finalechat-npm,target=/root/.npm,sharing=locked \
    npm ci --no-audit --no-fund
COPY web/ ./
ARG VERSION=dev
ENV FINALECHAT_VERSION=$VERSION
# Type checks for the app and the service worker gate the bundle.
RUN npm run build

# ---- Integration suite -----------------------------------------------------
# The Go suite exercises real SQL, LISTEN/NOTIFY fan-out and long-polls, so it
# runs against the same PostgreSQL major as production. The Go toolchain is
# copied from the official image into the postgres image; both are Alpine.
FROM golang:1.26.11-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS toolchain
FROM postgres:16.15-alpine@sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab AS test
COPY --from=toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH GOPATH=/go GOFLAGS=-mod=readonly CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . ./
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=finalechat-go-build,target=/root/.cache/go-build,sharing=locked \
    set -eu \
    && mkdir -p /tmp/pg && chown postgres:postgres /tmp/pg \
    && gosu postgres initdb -D /tmp/pg --auth=trust >/dev/null \
    # Unix socket only: concurrent builds on one runner can share a network
    # namespace, and a fixed TCP port then fails to bind. The socket lives in
    # this build's own /tmp. Print the server log if it still will not start.
    && { gosu postgres pg_ctl -D /tmp/pg -o "-k /tmp -c listen_addresses=''" -l /tmp/pg.log -w start >/dev/null || { cat /tmp/pg.log; exit 1; }; } \
    && gosu postgres createdb -h /tmp finalechat_test \
    && test -z "$(gofmt -l cmd internal assets.go)" \
    && go vet ./... \
    && FINALECHAT_TEST_DATABASE_URL='postgres://postgres@/finalechat_test?host=/tmp&sslmode=disable' go test -count=1 ./... \
    && gosu postgres pg_ctl -D /tmp/pg -w stop >/dev/null \
    && date -u +%Y-%m-%dT%H:%M:%SZ >/tmp/tests-passed

# ---- Server ----------------------------------------------------------------
FROM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . ./
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
# The binary is only built once the suite has passed in the same graph, so a
# published image can never skip its tests.
COPY --from=test /tmp/tests-passed /tmp/tests-passed
ARG VERSION=dev
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=finalechat-go-build-bookworm,target=/root/.cache/go-build,sharing=locked \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/finalechat ./cmd/finalechat

# ---- Runtime ---------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/finalechat /finalechat
ENV FINALECHAT_ADDR=:8080 TZ=UTC
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/finalechat"]
CMD ["serve"]
