# Stage 1: Build SvelteKit frontend
FROM oven/bun:1 AS frontend
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Stage 2: Build m4b-merge Rust binary from develop branch
FROM rust:1.85-slim AS m4bmerge
WORKDIR /m4b-merge
COPY m4b-merge/Cargo.toml m4b-merge/Cargo.lock ./
RUN cargo build --release 2>/dev/null || true  # download deps
COPY m4b-merge/src ./src
RUN cargo build --release

# Stage 3: Build Go binary
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git ffmpeg
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/build web/build/
COPY --from=m4bmerge /m4b-merge/target/release/m4b-merge /usr/local/bin/m4b-merge
RUN CGO_ENABLED=0 go build -tags webui -ldflags="-s -w" -o bragibooks ./cmd/bragibooks

# Stage 4: Runtime
FROM alpine:latest
RUN apk add --no-cache ffmpeg
COPY --from=builder /build/bragibooks /usr/local/bin/
COPY --from=m4bmerge /m4b-merge/target/release/m4b-merge /usr/local/bin/m4b-merge
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bragibooks"]