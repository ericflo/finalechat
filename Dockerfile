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

# ---- Server ----------------------------------------------------------------
FROM golang:1.26-bookworm@sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . ./
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
ARG VERSION=dev
# vet and the database-free unit tests run in the same graph as the compile so
# a publish can never skip them. The full suite (against PostgreSQL) runs in the
# CI test step before this image is built.
RUN --mount=type=cache,id=finalechat-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=finalechat-go-build,target=/root/.cache/go-build,sharing=locked \
    go vet ./... \
    && go test ./... \
    && go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/finalechat ./cmd/finalechat

# ---- Runtime ---------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/finalechat /finalechat
ENV FINALECHAT_ADDR=:8080 TZ=UTC
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/finalechat"]
CMD ["serve"]
