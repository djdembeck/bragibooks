---
target: all new or changed Match surfaces (batch, focused, evidence popovers, removal dialog)
total_score: 35
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 0
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/fix-select-to-remove-system/web/src/routes/match/+page.svelte"
target_fingerprint: "sha256:08cbe1b2d78561756e227b2aaf9b7bb6efda7126a0ec13587c686d85949f247a"
target_path: /home/djdembeck/orca/workspaces/bragibooks/fix-select-to-remove-system/web/src/routes/match/+page.svelte
timestamp: 2026-11-02T18-06-31Z
slug: web-src-routes-match-page-svelte
closed: true
---
Method: dual-agent (A: reviewer-design · B: task-light-local)

Assessment A reviewed the source and prior captures; it had no browser automation this session, so its visual claims are source-based. Assessment B drove the live preview at 1440×1000 and 390×844, ran the detector, and measured contrast, overflow, and popup fit. The parent verified the current batch and focused compositions live before synthesis.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|------:|-----------|
| 1 | Visibility of System Status | 4 | Counted Save, ready/included counts, removal count, and per-row state are explicit; live verified. |
| 2 | Match System / Real World | 4 | Source → Proposed match, narrator/runtime, and files-on-disk copy fit the audiobook-folder workflow. |
| 3 | User Control and Freedom | 3 | Mobile focused selection does not bring the inspector into view; the 220px source index remains between the tap and the record. |
| 4 | Consistency and Standards | 4 | Shared evidence rows and stable Save/Remove positions across both modes; one minor directional-cue deviation. |
| 5 | Error Prevention | 3 | The removal dialog can present indistinguishable same-named folders; the destructive selection is otherwise separate and zero-disabled. |
| 6 | Recognition Rather Than Recall | 3 | Full source paths exist only in a hover `title` in the removal list; long folder names truncate in the focused index. |
| 7 | Flexibility and Efficiency | 4 | Batch default, select-all-ready, bulk removal, and a focused exception path; no custom shortcuts. |
| 8 | Aesthetic and Minimalist Design | 3 | Per-row repeated labels and a 255px phone head add noise; the comparison itself is calm. |
| 9 | Error Recovery | 4 | Retry lookup/edition, failed-survivor retention, and stale-evidence suppression are clear. |
| 10 | Help and Documentation | 3 | Contextual popovers are useful, but the inline evidence affordance is quiet on touch. |
| **Total** | | **35/40** | **Good — address weak areas, solid foundation** |

## Design Specificity Verdict

**LLM assessment: authored for Bragi Books, not category-interchangeable.** The batch row is a real source-to-record comparison: a quieter source column with local art, folder identity, and a full-identity disclosure beside a catalog record that leads with title, author, narrator, and runtime before quieter release metadata. Focused review preserves source navigation, source and catalog art, and one "Change book or edition" disclosure instead of becoming a generic metadata form. Counted Save, the separate removal selection, the AudiobookDB provenance link, and “files on disk are not touched” are specific to this operator’s batch pipeline.

**Deterministic scan:** `impeccable detect` returned exit 0 with `[]` — zero findings across the four changed files. Live injection reported `[impeccable] No anti-patterns found.` No false positives to filter. Measured contrast passed everywhere: the lowest body-text ratio was 5.89:1 for the 12.5px “0 of 7 selected” line; 12px popup text measured about 4.9:1; 11px “Matched as:” measured 8.67:1. No horizontal overflow at either width; the removal dialog and both evidence popups fit their viewports.

**Visual overlays:** Injection succeeded in a headless tab and the console reported no anti-patterns; no human-visible overlay was presented.

## Overall Impression

The redesign fixes the original quick-glance failure. Source and Proposed match are now structurally distinct, actions sit after the comparison, and destructive removal is deliberately separated from inclusion. The remaining friction is concentrated on phones: moving from a source in the index to its inspector, and confirming which same-named folder a destructive checkbox will delete. The core composition is strong; the next pass should be narrow.

## What's Working

- **Source → Proposed match is operationally specific.** The amber conflict mark and “Conflicting evidence” label surface the one field that needs attention without inventing a confidence score; the aligned narrator/runtime rows make the comparison inspectable.
- **Action semantics are separated.** “Include in save” never deletes, counted Save states its scope, and the removal dialog owns an independent checked set with a zero-selection-disabled red action.
- **Evidence is honest under uncertainty.** Runtime comparison aligns Source and Catalog, refuses a direction when rounded ranges overlap, and keeps unread or unavailable fields quiet.

## Priority Issues

### [P2] The removal dialog cannot distinguish same-named source folders

**What:** `sourceBase()` returns only the last path segment (`web/src/routes/match/+page.svelte:1011`). Each removal row shows that leaf name, puts the full path only in a `title` attribute (`:2500–2501`), and gives the checkbox an accessible name containing only the leaf (`:2483`). Two imported folders with the same leaf name and the same matched title are indistinguishable in a destructive list, especially on touch where `title` never appears.

**Why it matters:** This is the one place where the operator authorizes irreversible record deletion. Ambiguity here undermines trust in the whole batch flow; the “files are not touched” reassurance explains scope but not identity.

**Fix:** Show a parent-folder breadcrumb in each pick row (for example, the parent directory above the folder name, or a two-line truncated path), and include that context in the checkbox’s accessible name. Keep the full path in Source details.

