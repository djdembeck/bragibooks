# Migrating from the Python app to the current Bragi Books Go app

The current Bragi Books Go app can take over a Python app's library. On an eligible first start it finds the legacy Django SQLite database, opens it **read-only**, and copies all books, people, and settings into the new database. The legacy file is not modified or renamed, and stays in place.

This guide covers the practical Docker upgrade, what does and does not get copied, how to verify the result, and how to roll back.

## Before you start

1. **Stop the Python app** before starting the Go app. Never run both apps at the same time, and do not run the legacy app while verifying the migration: the migration records a checksum of the legacy database, and a legacy DB that changes after the migration is treated as modified on later boots.
2. **Back up the legacy `/config` volume yourself** — the app does not create an automatic backup at startup. Copy the whole directory, including `db.sqlite3` and any SQLite sidecars (`db.sqlite3-wal`, `db.sqlite3-shm`) if present. The Python app does not enable WAL journaling by default, so those sidecars are usually absent. The Go database uses WAL mode, so its sidecars (`bragibooks.db-wal`, `bragibooks.db-shm`) do appear once it runs — keep them in backups of the new volume too.
3. **Back up your audio directories** (input, output, completed).
4. **Keep the host mounts stable.** Mount the *same* host directories into `/config`, `/input`, and `/output` (and completed source files into `/input/done`) in the new container as the old one used. The legacy input/output/completed directories and CPU count from your Python settings are copied into the new settings, so the path mappings the app sees inside the container stay correct.

## Which image to run

| Image | Build | Use |
| --- | --- | --- |
| `ghcr.io/djdembeck/bragibooks:develop` / `djdembeck/bragibooks:develop` | Current Go app | Rolling development channel. Use a versioned release tag to pin a release. |
| `ghcr.io/djdembeck/bragibooks:python-final` / `djdembeck/bragibooks:python-final` | Last Python source (commit `69aabfea`) | Archive/rollback image, pinned at index digest `sha256:e583597e3823a091a11a78e277805d453d791b65ad8da827ff2355bd8cb2f924` (linux/amd64) on both registries. |

## Upgrade steps

