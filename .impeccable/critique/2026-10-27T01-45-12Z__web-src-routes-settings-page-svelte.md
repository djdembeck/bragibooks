---
target: "new design surfaces: settings profiles, queue selector, books bulk selector"
total_score: 30
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/feat-import-per-group-output-directory-and-path/web/src/routes/settings/+page.svelte"
target_fingerprint: "sha256:107e67c89ba1ec7a264e39edd19eba378777768b0ea43e4b342993899831d6d8"
target_path: /home/djdembeck/orca/workspaces/bragibooks/feat-import-per-group-output-directory-and-path/web/src/routes/settings/+page.svelte
timestamp: 2026-10-27T01-45-12Z
slug: web-src-routes-settings-page-svelte
---
Method: dual-agent (A: AssessA design review · B: AssessB detector + browser evidence)

Target: saved output profiles surfaces — `web/src/routes/settings/+page.svelte` (profiles section + modal), `web/src/routes/process/+page.svelte` (queue selector), `web/src/routes/books/+page.svelte` (bulk requeue selector). Operate mode, Record workspace design system, WCAG 2.2 AA.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Live book-counted summary; profile list loads with bare skeletons, no count |
| 2 | Match System / Real World | 4 | Real resolved preview paths, zero jargon, inheritance copy reads plainly |
| 3 | User Control and Freedom | 3 | Modal opens with focus on the X (close) button, not the safe field |
| 4 | Consistency and Standards | 3 | Copy drift: "Use global output directory" vs "Uses global setting"; "Requeue selected" vs "Start processing" |
| 5 | Error Prevention | 2 | Adjacent tokens in the format builder silently merge into one path segment; delete-confirmation focus sits on "Yes, delete" |
| 6 | Recognition Rather Than Recall | 3 | No resolved destination preview on profile rows; content revealed only after selection |
| 7 | Flexibility and Efficiency | 3 | Progressive disclosure is right; modal tab path crosses 10 palette chips first |
| 8 | Aesthetic and Minimalist Design | 3 | Restrained and on-system; single-cell profile panel leaves a half-empty grid at 1440 |
| 9 | Error Recovery | 4 | 409 in-form; profile-list failure degrades both queue surfaces without blocking |
| 10 | Help and Documentation | 2 | Empty optional format field shows a red "invalid, not applied" banner on every modal open |
| **Total** | | **30/40** | Strong — targeted fixes available |

## Design Specificity Verdict

**LLM assessment:** Authored product UI, not a generic admin template. Profile rows read like the queue rows (neutral hairlines, mono paths), the inheritance language is threaded from Settings into both queue surfaces, and the delete grammar is the page's existing red-wash confirmation panel, not a bolted-on modal. The one interruption is the shared PathFormatBuilder inside the profile modal, whose empty-state "format is invalid, not applied" contradicts the field's "(optional)" framing.

**Deterministic scan:** Zero findings. CLI `impeccable detect` across the five markup files exited 0 with `[]`; the injected in-page detector reported "No anti-patterns found" on `/settings`, `/process`, and `/books`. Nothing the LLM missed; no false positives to triage.

**Visual overlays:** Injection succeeded in a dedicated tab; console output came from live detector runs in each page. Note: Assessment B ran before the seeded books table was repaired (NULL `release_date` → books API 500 → Queue/Books rendered their error state), so the selector-in-use state was evidence-verified only at source level and by Assessment A's live walk after the repair.

## Overall Impression

The feature is calm, on-system, and its failure isolation is exemplary. It manufactures exactly one false alarm (the empty-format "invalid" banner) and leaves one genuinely dangerous quiet spot (adjacent format tokens merging segments). Biggest opportunity: make the builder's empty optional state honest and warn — not silently validate — the one path shape that destroys merge destinations.

## What's Working

1. Inheritance language. "Use global output directory" / "Use global path format" in rows and "applies only to these N books in this batch" in the live summary make partial profiles self-describing everywhere. The summary updates live as selection changes.
2. Failure isolation, verified on both surfaces. With the profiles endpoint 500-ing, Settings keeps its global form fully editable with an in-section Retry; Queue keeps "Global settings" queueing enabled behind a muted one-liner.
3. Delete grammar reuse. Red-wash panel, named consequence ("Queued jobs that used it keep their stored output settings"), focus moved into the panel — page-native, not a modal library.

## Priority Issues

**P1 — False "invalid" state on the optional format field (Settings modal, every open).** Empty draft shows the builder's "Add at least one path element… / format is invalid, not applied" while the label says optional and help says leave empty. Fix: suppress the builder's error display for an empty value in the profile modal (or an `allowEmpty` flag); keep real validation for non-empty values.

**P1 — No separator warning for adjacent tokens (PathFormatBuilder, all uses).** `{author}{title}` yields "AndyWeirTheMartian" with zero signal — the single most likely data-corruption path in the feature, and the preview renders the bad path in full, unstyled. Fix: warning (not error) in the builder when two tokens/text parts are adjacent without a separator.

**P2 — Destructive focus: "Yes, delete" is the focused target (Settings).** A keyboard user's first Enter inside the confirmation deletes. Fix: focus the ghost Cancel in `startDeleteProfile`.

**P2 — Profile identity vanishes after queue (Queue).** After success the selector resets and nothing records that the batch ran under a profile; two back-to-back batches (profile then global) are indistinguishable in the active-jobs panel. Fix: tag the just-queued jobs on the page with the resolved summary (dir · scheme), one muted line under the job heading.

**P3 — Copy currency/density (three surfaces).** "Use global output directory" (rows) vs "Uses global setting" (summaries); "Requeue selected" vs "Start processing" for the same verb. Fix: per-field "Uses the global output directory / path format" in summaries; "Start requeue" vs "Start processing".

## Persona Red Flags

- **Alex (power user, 10 books, multiple profiles):** Profile rows give no at-a-glance destination — editing is the only way to compare three formats; the silent post-queue reset to Global is exactly the state she'll forget before wave 2. Failing elements: `.profile-row__values`, post-queue amnesia.
- **Jordan (first-timer, first profile):** Leaves both fields empty, gets a red "both empty" alert plus an independent red "format invalid" banner on a field marked optional — three reds on a form never filled; focus lands on X, so the first tab stops are close/cancel buttons.
- **Sam (distracted, re-queueing 2 of 7):** Books' bulk select-all is immutable per-row; the count is the only control, and the profile summary's "applies only to these 7 books" reads as reassurance rather than a check. (Pre-existing surface shape; the profile copy amplifies the trust.)

## Minor Observations

- "Add profile" secondary vs "Create profile" primary gold — defensible (page context vs commit), one size down from the gold rule.
- "No saved profiles yet." has no CTA link, though Add sits right above.
- `.profile-row__name` breaks unbroken names mid-word (`overflow-wrap: anywhere`); no truncation pattern elsewhere on the page.
- Books bulk select is inline-styled while Queue's uses `.profile-select` — duplication to converge.
- The profile section's single-cell `settings-grid` leaves a half-empty panel at 1440.
- Books bulk bar summary stacks three global-mentions in one line.

## Questions to Consider

1. Should the profile modal's preview show the seven real books being queued, not a hard-coded Audible title?
2. Is post-queue amnesia a feature? Session-level "last used" memory would make the reset legible; both are defensible — the choice should be visible.
3. With global config, profile, and per-job snapshot all owning "output destination", where does an operator audit where a finished file landed? The job history is silent about it.
