# Bragibooks

[![CI](https://img.shields.io/github/actions/workflow/status/djdembeck/bragibooks/ci.yml?branch=develop&label=CI)](https://github.com/djdembeck/bragibooks/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/djdembeck/bragibooks.svg)](LICENSE)
[![Docker pulls](https://img.shields.io/docker/pulls/djdembeck/bragibooks.svg)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Docker image version](https://img.shields.io/docker/v/djdembeck/bragibooks.svg)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Contributors](https://img.shields.io/github/contributors/djdembeck/bragibooks.svg)](https://github.com/djdembeck/bragibooks/graphs/contributors)

> The only known web GUI for `m4b-merge`: batch audiobook processing with AudiobookDB metadata, running on your server.

## Long Description

Bragibooks is a self-hosted web application for cleaning up and organizing audiobook files. It merges split `mp3`, `m4a`, and `m4b` chapters, converts formats such as `mp3` to `m4b`, cleans existing metadata, and writes correctly tagged output through [`m4b-merge`](https://github.com/djdembeck/m4b-merge). Match books against the community-maintained [AudiobookDB](https://audiobookdb.org/) database for title, author, narrator, and release information.

It is an operations board for a personal audiobook pipeline, not a cloud library, SaaS dashboard, or media player. Run it locally or on a home server, select a batch, match it, queue it, and watch the work finish. Bragibooks is single-user, local-first, and has no cloud account or telemetry requirement.

## Table of Contents

- [Security](#security)
- [Background](#background)
- [Install/Running](#installrunning)
  - [Docker](#docker)
  - [Existing binary](#existing-binary)
- [Usage](#usage)
- [Configuration](#configuration)
- [API](#api)
- [Contributing](#contributing)
- [Building](#building)
  - [Make](#make)
  - [Explicit build steps](#explicit-build-steps)
  - [Build a Docker image](#build-a-docker-image)
  - [Development](#development)
- [Maintainers](#maintainers)
- [License](#license)

## Security

Bragibooks is designed for one operator on a private network. The application does not provide authentication, so do not expose it directly to the public internet; put it behind a trusted network boundary and, when needed, an authenticated reverse proxy.

## Background

**Bragi - god of poetry in [Norse mythology](https://en.wikipedia.org/wiki/Bragi).** The name fits an application concerned with spoken books and music.

The interface follows a **Signal Interlocking Panel**: a calm, dense work surface modeled after a railway control board. Its four stations are **Intake**, **Match**, **Queue**, and **Finish**. The person who runs the server, owns the books, and needs to see state should be able to tell what is happening without decoration or guesswork.

## Install/Running

Docker is the fastest way to run Bragibooks. The image includes the Go server, embedded SvelteKit frontend, `ffmpeg`, and `m4b-merge`; no host toolchain is required.

### Docker

Create or choose host directories for the database/configuration, input books, output books, and completed source files. Then start the pre-built image:

```sh
docker run --rm -d --name bragibooks \
  --publish 8888:8080 \
  --volume "$PWD/config:/app/config" \
  --volume /path/to/input:/input \
  --volume /path/to/output:/output \
  --volume /path/to/completed:/input/done \
  --env SERVER_HOST=0.0.0.0 \
  --env SERVER_PORT=8080 \
  --env DIRECTORIES_INPUT_DIR=/input \
  --env DIRECTORIES_OUTPUT_DIR=/output \
  --env DIRECTORIES_COMPLETED_DIR=/input/done \
  --env PROCESSING_REGION=us \
  ghcr.io/djdembeck/bragibooks:main
```

The container listens on port `8080`; the command above publishes it as `http://localhost:8888`. The mounted `/app/config` directory stores `config/config.yaml` and the SQLite database. `API_KEY_API_KEY` is optional, but setting it enables AudiobookDB requests that require an API key.

A Docker Compose deployment can use the same image and mounts. The repository's [production compose file](docker/docker-compose.yml) is the reference for the container port and persistent config mount; replace its local image name with `ghcr.io/djdembeck/bragibooks:main` when running the pre-built image.

### Existing binary

If you already have a `bragibooks` binary, put `ffmpeg` and [`m4b-merge`](https://github.com/djdembeck/m4b-merge) on `PATH`, provide the input/output directories, and run it from the directory containing `config/config.yaml`:

```sh
DIRECTORIES_INPUT_DIR=/path/to/input \
DIRECTORIES_OUTPUT_DIR=/path/to/output \
DIRECTORIES_COMPLETED_DIR=/path/to/completed \
./bragibooks
```

The binary serves the web UI on `SERVER_HOST:SERVER_PORT` (port `8080` by default). See [Building](#building) for the source-build commands.

## Usage

The normal work is deliberately linear: **select, match, queue, finish**.

1. Start the container with the Docker command above. A compact version with explicit environment configuration is:

   ```sh
   docker run --rm -d --name bragibooks \
     -p 8888:8080 \
     -v "$PWD/config:/app/config" \
     -v /path/to/input:/input \
     -v /path/to/output:/output \
     -v /path/to/completed:/input/done \
     -e SERVER_PORT=8080 \
     -e DIRECTORIES_INPUT_DIR=/input \
     -e DIRECTORIES_OUTPUT_DIR=/output \
     -e DIRECTORIES_COMPLETED_DIR=/input/done \
     -e PROCESSING_NUM_CPUS=1 \
     -e PROCESSING_REGION=us \
     -e API_KEY_API_KEY="${AUDIOBOOKDB_API_KEY:-}" \
     ghcr.io/djdembeck/bragibooks:main
   ```

2. Open [http://localhost:8888](http://localhost:8888) in a browser. You can check that the server is up first:

   ```sh
   curl http://localhost:8888/api/health
   ```

3. **Intake:** browse the mounted input directory and select one or more audiobook folders or files.
4. **Match:** let Bragibooks search AudiobookDB, then confirm or correct the book, release, author, and narrator metadata. Custom search is available when the first match is wrong.
5. **Queue:** submit the matched batch. Bragibooks invokes `m4b-merge` to merge, convert, clean, tag, and repackage the source files.
6. **Finish:** follow live processing output in the browser, then review completed books and any errors. Jobs can take from seconds to hours depending on the number and type of files.

Set `PROCESSING_REGION` to the Audible region you use for metadata lookup, such as `us`, `uk`, `de`, or `fr`. Bragibooks also accepts the legacy `REGION` environment variable as an alias.

## Configuration

Bragibooks reads `config/config.yaml` and falls back to environment variables. When the same setting is present in both, the YAML value takes precedence. Settings can also be changed at runtime through `PUT /api/settings`; runtime changes are persisted by the application.

The key environment variables are:

| Variable | Purpose | Default |
| --- | --- | --- |
| `SERVER_HOST` | Address for the HTTP server | `0.0.0.0` |
| `SERVER_PORT` | HTTP port | `8080` |
| `DATABASE_PATH` | SQLite database path | `config/bragibooks.db` |
| `M4B_MERGE_BINARY` | `m4b-merge` executable name or path | `m4b-merge` |
| `API_KEY_API_KEY` | Optional AudiobookDB API key | empty |
| `API_KEY_BASE_URL` | AudiobookDB API base URL | `https://audiobookdb.org/api` |
| `DIRECTORIES_INPUT_DIR` | Source audiobook directory | `/input` |
| `DIRECTORIES_OUTPUT_DIR` | Destination directory for processed books | `/output` |
| `DIRECTORIES_COMPLETED_DIR` | Directory for completed source files | `/input/done` |
| `PROCESSING_NUM_CPUS` | Number of processing workers | `1` |
| `PROCESSING_PATH_FORMAT` | Output path format | `{author}/{title}` |
| `PROCESSING_REGION` | Audible metadata region (`us`, `uk`, `de`, `fr`, and others) | `us` |
| `PROCESSING_LOG_LEVEL` | Processing log level | `info` |

For a YAML-based setup, create `config/config.yaml` in the mounted config directory:

```yaml
server:
  host: 0.0.0.0
  port: 8080

directories:
  input_dir: /input
  output_dir: /output
  completed_dir: /input/done

processing:
  num_cpus: 1
  path_format: "{author}/{title}"
  region: us
```

<details>
<summary>All configuration keys and precedence details</summary>

Environment variables use the uppercase, prefixed form of the YAML path: `server.port` becomes `SERVER_PORT`, `directories.input_dir` becomes `DIRECTORIES_INPUT_DIR`, and so on. The remaining supported keys are `database.path`, `m4b_merge.binary`, `api_key.api_key`, `api_key.base_url`, `processing.log_level`, and `processing.path_format`.

The configuration file is normally `config/config.yaml`; the loader also searches the working directory, `/app/data`, and `/config`. A value explicitly written in YAML overrides its environment-variable counterpart. If no YAML file exists, environment variables and defaults are used.

</details>

## API

The web frontend uses the HTTP API below. Responses are JSON unless noted otherwise; job streams use server-sent events (SSE).

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/api/health` | Health and version check |
| `GET` | `/api/directories?path=/input` | List a directory |
| `GET` | `/api/directories/tree?path=/input` | Read a directory tree |
| `GET` | `/api/directories/stream?path=/input` | Stream directory entries as NDJSON |
| `GET` | `/api/books` | List books; supports status, page, and limit filters |
| `POST` | `/api/books` | Add source book entries |
| `GET`, `PUT`, `DELETE` | `/api/books/{id}` | Read, update, or remove a book |
| `GET` | `/api/search?query=...&types=books&skip=0&take=20` | Search AudiobookDB |
| `GET` | `/api/search/books/{id}` | Fetch an AudiobookDB book |
| `GET` | `/api/search/releases/{id}` | Fetch an AudiobookDB release |
| `POST` | `/api/process` | Queue processing for matched books |
| `GET` | `/api/jobs` | List processing jobs |
| `GET` | `/api/jobs/{id}` | Read job status |
| `GET` | `/api/jobs/{id}/stream` | Stream live job output over SSE |
| `GET`, `PUT` | `/api/settings` | Read or update runtime settings |
| `POST` | `/api/migrate` | Migrate a legacy database |
| `POST` | `/api/migrate/people` | Recover people from a legacy database |

For example, monitor a queued job without opening the web UI:

```sh
curl -N http://localhost:8888/api/jobs/<job-id>/stream
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow, bug reports, enhancement proposals, and style guidance.

Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). Feature work normally targets `develop`; releases are made from `main`. The CI pipeline checks formatting, vetting, module tidiness, frontend type checks, builds, and tests.

## Building

Building from source requires Go 1.25, [Bun](https://bun.sh/), and the runtime dependencies `ffmpeg` and [`m4b-merge`](https://github.com/djdembeck/m4b-merge). The production build embeds the SvelteKit frontend in the Go binary.

### Make

The repository's standard build is:

```sh
make build
./bragibooks
```

`make build` installs the locked frontend dependencies, builds `web/`, copies the result into `webfs/build/`, and compiles the server with the `webui` build tag.

### Explicit build steps

The equivalent commands are:

```sh
cd web
bun install --frozen-lockfile
bun run build
cd ..
rm -rf webfs/build
cp -r web/build webfs/build
go build -tags webui -ldflags="-s -w" -o bragibooks ./cmd/bragibooks
```

### Build a Docker image

The root [Dockerfile](Dockerfile) builds the SvelteKit frontend, embeds it in a statically compiled Go binary, and creates a Debian runtime image with `ffmpeg` and `m4b-merge`:

```sh
docker build -t bragibooks:local .
docker run --rm -p 8888:8080 \
  -v "$PWD/config:/app/config" \
  -v /path/to/input:/input \
  -v /path/to/output:/output \
  bragibooks:local
```

### Development

For backend hot reload with `air`, run:

```sh
make watch
```

For the complete two-service development environment, use the development compose file. It runs the Go backend on port `8080` and the Vite frontend on port `5175`:

```sh
docker compose -f docker/docker-compose.dev.yml up --build
```

Open [http://localhost:5175](http://localhost:5175) while the development stack is running. The Vite server proxies `/api` requests to the Go backend.

## Maintainers

- [@djdembeck](https://github.com/djdembeck) — idea and initial work

Contributors are recorded using the [all-contributors](https://allcontributors.org/) specification:

- [Koby Huckabee](https://koby.huckabee.dev) (`AceTugboat`) — code, ideas/planning/feedback, and documentation
- [Andreas](https://pilabor.com) (`sandreas`) — tools

Release notes are maintained in [CHANGELOG.md](CHANGELOG.md).

## License

[GPL-3.0-only](LICENSE) — GNU General Public License v3.