1. Stop and remove the Python container.
2. Start the `:develop` image against the **same host `/config` directory** that holds `db.sqlite3`:

   ```sh
   docker run --rm -d --name bragibooks \
     --publish 8888:8080 \
     --volume "$PWD/config:/config" \
     --volume /path/to/input:/input \
     --volume /path/to/output:/output \
     --volume /path/to/completed:/input/done \
     --env SERVER_PORT=8080 \
     --env DIRECTORIES_INPUT_DIR=/input \
     --env DIRECTORIES_OUTPUT_DIR=/output \
     --env DIRECTORIES_COMPLETED_DIR=/input/done \
     ghcr.io/djdembeck/bragibooks:develop
   ```

   The Python image exposed container port `8000`; the Go image listens on `8080` (`SERVER_PORT`). Pick whatever host port you like — `8888` is the example used in the [README](../README.md#docker).

3. Read the start log. An eligible migration logs, in order:

   ```text
   Legacy Django DB found at /config/db.sqlite3, starting auto-migration...
   Auto-migration complete: N books, M people migrated
   ```

   The startup logs also print the new DB path and its size/mtime alongside the legacy DB's, so you can confirm the new database file is on the persistent volume.

The migration runs automatically on first eligible start — you do not need to call anything.

## How the app finds the legacy database

The legacy DB is searched in this order; the first existing path wins (existence only — it must then be a valid legacy Bragi Books database):

1. `LEGACY_DB_PATH` (environment variable) — explicit override, e.g. if your legacy `db.sqlite3` is not where the candidates look.
2. `db.sqlite3` next to the discovered `config.yaml` file, if one exists.
3. `db.sqlite3` next to the new database file (i.e. next to `DATABASE_PATH`).
4. `config/db.sqlite3` (relative to the working directory).
5. `/config/db.sqlite3` — the standard Docker layout.

If no candidate exists, migration is quietly skipped and the app starts empty. Only migration logging happens when a candidate is found.

`LEGACY_DB_PATH` is the container's view of the file: for a file on the mounted config volume it is `/config/db.sqlite3`.

## What is copied, and what is not

Everything is copied inside a single transaction in the new database — it is all-or-nothing. The legacy database is opened read-only and never modified, renamed, or deleted.

| Legacy | New |
| --- | --- |
| Every `importer_book` row | One `books` row: title, ASIN, description (short + long, combined), release date, series, publisher, language, runtime, format, source/destination paths, status, cover image, timestamps, converted flag |
| `importer_status` names | Status mapping (below) |
| Authors and narrators (with their book links) | `people` rows, matched to the new book by ASIN first, then title |
| `importer_setting`: input dir, output dir, completed dir, `num_cpus`, `output_scheme` | New settings row. Any AudiobookDB base URL or `m4b-merge` binary already present in the new app's config is preserved on top of the legacy values; the region is set to `us` |

Status mapping:

| Legacy status | New status |
| --- | --- |
| `Done` | `done` |
| `Processing` | `pending` |
| `Error` | `error` |
| anything else / missing | `pending` |

`Processing` becomes `pending` on purpose: the new app tracks active work as job rows, and a migrated book has no job — `pending` is the only status that works without one.

Not copied:

- **Processing job history.** The new `processing_jobs` table starts empty; only the books and their statuses carry over.
- **Django internals.** Admin sessions, Django users, and the `secret_key.txt` in `/config` are not used by the Go app (they can stay in the volume, go unused).
- **Audio files.** Nothing moves. Source files, processed output, and completed files stay exactly where the old app left them; what changes is only where the app keeps its data (from `db.sqlite3` to `bragibooks.db` in the same volume).

## Configuration changes

The Python app's environment variables no longer apply. Mapping of what changed:

| Python env var | Go equivalent |
| --- | --- |
| `ALLOWED_HOSTS`, `DEBUG`, `CSRF_TRUSTED_ORIGINS` | none — replaced by the Go app's admin auth (see [Security](../README.md#security) in the README) |
| `LOG_LEVEL` | `PROCESSING_LOG_LEVEL` |
| `REGION` | `PROCESSING_REGION` (`REGION` is still accepted as an alias) |
| *(none — port 8000)* | `SERVER_PORT` (default `8080`) and `SERVER_HOST` |
| *(none — the Python app had no AudiobookDB credential setting)* | `API_KEY_API_KEY` (optional) and `API_KEY_BASE_URL` (default `https://audiobookdb.org/api`) |
| *(directories came from the DB setting row)* | `DATABASE_PATH`, `M4B_MERGE_BINARY`, `DIRECTORIES_*`, `PROCESSING_NUM_CPUS`, `PROCESSING_PATH_FORMAT`, plus `LEGACY_DB_PATH` for migration only. Remote API access is no longer an env var: it is the Settings → External API key (optional `SERVER_API_ENDPOINT_ENABLED` pins the on/off flag at deployment level) |

Go settings precedence is **environment variable > YAML file > default**, with the database settings row (where the migration writes the legacy settings) loaded below YAML and env.

One binding detail to be aware of: `SERVER_HOST` defaults to *empty*, and an empty host makes the Go server listen on **all interfaces** (`:8080`), not loopback. In Docker the published port is what controls exposure — use `--publish 127.0.0.1:8888:8080` to keep it reachable from the host only. If you run the binary directly on a host and want loopback-only, set `SERVER_HOST=127.0.0.1` explicitly.

## Verifying the migration

1. `curl http://localhost:8888/api/health` — confirms the server is up.
2. Check the start log for `Auto-migration complete: N books, M people migrated` and for the `Database:` line showing an absolute path under your mounted volume (not a container-layer path).
3. Compare the book count in the app, or:

   ```sh
   curl -s http://localhost:8888/api/books | head
   ```

   The response has a `"total"` field; it should match the legacy book count. With the External API off (the default), requests are accepted only from loopback/private-LAN sources — a call issued from the Docker host passes that test, since the container's bridge network is private-LAN. From elsewhere, first enable the External API in Settings and use the printed `bk_…` key as `Authorization: Bearer <key>`.
4. Spot-check the Books screen: titles, processed output paths, and cover images should match what the Python app showed.
5. Confirm the new files exist on the host in the config volume: `bragibooks.db` (plus its `-wal`/`-shm` sidecars) and `db.sqlite3.bragi-migrated` (the migration receipt — see below).

## Restart persistence and the migration receipt

After a successful migration the app writes a receipt file next to the legacy database: `db.sqlite3.bragi-migrated` — JSON with the legacy file's SHA-256 and size, the migrated counts, the timestamp, and the path the new database was written to. Its job is to protect against a **non-persistent new database path**: if you later boot with an empty new DB (e.g. the `/config` volume was not mounted, or `DATABASE_PATH` points somewhere ephemeral) that already has a matching receipt, the app refuses to migrate again — re-migrating into a fresh empty DB would look like "new", silent data loss.

On such a boot you see a `WARNING` line instead, naming the recorded target database and telling you to restore it, point `database.path` at it, and delete the receipt. The normal safe sequence it implies:

- Restore (or copy) the migrated `bragibooks.db` from your backup into the mount, set `DATABASE_PATH` back to that persistent path, delete `db.sqlite3.bragi-migrated`, and restart.
- If you have genuinely deleted the migrated database and want a fresh migration from the intact legacy file, delete `db.sqlite3.bragi-migrated` and restart. The receipt is the only thing that stands in the way, and the legacy file itself was never touched by the migration.

The other normal boots are no-ops: once the new database has any books, migration is skipped silently (regardless of receipt), and a receipt whose recorded hash no longer matches the legacy file causes a logged note and proceeds, since the legacy DB was changed after migrating.

## Manual migration endpoint

If the automatic migration did not find your legacy database (set `LEGACY_DB_PATH` and restart, or use the endpoint directly):

```sh
curl -s -X POST http://localhost:8888/api/migrate \
  -H "Content-Type: application/json" \
  -d '{"path": "/config/db.sqlite3"}'
```

- `path` is required and is the container path to the legacy database (`~` is expanded).
- Success returns `{"migrated":{"books":N,"people":M},"backup":"/config/db.sqlite3.bak"}`.
- `409` if the new database already contains books — the guard against duplicating a completed migration.
- The `backup` field is a best-effort copy of the legacy file to `<path>.bak` made before the migration; it is not guaranteed (a failed copy is a warning, not an error) and it is not a rollback. Your own pre-upgrade backup is the safety net.

There is also `POST /api/migrate/people` with the same body, which migrates only authors/narrators into a database that already has the books — the recovery path when books migrated but people did not. It returns `{"migrated":{"authors":N,"narrators":M}}`.

## Troubleshooting

| Log line / symptom | Cause and fix |
| --- | --- |
| No `Legacy Django DB found…` line at all | No candidate path existed. Put the legacy DB at `/config/db.sqlite3` (standard layout) or set `LEGACY_DB_PATH`. |
| `Warning: auto-migration failed: … not a valid legacy Bragi Books database` | The file found is not a Django Bragi Books database (no `importer_book`/`importer_author`/`importer_narrator` tables). Point `LEGACY_DB_PATH` at the real one. Startup continues with an empty library; fix the path and use `POST /api/migrate` or restart. |
| `Warning: auto-migration failed: … (you can manually migrate via POST /api/migrate)` | Migration read an unsupported legacy schema (older versions are supported for missing `status_id`/`cover_image_link` columns; anything further off errors). Use the manual endpoint to see the same error, then inspect the legacy DB. |
| `WARNING: legacy DB … already auto-migrated … but that database is still present … but current database.path does not point there` | Your new DB is on a persistent volume but `DATABASE_PATH` now points elsewhere. Set `database.path` back to the recorded path (or copy that DB file), delete `db.sqlite3.bragi-migrated`, restart. |
| `WARNING: legacy DB … but … is empty — the merged data is gone` | The migrated database was lost because `database.path` was not persistent. Restore `bragibooks.db` from backup into the mount, fix the setting, delete the receipt, restart. |
| Migration never appears in the log and the library is empty | The guard only refuses when it can confirm books already exist in the new DB, so with an empty new DB and a found legacy DB it always attempts. With zero books and no candidate path the app starts empty by design (see "No `Legacy Django DB found…` line"). If startup died before the migration ran — an earlier database-open or schema-migration failure — fix that first; the `m4b-merge` version check occurs after the migration and cannot block it. |
| API calls return `401`/`403` in this flow | The call comes from outside loopback/private-LAN with the External API off (`403`), on with no key (`401`), or with a key that does not match the stored one (`403`). From the Docker host the call passes the LAN rule; from elsewhere, enable the External API in Settings and present the printed `bk_…` key. See the [Security](../README.md#security) section. |

## Rolling back to the Python app

The rollback is simple because the legacy database was never modified:

1. Stop and remove the Go container.
2. If you will be running the Python app again, remove (or keep aside) `bragibooks.db`, `bragibooks.db-wal`, `bragibooks.db-shm`, and `db.sqlite3.bragi-migrated` from the config volume so the legacy file is exactly as before — or restore the config volume from the backup you made before the upgrade.
3. Start the archived image on the old port layout (container port `8000`): `ghcr.io/djdembeck/bragibooks:python-final` (or the Docker Hub equivalent; both are pinned at the digest stated above). The legacy app opens `/config/db.sqlite3` exactly as it always did.

Caveat: **audio work the Go app already performed is not undone.** Processed output and completed files produced since the upgrade remain on disk, and the legacy app will simply not know about them (its book list and statuses come from `db.sqlite3`).

After a rollback, re-migrating up works as before: start the Go image, and if the new database is empty and the legacy checksum matches, the migration re-runs.

## Safety summary

- The app performs **no automatic backup** at startup or during migration; the `db.sqlite3.bak` from the manual endpoint is best-effort only. Your backup is your backup.
- The legacy database is opened read-only and is never modified, moved, renamed, or deleted by the migration. The only files the app ever writes next to it are `db.sqlite3.bragi-migrated` (the receipt) and, for the manual endpoint only, `db.sqlite3.bak` (best-effort copy).
- Two apps must not process the same library concurrently; stop the old one before starting the new.
- The migration is a copy, one transaction in the new database, all-or-nothing; a failed migration leaves the new database empty and the legacy one untouched.
- Audio files are located by the stored source/output paths; keeping the host mounts stable means nothing to re-point.
