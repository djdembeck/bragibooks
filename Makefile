.PHONY: watch build run test

watch:
	air

build:
	cd web && bun install --frozen-lockfile && bun run build
	rm -rf webfs/build && cp -r web/build webfs/build
	go build -tags webui -ldflags="-s -w" -o bragibooks ./cmd/bragibooks

run:
	./bragibooks

test:
	go test ./internal/...
	cd web && bun run test