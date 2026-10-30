# Stage 0: Build SvelteKit frontend
FROM oven/bun:1 AS frontend
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Stage 1: Build Go backend with embedded frontend
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/build webfs/build/
ARG VERSION=dev
RUN VERSION="${VERSION#v}" && CGO_ENABLED=0 go build -tags webui -ldflags="-s -w -X github.com/bragibooks/bragibooks/internal/version.Version=${VERSION}" -o bragibooks ./cmd/bragibooks


# Stage 2: Runtime
FROM debian:trixie-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ffmpeg ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /config /input /output
WORKDIR /app
COPY --from=builder /build/bragibooks /usr/local/bin/
COPY --from=ghcr.io/djdembeck/m4b-merge:main-amd64@sha256:f36864ceba5867bd10f36ad8574ac9f4e2cf45d97a94c6c7c13b63015f80cfff /usr/local/bin/m4b-merge /usr/local/bin/
COPY docker/entrypoint.sh /usr/local/bin/bragibooks-entrypoint
RUN chmod 0755 /usr/local/bin/bragibooks-entrypoint
# The image runs as root by default (matching the legacy image); the
# entrypoint chowns the runtime directories and drops to UID/GID (legacy
# variable names, defaults 99/100; PUID/PGID accepted as aliases) so
# host-mounted volumes keep working on upgrade. The built-in default
# database path is relative to WORKDIR and would fatal at startup, so
# pin it to the /config volume. Compose files and operator env vars
# keep overriding these.
ENV DATABASE_PATH=/config/bragibooks.db
EXPOSE 8080
VOLUME ["/config", "/input", "/output"]
ARG VERSION=dev
ARG GIT_COMMIT=unknown
LABEL org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}"
ENTRYPOINT ["/usr/local/bin/bragibooks-entrypoint"]