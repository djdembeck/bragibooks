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

## Batch removal pass

The user asked for a "remove selected" action on Match so a batch of unwanted sources does not need one click per row. Per the user's two choices, selection is a **deliberate pass** entered from the batch bar, and the final delete is confirmed in a **dialog that lists every source name**.

**Direction contract additions.** The pass is a mode, not a second checkbox column: entering it turns the existing row checkbox into a removal selector, so the include-in-save set can never become a destructive set by accident. The bar takes the app's existing state-red wash (same as the per-row confirmation band); checked faces paint solid `#b3261e` with a white tick; picked rows take the red wash and "Selected for removal"; `N of M sources selected to remove` is the count; the files-stay sentence stays in the bar, the dialog lead, and the dialog footer. No new colors, no side-stripe, no shadow. The first detector pass flagged the initial 3px left rule as a side-tab tell and it was replaced with the wash.

**Behavior.** Entering the pass retires any open per-row confirmation. Selection spans every source, including failed lookups and no-match rows, and is independent of inclusion (inclusion survives the pass untouched). While the sequential deletes run, the review-mode controls and the Save action are disabled and their handlers early-return, so a mode switch or a save cannot race the deletion; the confirmation dialog can still be dismissed without corrupting the loop. `Select all sources` is tri-state. Escape or Cancel leaves the pass and returns focus to the entry button. Switching Batch/Focused ends the pass. Deletion runs one `DELETE /books/:id` at a time; a partial failure closes the dialog, keeps exactly the survivors selected, reports `N of M records were not removed` plus the cleaned cause and raw detail, scrolls the alert below the docked toolbar, and focuses the alert's Retry action, which reopens the dialog with exactly the survivors. Success announces `Removed N records.`, exits the pass, and focuses the entry button (or the page title when the batch is empty). Wide screens dock the bar under the measured sticky page head while the list scrolls; below 761px the commit docks to a fixed bottom bar and the list gets clearance. The dialog opens with Cancel focused.

**Verified.** Disposable database at `/tmp/bragi-rm` (11 pending sources; removed after verification), a local AudiobookDB stub (`BRAGIBOOKS_ALLOW_LOCAL_ADB=1`) with six real-shaped records, one failing lookup, and generated audio folders; the dev backend and Vite ran against it. Browser runs at 1440×900 and 1440×700 (scroll/dock) and 390×844 (fixed dock). Removed three sources (one auto-matched, one duplicate, one failed lookup) after confirming the dialog: the database dropped exactly those records and all twelve input folders and their `.m4b` files remained. Simulated a failing `DELETE` for one of two picks: one row removed, one survivor stayed selected, the alert sat fully below the docked bar with focus on its Retry, and Retry reopened the dialog for exactly that survivor (cancel returned focus to the commit control, a later retry success exited the pass and cleared the alert); simulated both failing: `No records were removed.` and both stayed selected. Checked select-all (11 of 11), indeterminate after unchecking one, Escape exit, the bar's Cancel exit, mode-switch exit, focus after entering the pass (first row checkbox), focus after dialog cancel (the commit control on desktop and in the mobile dock), focus after cancelling the per-row band (the row's Remove trigger), and the untouched per-row band. A 1.5s-delayed `DELETE`, dismissed with Escape mid-flight, kept the mode buttons and Save disabled until the delete resolved; then they re-enabled and the pass exited. `svelte-check` returned 0 errors and 0 warnings, the production build succeeded, and `impeccable detect` returned no findings.

**Finish review.** Independent `reviewer-design` pass returned two P2 concurrency findings and two notes; verdict "ship after fixes". Both findings are fixed and re-confirmed by the reviewer with no remaining blocker: mode changes and saving are guarded and disabled during batch deletion. The notes were applied too: the bulk checkbox's accessible name is "Select all sources for removal", and the row checkbox hit area is widened to 44px without moving the control. Final `svelte-check` and production build: 0 errors and 0 warnings; `impeccable detect`: no findings. Review captures live at `.impeccable/review/batch-removal-*.png`.

**Observed, not changed.** `stripExtension` (pre-existing) strips everything after the last period when a source title contains author initials, so `Ursula K. Le Guin - …` searches for `Ursula K`. That row correctly shows "No catalog match" and was used as a removal target; the truncation is out of scope here.
