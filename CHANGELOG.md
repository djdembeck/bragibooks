# Changelog

All notable changes to this project will be documented in this file. See [standard-version](https://github.com/conventional-changelog/standard-version) for commit guidelines.

## [Unreleased]

### Fixed

- Release assets are uploaded with their plain file names
  (`bragibooks_2.0.0_checksums.txt`, `bragibooks_Darwin_arm64.tar.gz`, …).
  New uploads no longer attach a `bragibooks-publish-v1;…` display label,
  which the GitHub web UI rendered as the asset name and made the release
  page show long, unreadable names. The v2.0.0 archives previously carried
  such labels, which a one-time manual API cleanup cleared; with no label,
  a retry now reconciles them through the strict content-digest branch —
  a GoReleaser rebuild is not bit-reproducible, so a v2.0.0 publication
  retry after a partial failure fails closed unless the preserved archive
  bytes match the rebuild exactly (stale assets resolve out of band). No
  code path re-uploads or relabels them.
- Release draft titles are the bare version (`vX.Y.Z`) instead of
  `Release vX.Y.Z`.
- Resume contract: a new unlabeled asset is reused on a partial-upload
  retry only when its content digest matches the freshly built artifact.
  An existing unlabeled archive that differs from a rebuild still fails
  the run before any upload; that rule, previously pinned for the legacy
  path only, now applies to every plain-named asset, so the stale asset
  must be resolved out of band. Labeled historical assets keep the
  provenance-bound reuse that tolerates a byte-differing rebuild.

### Features & Improvements

- Release archives embed the version and use lowercase platform names
  (`bragibooks_2.1.0_linux-amd64.tar.gz`, `bragibooks_2.1.0_darwin-arm64.tar.gz`,
  …), and a Windows amd64 archive is now built alongside the existing
  Linux and macOS amd64/arm64 archives.
- Kept the container's default HTTP port at `8000`, the same port the Python image used, so existing Docker port mappings keep working after upgrading; `SERVER_PORT` still overrides it.
- Narrowed development Docker media access to configurable input/output directories rather than mounting the developer home directory; pinned Docker build, runtime, and media-tool image digests for reproducible builds, with Renovate digest updates.
- **Per-book selection and bulk book actions on the catalog** (#506)
  - The Books catalog selects books individually: every row carries a checkbox, and the header checkbox is tri-state (all / some / none) that selects or clears every book matching the current filter and search (server-capped at 1,000 via the `GET /books?all_ids=1` ID list). The bulk bar's count follows that selection, and the bulk actions act on that explicit selection only — never on an implicit whole-catalog scope.
  - The bulk bar offers three operations over the selected books: requeue for processing (with the chosen output profile's summary rendered under its picker as a caption, so the bar's actions stay on one line), mark as done, and remove. The bar reflows to a full-width stack on narrow screens.
  - Removal is confirm-gated: the red Remove… action opens a dialog that lists the affected book titles, loads off-page titles with a capped fetch pool, and deletes only after that confirmation. The dialog's close control is disabled while deletes run, and files on disk are never touched.
  - Server-side, `DELETE /api/books/:id` refuses to delete a book whose processing job is still queued or running, so a bulk removal cannot orphan a live job.
  - Bulk done and remove re-fetch the catalog afterward so badges, totals, and pagination match the new state, and a failed operation releases the bar's lock instead of leaving it stuck. A failed pre-removal title fetch gets its own Retry that rebuilds the confirmation list.
- **Missing-release handoff on Match**
  - When an active source has no usable catalog record, Match offers a "Missing from AudiobookDB?" link. A complete, unambiguous source ASIN is carried to `audiobookdb.org/add` for prefill; absent, multiple, incomplete, or unreadable evidence opens the bare add page with truthful copy.
  - The projection is read-only from `book_source_metadata` on the pending list; `books.asin` and processing are unchanged. Batch no-match rows, focused no-match, and the zero-result search repeat expose the handoff only after a settled lookup; search failures never suggest adding. The wrong-match editor footer remains an operator escape while a search is pending or failed.
  - The focused search repeat and the wrong-match footer use the short “Add to AudiobookDB” label; the batch no-match rows use the sentence label “add it to AudiobookDB”. The block keeps the full “Add this audiobook…” phrase and ASIN evidence, and the wrong-match footer describes the source audiobook rather than an edition. A ≤370px wrap safety keeps the compact actions inside a 320px viewport.

### Security

- Validate Host authorities before all HTTP routes to reject DNS-rebinding names, malformed hosts, and forwarded-host spoofing. Preserve auth-free LAN literal-address access (including published container ports), external API keys, and CSRF; add exact DNS/public-address opt-ins through `SERVER_ALLOWED_HOSTS` / `server.allowed_hosts` and reuse non-wildcard listener hosts. The development Compose profile explicitly allows its `bragibooks-dev` backend service authority so Vite's API and cover proxies keep working.

### Major Updates

- **Django 5.2 LTS Modernization** (#407)
  - Upgraded from Django 4.2 to Django 5.2 LTS
  - Fixed wildcard ALLOWED_HOSTS security issue
  - Improved CSRF_TRUSTED_ORIGINS parsing
  - Added production security headers
  - Migrated packaging to modern `pyproject.toml` format
  - Switched from python-Levenshtein to rapidfuzz for better performance

- **m4b-merge migrated to Rust** (#406)
  - Replaced Python m4b-merge library with high-performance Rust binary
  - Added multi-stage Docker build for Rust compilation
  - Improved processing speed and reliability
  - Added retry logic for subprocess operations

### Features & Improvements

- **Unified release publication on reserved versions** (#484)
  - Release lifecycle and asset lookups are now draft-aware and paginated with an exact `tag_name` match; duplicate records fail closed, and a 403/5xx/page failure stops the run before any create or upload.
  - `--latest` is computed from published non-draft non-prerelease numeric SemVer: a new highest stable advances GitHub Latest, a delayed lower stable publishes with `--latest=false`, and an already-published retry is a no-op. Mutable `latest`/major/minor Docker aliases are no longer written.
  - Before any upload, the complete planned asset set (preserved immutable archives plus the pending build) is built and the checksum manifest is verified against those actual bytes. An existing labelled archive may differ from a rebuild and is preserved; a missing manifest is generated from the combined verified digests so a valid partial retry can finish; an immutable manifest that cannot describe the combined set stops the run with no writes. Existing unlabelled-mismatch and provenance rejections are preserved.

- **Recorded activity shows what happened** (#440)
  - `processing_jobs` now carries a stable `action` key (`merge` | `metadata_update` | `import`); the migration backfills existing rows as `merge`.
  - Imports and metadata saves write terminal `done` audit rows, so the record's activity list no longer only shows processing runs. These rows never affect queuing or the orphan sweep, which key on `queued`/`running`.
  - `GET /api/jobs` and `GET /api/jobs/:id` expose `action`; the Queue cards and the book record's activity list show a quiet sentence-case operation label (M4B merge / Metadata update / Import) beside the status chip. Unknown stored operations read "Unknown action" instead of being guessed as merges.
  - `processing_jobs` gains a `detail` column (migration v8) exposed by `GET /api/jobs` and `GET /api/jobs/:id`; pre-v8 rows are NULL, and import rows show their source path.
  - Job completion reads "Completed" across merges, imports, and metadata saves; an empty record reads "No recorded activity yet."

- **Live Queue output and real destinations**
  - Active Queue cards now stream `m4b-merge` stdout over SSE as it runs, with a reconnect snapshot from the in-memory job state instead of waiting for the terminal row.
  - Queue cards show the book cover and author, an indeterminate processing rail and the current engine stage, and the destination `m4b-merge` reports (the completed `output_file`, or the live path with its per-job `.tmp` segment removed). Until the engine resolves the path, the queue-time profile is labeled "Output profile", never "Destination".
  - `GET /api/jobs` and `GET /api/jobs/:id` expose a profiled job's stored output directory and path format, so a restored active job keeps its destination context.
  - The live log stays where the operator scrolls it (re-pinning only at the bottom) and is keyboard-scrollable; running jobs lead the active section, queued jobs keep their enqueue order across reloads, and a book with a queued job no longer remains selectable in Ready to process.
  - A transient stream drop reconnects with the server's accumulated output instead of freezing the log, and cancelling a running job keeps the output it produced before the process was killed.

- **Saved output profiles with queue-time snapshots**
  - Added named output profiles (output directory and/or relative path format) managed on the Settings page via `/api/output-profiles` (list, create, update, delete). Blank fields inherit the global values.
  - The Queue page and the Books bulk requeue action select one profile per batch. The selection is resolved once at queue time, snapshotted into each created job's arguments, so already-queued jobs are unaffected by later profile or Settings changes; no profile is stored on a book.
  - Removed the obsolete per-book and per-import output override fields, UI, and migrations (migration v8 adds the `output_profiles` table and drops the unreleased per-book columns).

- **Record workspace rollout**
  - Restored the original gold palette, stacked-books logo, and favicon; applied the shared top navigation, neutral work surfaces, and self-hosted Public Sans headings across Overview, Import, Match, Queue, Books, book detail, and Settings.
  - Added counted batch match approval with explicit inclusion and optional focused review. Source and proposed output names stay visible; the processor's actual output path remains distinct from the preview.
  - Expanded selected metadata, responsive description disclosure, and catalog choices with author, narrator, runtime, and available release details. Preserved distinct credits that share a display name and the selected release when reselecting a record.
  - Retained Queue's ready-work, active-processing, and history hierarchy; corrected partial Select all, reactive job updates, and book titles after reload.
  - Preserved keyboard selection, source-removal confirmation, error recovery, and folder-picker cancellation. Added truthful loading/removal states, executed-search labels, and same-folder retry after a directory error.
  - Kept catalog publication dates stable across time zones, made mobile Overview and Books titles readable on two lines, and rebalanced helper-text wrapping.
  - Corrected AudiobookDB book links to `/books/{id}` and account-creation links to `/users/signup`.
  - Queue job cards and book job history now show elapsed time per job. Queued jobs report time spent waiting, running jobs tick each second, and finished jobs keep second precision up to an hour.

- **Match comparison hierarchy**
  - Replaced the filename-led Before/After block with a direct Source → Proposed match comparison in batch and focused review. Output naming is a secondary preview with accessible full paths and configuration states.
  - Made narrator, runtime, publisher, release date, language, and available edition distinctions visible in batch review. Kept focused catalog metadata and source artwork, including on phones.
  - Replaced successful match chips with plain selection-method text and an unboxed AudiobookDB link. Grouped inclusion and editing controls outside the comparison; per-source Remove now lives under More and still requires confirmation.
  - Prioritized narrator/runtime over secondary release metadata, consolidated focused editing into one disclosure, and separated mobile entries with clear boundaries and gaps. Kept Remove in the same toolbar position in both modes; automatic choices are explicitly title-based, not confidence scores.
  - On phones, shortened the Match intro to the record-check reminder and handed focused review to the selected inspector after a source or Review is chosen; the focused Proposed match header now uses the same direction cue as batch.
  - Announced per-row lookup and search failures, kept the Technical detail disclosure at a 24px target, and made the header and Match commit row reflow at 200% text. The nav height now follows the rendered header so sticky offsets stay correct when labels wrap.
  - Unified disabled buttons at 0.45 opacity across variants and centered the active destination in the scrollable nav at narrow or large-text widths.

- **Source-evidence presentation**
  - Added a small match-origin badge beside Proposed match and subtle supplied-evidence field indicators, with independent source/catalog values in the existing Source details disclosure.
  - Current records remain explicitly "metadata not read" with no agreement checks. Optional candidate-scoped reports suppress stale verification when a book or edition changes; exact book identity never implies edition certainty.
  - Aligned origin labels and evidence icons, explained exact-ID scope and runtime approximation, and added a neutral information indicator for incomparable runtime. Tooltips use concise production-facing copy; full provenance, reasons, and preview diagnostics stay in Source details. Explanations work on hover, keyboard focus, or tap.
  - Runtime popups now align source/catalog durations, show longer/shorter direction, and distinguish gap severity in whole minutes. Explicit measurement precision prevents rounding artifacts from becoming confident differences; equality, sub-minute gaps, overlapping rounded measurements, and unavailable comparisons remain neutral. These presentation bands do not change matching confidence.
  - Separated Source and Catalog values onto aligned rows in all differing-field popups, including narrator, and in formatted-only or unavailable-precision runtime comparisons. Long credits wrap without merging the two values.
  - Added a dotted underline and a 24px transparent hit area to inline evidence triggers, so the comparison reads as actionable on touch without boxing the field. Per-field agreement checks are unchanged.
  - This is visual wiring only: no source tag extraction, ID lookup, or backend transport was added. Match-entry extraction is tracked separately in #488.

- **Match removal**
  - Remove a single source through More, or open Remove… in the page header and select records in the independent removal dialog. Only Bragi Books records are deleted; files on disk stay untouched.
  - When two sources share a folder name, the removal list adds a distinguishing parent-folder breadcrumb and includes that context in the checkbox's accessible name; unique names keep the compact row.
  - Failed deletions keep the survivors selected and report the count and cause, so a partial batch can be retried. The removal selection is separate from "included in this save" and never changes inclusion.

- **Selectable output path format**
  - Replaced the free-text output path format field in Settings with a token builder: token/text/separator parts, live example resolved against a real sample record, and keyboard reorder (Alt+Arrows) with wrap-around selection.
  - Warns when literal text joins an adjacent token in the same path segment, annotates the example when the format is invalid, and documents the keyboard model in a help line.
  - Rejects settings updates with an invalid format server-side with an HTTP 400.
  - Mutes the non-substituted `{asin}` chip; on touch, parts show a persistent removal control and 40px minimum targets.

- **Loading States** (#409)
  - Added loading indicators to the import page for better UX
  - Users can now see when operations are in progress

- **Performance Optimizations**
  - Skip chown operations when file ownership is already correct
  - Reduced container startup time

- **Dependency Updates**
  - Upgraded kombu to v5.6.2
  - Upgraded whitenoise to v6.11.0
  - Migrated Levenshtein to rapidfuzz for faster fuzzy matching

- **Docker Improvements**
  - Fixed gosu installation for Alpine v3.15 compatibility
  - Fixed Docker container startup failures
  - Improved file permission handling

### Bug Fixes

- Fixed output path parsing regex for Rust m4b-merge binary
- Fixed installation to use pyproject.toml for all Django dependencies
- Fixed ALLOWED_HOSTS to use environment variable with sensible defaults
- Fixed CSRF_TRUSTED_ORIGINS to return empty list instead of string when not set

---

## [1.2.3](https://github.com/djdembeck/bragibooks/compare/v1.2.2...v1.2.3) (2024-08-07)


### Bug Fixes

* 🐛 fix redirect check ([#321](https://github.com/djdembeck/bragibooks/issues/321)) ([5a9429d](https://github.com/djdembeck/bragibooks/commit/5a9429dbdd6859509761eff1054d57168ea1616a))
* 🐛 required attr was in the wrong place  ([#319](https://github.com/djdembeck/bragibooks/issues/319)) ([1cd064a](https://github.com/djdembeck/bragibooks/commit/1cd064aad41a192a42c2697bf665198f6a8770c6))

## [1.2.2](https://github.com/djdembeck/bragibooks/compare/v1.2.1...v1.2.2) (2024-08-07)


### Bug Fixes

* :bug: avoid using split for url checking ([c934c61](https://github.com/djdembeck/bragibooks/commit/c934c61dc674bfa62de70d1a140d87caa1f7b489))

### [1.2.1](https://github.com/djdembeck/bragibooks/compare/v1.2.0...v1.2.1) (2023-06-09)


### Bug Fixes

* button disabled bug [#178](https://github.com/djdembeck/bragibooks/issues/178) ([#183](https://github.com/djdembeck/bragibooks/issues/183)) ([ab378d5](https://github.com/djdembeck/bragibooks/commit/ab378d518b37c22535ef4ccae5a2832b093e94b2))

## [1.2.0](https://github.com/djdembeck/bragibooks/compare/v1.0.0...v1.2.0) (2023-05-18)


### Features

* add 'x' to remove search selection ([#166](https://github.com/djdembeck/bragibooks/issues/166)) ([f1f8f6d](https://github.com/djdembeck/bragibooks/commit/f1f8f6ddfcf30a945f263a69ac949c9ed728b460))
* add cover images to search results ([#167](https://github.com/djdembeck/bragibooks/issues/167)) ([e5acbc0](https://github.com/djdembeck/bragibooks/commit/e5acbc0e32b2a3e4d2803994f2352db0763080d0))
* updated the file picker ([#153](https://github.com/djdembeck/bragibooks/issues/153)) ([aed75dd](https://github.com/djdembeck/bragibooks/commit/aed75ddbffc939e0b394fe7fc063fcc92cffeac4))


### Bug Fixes

* :bug: entrypoint wasn't setting user permissions for processes ([b3bd8a7](https://github.com/djdembeck/bragibooks/commit/b3bd8a765c040ce14d57e0b483fdf689669b976b))
* updates to Dockerfile and fix bugs ([#163](https://github.com/djdembeck/bragibooks/issues/163)) ([3e0a7b3](https://github.com/djdembeck/bragibooks/commit/3e0a7b3f13b96307e43ad47222f1b3d4fd1fc726))

## [1.0.0](https://github.com/djdembeck/bragibooks/compare/v0.3.7...v1.0.0) (2023-04-18)


### Features

* Add auto search for books and Add Celery queue and task runner ([#145](https://github.com/djdembeck/bragibooks/issues/145)) ([fb17706](https://github.com/djdembeck/bragibooks/commit/fb17706b9e8e3a50546545ff16d730b7affedcde)), closes [#27](https://github.com/djdembeck/bragibooks/issues/27) [#85](https://github.com/djdembeck/bragibooks/issues/85)


### Bug Fixes

* :bug: search single file now removes extension ([d05a97a](https://github.com/djdembeck/bragibooks/commit/d05a97a322ba8873d63ea55b7e59bc24fe70f229))
* :bug: searches with `&` character could fail ([3ac90c1](https://github.com/djdembeck/bragibooks/commit/3ac90c1c8196db2b63242c9e52af23ca69e6500c))

### [0.3.7](https://github.com/djdembeck/bragibooks/compare/v0.3.6...v0.3.7) (2023-02-24)

### [0.3.6](https://github.com/djdembeck/bragibooks/compare/v0.3.5...v0.3.6) (2022-09-21)


### Features

* allow setting CSRF_TRUSTED_ORIGINS via envvar ([f5c68c4](https://github.com/djdembeck/bragibooks/commit/f5c68c46ab3747f340a048ee96781bda7fa70303))


### Bug Fixes

* :bug: pass original path to `m4b-merge` so it knows what to move to the completed folder ([101e8f2](https://github.com/djdembeck/bragibooks/commit/101e8f25b6c2ecae5715e129e33f425ac485e048))

### [0.3.5](https://github.com/djdembeck/bragibooks/compare/v0.3.4...v0.3.5) (2022-06-12)


### Features

* :sparkles: Allow setting CSRF origins for reverse proxies ([ad91f25](https://github.com/djdembeck/bragibooks/commit/ad91f25050d796ca8ea5bda1e5416f50df4fa1a5))


### Bug Fixes

* :bug: fix cpu count not being set correctly ([748f980](https://github.com/djdembeck/bragibooks/commit/748f98005e8ce0a7852ce84b9ffad48d49f1fba1))
* **docker:** :bug: fix for Docker permissions initialization ([8b8386f](https://github.com/djdembeck/bragibooks/commit/8b8386f7a22c43c6b6532cebbbc8fa65cfc1e277))

### [0.3.4](https://github.com/djdembeck/bragibooks/compare/v0.3.3...v0.3.4) (2022-01-11)


### Features

* :lipstick: add versions to footer ([0969087](https://github.com/djdembeck/bragibooks/commit/0969087b1f96e3dd4e81960e938329e1758dc9b2))

### [0.3.3](https://github.com/djdembeck/bragibooks/compare/v0.3.2...v0.3.3) (2021-12-06)


### Features

* :sparkles: connect output scheme setting to m4b-merge path format ([5521be4](https://github.com/djdembeck/bragibooks/commit/5521be486a260222f01b9ac492f16223b6cdc524))

### [0.3.2](https://github.com/djdembeck/bragibooks/compare/v0.3.1...v0.3.2) (2021-11-18)


### Features

* :sparkles: add settings page ([bf90b57](https://github.com/djdembeck/bragibooks/commit/bf90b57fed20e57ed1f23ef82bad0a378a80cc10))


### Bug Fixes

* :bug: fix order of setting import ([100853a](https://github.com/djdembeck/bragibooks/commit/100853a6202a1d54cc0e8b9538500f26f521566b))
* :bug: show durations longer than 24hrs ([098f376](https://github.com/djdembeck/bragibooks/commit/098f37672ce3f0a1677b016811c0d70c88c26b97))
* **docker:** :ambulance: fix pip package location ([644a422](https://github.com/djdembeck/bragibooks/commit/644a4221512abf844877e56dcdac5b90df3acb3e))
* **model:** :bug: allow runtime to be 0 for podcasts ([8e99513](https://github.com/djdembeck/bragibooks/commit/8e99513643ddaebe27e4c66b73cf4bd673993363))

### [0.4.1](https://github.com/djdembeck/bragibooks/compare/v0.3.1...v0.4.1) (2021-10-18)


### Bug Fixes

* :bug: show durations longer than 24hrs ([098f376](https://github.com/djdembeck/bragibooks/commit/098f37672ce3f0a1677b016811c0d70c88c26b97))
* **model:** :bug: allow runtime to be 0 for podcasts ([8e99513](https://github.com/djdembeck/bragibooks/commit/8e99513643ddaebe27e4c66b73cf4bd673993363))
