---
target: match batch removal dialog + batch row record column (branch vs develop)
total_score: 32
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 0
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/fix-select-to-remove-system/web/src/routes/match/+page.svelte"
target_fingerprint: "sha256:c3fefafa497135b014836ab8a47e6f91bd2be7989cd50c9c6e55a71216da2a75"
target_path: /home/djdembeck/orca/workspaces/bragibooks/fix-select-to-remove-system/web/src/routes/match/+page.svelte
timestamp: 2026-10-28T21-14-59Z
slug: web-src-routes-match-page-svelte
closed: true
---
# Critique — Match batch review & dialog removal

Method: dual-agent (A: `reviewer-design` · B: `task` detector/browser). A assessed the rendered surface blind to detector output; B ran the deterministic scan and browser evidence. The synthesis verified both against source and parent captures, and corrected two B claims.

Target: `web/src/routes/match/+page.svelte`, branch commits `4638687`, `de2dc18`, `932f8b4` — dialog-owned bulk removal plus the rewritten batch record column. Fixture: 8 pending sources, local catalog stub, 1440×900 / 1100×800 / 760×900 / 390×844.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | A ready row excluded from the save still reads “Auto-matched”; the batch no longer reports ready/included totals. |
| 2 | Match System / Real World | 4 | Folder → proposed filename → catalog record with author, narrator, runtime, edition maps to the operator’s actual job. |
| 3 | User Control and Freedom | 4 | Dialog is cancellable, starts empty, and never touches the include-in-save set. |
| 4 | Consistency and Standards | 3 | Batch and Focused review describe the same source state with different words (and the low-confidence chip changes color between them). |
| 5 | Error Prevention | 4 | Destructive set is separate, listed by source, counted, zero-pick disabled, files-on-disk stated. |
| 6 | Recognition Rather Than Recall | 2 | Exclusion must be inferred from a checkbox and a barely-visible row tint; batch totals are gone. |
| 7 | Flexibility and Efficiency | 4 | Batch scanning, select-all, focused exceptions, inline per-row confirmation. |
| 8 | Aesthetic and Minimalist Design | 3 | Calm and on-system; the phone row spends ~275px on one source. |
| 9 | Error Recovery | 4 | Survivors stay selected; plain alert; one-click retry. |
| 10 | Help and Documentation | 2 | Page description and batch footnote were removed; “ready” and “preview, not final path” are unexplained. |
| **Total** | | **32/40** | **Good** |

Cognitive load: 3 failures — the dialog’s >4 visible decisions (inherent to a source list, but unmitigated), per-row exclusion state not stated, and mobile rows too tall to scan. Working memory, safe defaults, progressive disclosure, and error recovery pass.

Emotional journey: the destructive moment is the strongest part — empty selection, named sources, live count, and the files-untouched sentence create real reassurance. The weakest moment is the save decision: after unchecking a ready row, nothing in that row says “not in this save,” and the global count only exists inside the Save label.

## Design Specificity Verdict

**Authored for this product.** Gold operation chrome, Before → Proposed output, separate source art vs catalog art, AbDB provenance, credits/runtime/edition, and “Save N → Queue” are Bragi’s pipeline, not a generic admin table.

**Deterministic scan:** `impeccable detect --json web/src/routes/match/+page.svelte` → exit 0, no findings. Browser overlay: the detector bundle loaded and logged `[impeccable] No anti-patterns found.`, but produced no overlay DOM; no visual overlay is claimed.

**Verified corrections to B:** B reported “one `matchState()` supplies text for all surfaces.” False — the focused inspector hardcodes “Auto-selected · not saved” / “Manually selected · not saved” (line 1975) while batch/index say “Auto-matched” / “Matched manually”; there is no `rowStatus`. B also reported an assertive live region for “Removed 2 records”; the page’s removal notice is `aria-live="polite"` (the per-row confirmation band is the only `role="alert"`).

## What's Working

- **Separating save inclusion from destructive selection.** The row checkbox never authorizes a delete; the dialog owns its own checked set and opens empty.
- **The row now carries the evidence an operator needs.** Cover, title, provenance, match state, credits, runtime, edition — no inspector round-trip for the common decision.
- **Recovery is concrete.** Partial failure closes the dialog, keeps only survivors selected, and focuses a retry that reopens with them checked.

## Priority Issues

1. **[P2] Excluded ready rows don’t say so.** `class:batch__row--excluded` dims to `#4c4c43` on a `#f8f8f6` row (1.03:1 background shift) and leaves the chip reading “Auto-matched.” Focused review says “Excluded from save” for the same source. Fix: on ready, unchecked rows show “Excluded from save” (quiet neutral chip or a muted line beside the include box) and keep the dim; use the same status source in both modes.
2. **[P2] The batch bar lost its totals.** It contains only “Select all ready”; “Save 6 → Queue” counts included-ready records but not batch or ready totals. Fix: add one quiet tabular line: `8 ready · 6 of 8 included`.
3. **[P2] One state, two vocabularies.** Batch/index: “Auto-matched / Matched manually”; inspector: “Auto-selected · not saved”; “No confident match” is amber in batch, neutral in the focused index. Fix: one vocabulary and one color mapping for match state; keep “not saved” only where it adds truth.
4. **[P2] The dialog’s close X is a dead control mid-delete.** Verified: `disabled=false`, tabbable, clickable, guarded only by `beforeClose`; Escape is silently ignored. Fix: disable and visually mute the X while `removingMany`, or hide it.
5. **[P2] Phone rows cost 275–294px each.** Header 168px + one expanded row leaves almost no second source on screen; batch scanning collapses into single-item reading. Fix: cap secondary credits/edition to a two-line preview on ≤760px (full detail stays in Focused review) without hiding source or proposed output.

## Persona Red Flags

Alex (power user): cannot gauge batch progress without counting rows; “Select all ready” gives no ready/total. Jordan (first-timer): title-only head, unexplained “ready,” and a save decision whose exclusion state is invisible. Solo self-hosting operator: the files-untouched reassurance lands; the missing undo/restore for a mistaken record removal is the residual risk.

## Minor Observations

- “Save 6 → Queue” has no noun and exposes “→” in its accessible name; add `aria-label="Save 6 matches to the queue"` or restore the noun.
- Compact buttons disagree: row Review 79×36 vs row Remove 85×44; header Remove… 174×44 vs Save 172×46 on phones.
- Mode buttons are 40px on phones and 36px on desktop; select-all label 21–36px — below the project’s 44px floor (WCAG 2.5.8 passes at 24px).
- Cover art is hand-rolled (44/40/32px, 3px corners, diagonal-gradient placeholder) while `CoverArt.svelte` owns the documented 3rem/6px grammar and spine placeholder.
- The removed footnote was the only place stating the proposed name is a preview and the processor reports the final path.
- Dialog row names duplicate the record title for matched sources; selected rows carry no wash, so selection is only the checkbox.
- Ellipsized author/narrator meta on batch rows has no `title`, so hidden credits are unrecoverable without Review.

## Questions to Consider

- Should a batch chip describe the catalog match or the save inclusion — and which one should an operator trust at a glance?
- Is a “batch review” that shows one source per phone viewport still batch review?
- What is the honest recovery for removing the wrong record, given files remain but the record is gone?