**Suggested command:** `$impeccable harden`

### [P2] Mobile focused review does not hand off from the source index to the inspector

**What:** `setActive()` only updates `activeId` and closes the picker (`+page.svelte:205–208`). At 390px, `.match__index-list` is a nested 220px scroll region (`:4123`), and the inspector follows it. Selecting a different source leaves the viewport on the index; the selected record is below the fold. The user must scroll the page, not just the list.

**Why it matters:** Focused review is the exception path for records that need attention. On a phone, every source change costs an extra scroll and a re-orientation, which is exactly the friction the mode is meant to remove.

**Fix:** On mobile, scroll and focus `.match__inspector` after `setActive()` from the index (not after Previous/Next), or collapse the index to a compact 44px switcher while the inspector is active. Preserve the current desktop two-column behavior.

**Suggested command:** `$impeccable adapt`

### [P3] Inline evidence triggers are quiet and small on touch

**What:** `.match__origin` and `.match__field-trigger` reset padding to zero and have no minimum target size (`+page.svelte:3280`); the origin label is 12px, and the runtime trigger measures about 23px high while the origin measures about 18px. The popovers fit and read well, but nothing persistent says the value opens a comparison beyond the amber mark and `cursor: help`.

**Why it matters:** The page’s own instruction says to check narrator and edition. A first-timer may read the amber triangle as a static warning rather than a control, and the touch target is below the comfortable 24px minimum used elsewhere.

**Fix:** Add a subtle persistent disclosure cue (a small chevron or dotted underline) and a transparent hit area of at least 24×24px around the inline trigger, without turning the field into a boxed button.

**Suggested command:** `$impeccable clarify`

### [P3] The directional cue is inconsistent between batch and focused review

**What:** Batch uses an arrow glyph in “→ Proposed match” on desktop and a down arrow on phones; focused desktop renders plain “Proposed match” with no cue (`+page.svelte`, proposal-header markup). The seam and the Source label still communicate order, but the same comparison uses two grammars.

**Why it matters:** The source-to-catalog direction is the central mental model of this screen. Reusing the cue in focused review costs nothing and makes the two modes feel like one system.

**Fix:** Use the same directional cue in the focused inspector, or remove it from batch and let the seam carry the order. Do not add a second arrow elsewhere.

**Suggested command:** `$impeccable polish`

### [P3] The phone head delays the comparison to the fold

**What:** At 390×844, the page head is 255px and the select-all bar adds 44px; the first row starts at 498px and the Narrative/runtime line sits at the bottom edge of the viewport. The four-line intro repeats what the rows demonstrate.

**Why it matters:** The quick-glance test is now close on mobile — the proposed header and conflict label are visible — but the operator still reads a paragraph before the evidence. Trimming the intro would put the narrator conflict fully in the first viewport.

**Fix:** On phones, shorten the intro to one line (“Compare source with the proposed catalog record; choices are saved with the batch.”) or collapse it after first visit. Keep the full copy on desktop.

**Suggested command:** `$impeccable distill`

## Persona Red Flags

**Alex (Power User):** On a phone, changing the active source in focused review does not bring the inspector into view, so rapid exception review costs two scroll gestures per book. There are no arrow-key shortcuts for Previous/Next; the buttons work, but a keyboard-heavy operator must reach for the pointer.

**Jordan (First-Timer):** “Selected by title” and “Exact book ID · edition unverified” require interpretation. The amber narrator mark looks like a warning icon, not a control; nothing says tapping it opens the source/catalog comparison. The removal dialog’s full path never appears on touch, so a first-timer cannot verify a same-named folder before deleting.

**Maya (single-operator home-server user):** She reviews folders by name, often duplicates from different imports. The removal dialog shows only the leaf name, and the “Matched as” line can be identical for duplicates; the one distinguishing detail is hover-only. The reassurance that files stay on disk does not resolve which record is selected.

## Minor Observations

- Every batch row repeats “Source” and “Proposed match”; the repeated labels make each row self-contained but add vertical noise across seven rows. A single column header would be quieter on desktop, though it would weaken each row’s standalone reading.
- Green agreement checks on title, author, release date, publisher, and language create a tick texture. The amber conflict still wins in the tested fixture, but the density reduces the signal of any single check.
- The removal list cuts the last visible row at its 20rem scroll edge with no fade or edge cue. The selected count makes the list length clear, but a subtle bottom fade would signal more rows.
- The focused inspector’s “Source cover” and “Catalog cover” captions duplicate the region headers; they are small, but they add two labels where the composition already says which is which.
- The phone head is intentionally non-sticky, so Remove/Save scroll away during a long batch; committing requires a return to the top. This is a reasonable tradeoff, but it should be a conscious one.
- The focused index truncates long folder names with an ellipsis; the full identity is only in the inspector’s Source details, which requires selecting the row first.

## Questions to Consider

- When two folders share a leaf name, should the removal dialog show their parent directories by default rather than relying on hover?
- Should choosing a source in mobile focused review take the operator straight to the inspector, with the index still available as a compact switcher?
- Could the phone head show only the counted Save and a compact overflow, letting the comparison start in the first viewport?
- Do all agreed fields need a green check, or would conflict marks plus one “source-backed” summary scan faster while keeping the same honesty?
