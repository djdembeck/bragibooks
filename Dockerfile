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
RUN CGO_ENABLED=0 go build -tags webui -ldflags="-s -w" -o bragibooks ./cmd/bragibooks


# Stage 2: Runtime
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /build/bragibooks /usr/local/bin/
COPY --from=ghcr.io/djdembeck/m4b-merge:main-amd64 /usr/local/bin/m4b-merge /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bragibooks"]