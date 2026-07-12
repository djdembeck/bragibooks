# Stage 0: Build m4b-merge from sibling Rust project (--build-context m4b=../m4b-merge)
FROM rust:1.96-slim AS m4b-merge-builder
WORKDIR /app
COPY --from=m4b . ./
RUN cargo build --release --locked

# Stage 1: Build SvelteKit frontend
FROM oven/bun:1 AS frontend
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Stage 2: Build Go backend with embedded frontend
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/build webfs/build/
RUN CGO_ENABLED=0 go build -tags webui -ldflags="-s -w" -o bragibooks ./cmd/bragibooks


# Stage 3: Runtime
FROM alpine:latest
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY --from=builder /build/bragibooks /usr/local/bin/
COPY --from=m4b-merge-builder /app/target/release/m4b-merge /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bragibooks"]