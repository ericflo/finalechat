# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

# ---- Web app ---------------------------------------------------------------
FROM node:22-bookworm-slim@sha256:83f487e0a63425e5b4d146fb5e5be574bcbe1b7b843d3ebafdd95eaf7767a7e5 AS web
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
FROM golang:1.26-alpine@sha256:ce864e7223ac17b1775e6fd0b4c0db580c2eb50e7953a427916379e4b92a1628 AS toolchain
FROM postgres:16-alpine@sha256:cf78e76683b9ca8c5733cbbdce6c9262b45b6767934dd0a95e671f9a0fc20685 AS test
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
FROM golang:1.26-bookworm@sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81 AS build
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
