---
version: 1
slug: "web-src-routes-match-page-svelte"
primary_target: "web/src/routes/match/+page.svelte"
related_targets: ["web/src/lib/components/Nav.svelte","web/src/routes/process/+page.svelte","web/src/routes/+page.svelte","web/src/routes/import/+page.svelte","web/src/routes/books/+page.svelte","web/src/routes/books/[id]/+page.svelte","web/src/routes/settings/+page.svelte"]
---

# Record workspace rollout

Mode: Operate. Scope: the shared application shell and all application routes. The user accepts the Record workspace placement and Queue structure, rejects the blue-heavy palette, and explicitly authorizes rollout alongside Match refinements. Preserve all existing import, catalog, settings, matching, processing, cancellation, and recovery behavior.

## Direction contract

**THESIS:** Scan proposed matches as a batch, resolve exceptions in a focused record inspector, then approve the included books together. No station rail, equipment vocabulary, decorative meters, or dashboard-card framing.

**OWN-WORLD:** Neutral white/graphite work surfaces with the original `develop` gold `#ffe08a` header and primary actions. Selection is pale gold; focus and secondary actions use dark gold/graphite, not cobalt. Restore the exact stacked-books artwork and favicon from `develop:importer/static/images/`, not a newly drawn mark. Public Sans headings; system UI body text.

**STORY:** Review approximately fifteen sources without visiting every inspector. See the source and proposed output name together, include or exclude matches deliberately, identify narration/edition differences, and open focused review when a record needs attention. Source identity, provenance, and unsaved state remain explicit.

**FIRST VIEWPORT:** Familiar top navigation, Match heading and counted batch approval, Batch/Focused review controls, and readable source-to-record comparisons. Focused review retains the source index and inspector. Compact Before/After names and fuller selected metadata belong in the inspector; rich alternatives identify author, narrator, duration, and available disambiguation. Queue retains its ready-work/current-processing hierarchy with history below.

**FORM:** Record workspace, candidate 1 (user selected the pick), seed:e1ec789bd1236fd8. Original sketch: `.impeccable/mocks/decision/record-workspace.html`. The user's later revisions pin neutral/gold colors, authentic develop assets, batch efficiency plus optional focus, richer metadata, and app-wide rollout; these supersede the earlier pale-blue reference treatment.

**FINISH:** unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## States and behavior

Keep source covers separate from selected catalog artwork; preserve missing-cover fallback, candidate loading/errors/retry, unmatched results, custom search, keyboard selection, full source/output paths, removal confirmation, and batch save semantics. Preserve queue selection, active SSE/poll lifecycle, cancellation, terminal history, and readable error/log details. No invented confidence scores or API behavior.

Batch inclusion must remain explicit when switching review modes. Save only included, loaded, internally consistent book/release selections. Expose release choice when available, and distinguish missing metadata from loading or errors. Naming is a proposed preview from selected metadata and the output scheme; the processor reports the actual final output path.

App-wide rollout is authorized by the user's latest review. The earlier reference gate below is historical evidence, not a current blocker.

## Earlier reference verification

- Validated `seed:e1ec789bd1236fd8` with the skill launcher: known roll on this machine.
- Captured Match and Queue at 1440px desktop and 390px mobile under `.impeccable/review/reference-{match,queue}-{desktop,mobile}.png`.
- Used a disposable database and generated audio to exercise import, automatic and manual catalog matching, keyboard selection, inline search, removal cancellation/confirmation, batch save, real processing output/history, and job cancellation. Real-library data was not mutated.
- Delayed an actual catalog response across a source switch; the earlier response did not replace the new source's results.
- Corrected Queue's partial Select all and raw-object job-state updates so asynchronous status changes render reactively.
- Mobile source controls use a bounded, single-column list with complete 72px rows. Workspace headings and the shared wordmark use self-hosted Public Sans 700 with its OFL license; body text remains unchanged.
- Frontend check: zero errors; two layout slot-deprecation warnings. The rendered-surface detector returned no findings. Full review used `reviewer-design` because the shipped Impeccable finish-reviewer selector is unavailable. Its verdict pass scored seed provenance, mobile source legibility, and self-hosted display typography resolved (`ship` at the correction-list scope, not whole-app approval).

## App-wide rollout verification

- Captured all seven routes at 1440px desktop and 390px mobile under `.impeccable/review/rollout-*.png`, including batch/focused Match, rich alternatives, actual loading, active Queue, and directory-picker states. A separate live Overview pair shows the unchanged 19-book library.
- Used a disposable database and 16 generated three-second audio sources for real import, 15-match approval, mixed inclusion, release selection, source removal without file deletion, provider-error recovery, directory retry/cancel, and job-history recovery. The actual processor produced a three-second AAC/MP4 output and a genuine running job was cancelled. The dev binary reported `Metadata applied: No`; the proof does not establish catalog tagging or proposed naming.
- Confirmed distinct catalog credit IDs sharing a display name survive save, release choice survives reselecting the same book, and Queue retains real record titles after reload. Matched/processing/pending counts use actual API state, not invented import backlog.
- Confirmed the actual Match publication date remains November 20, 2015 in both Los Angeles and Tokyo. Mobile Overview/Books titles preserve the reported full identities within two lines; secondary metadata may truncate.
- Final frontend check: zero errors and zero warnings; production build succeeded. Shipping raster provenance scan: 19 rasters, zero missing. The one final rendered detector pass found two helper-wrap advisories and an Import toolbar nested-container warning; the reviewer judged the toolbar warning a false positive, and both helper paragraphs were corrected.
- Fresh independent `reviewer-design` substituted for the unavailable shipped finish-reviewer. The full review returned three material findings; the final verdict scored release dates, mobile titles, and both helper paragraphs resolved, with no observed correction regressions: `ship` for that fix list. No dedicated QUALITY BAR card was persisted for this own-pick direction, so the card-specific ceiling remains unscored; this verdict is not a blanket whole-app quality certification.
- Replaced the obsolete Merge Bench `DESIGN.md` and `.impeccable/design.json` with the implemented neutral/gold system. Validated canonical YAML/JSON structure, all 34 normative colors against rendered source values, token/component references, exact narrative mapping, and eight-step source-hue OKLCH ramps. Rendered all ten framework-free component previews in a neutral shadow-DOM canvas at desktop/mobile sizes; confirmed original logo geometry, primary hover/keyboard focus, and narrow navigation reachability. Disposable QA services, generated-audio fixtures, and verification browser tabs were removed; the original live services remain running.

## Publisher and catalog-link repair

The user subsequently approved refreshing all 19 sample records after inspection found literal `Unknown` publishers and fabricated catalog IDs. A WAL-safe backup was saved at `dev/bragibooks_pre_refresh_20261023_200517.db`. Verified title/author matches and real release metadata replaced sample publishers, catalog identifiers, descriptions, dates, runtimes, languages, and credits; unreliable edition data used the catalog default release as approved. Local IDs, statuses, paths, covers, and job history were preserved. All 19 publishers now have catalog values; no placeholder book/release IDs remain.

Both public book-link constructors now use `/books/{id}`. Settings account creation uses `/users/signup`, verified against the catalog's own login link; `/users/register` was a profile route. All 19 stored public book destinations returned HTTP 200 with their expected canonical URLs and actual book titles. Browser smoke showed Random House Audio on record detail and Tantor Audio in focused Match, with valid plural book links. Frontend check and production build passed with zero errors and warnings. These are data/link corrections; the visual system is unchanged.
