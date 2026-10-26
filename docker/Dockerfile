# Stage 0: Build SvelteKit frontend
FROM oven/bun:1 AS frontend
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Stage 1: Build Go backend with embedded frontend
FROM golang:1.25-alpine AS builder
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
    useradd -u 1000 app && \
    mkdir -p /config /input /output && \
    chown -R app:app /config /input /output
WORKDIR /app
COPY --from=builder /build/bragibooks /usr/local/bin/
COPY --from=ghcr.io/djdembeck/m4b-merge:main-amd64 /usr/local/bin/m4b-merge /usr/local/bin/
COPY docker/entrypoint.sh /usr/local/bin/bragibooks-entrypoint
RUN chmod 0755 /usr/local/bin/bragibooks-entrypoint
ENV PUID=1000 PGID=1000
# The image runs as `app` (uid 1000); the built-in default database path
# is relative to WORKDIR (/app, not writable) and would fatal at startup.
# Pin the runtime default to the app-owned /config volume. Compose files
# and operator env vars keep overriding this.
ENV DATABASE_PATH=/config/bragibooks.db
EXPOSE 8080
VOLUME ["/config", "/input", "/output"]
USER app
ENTRYPOINT ["/usr/local/bin/bragibooks-entrypoint"]