---
target: job action type UI (branch vs develop)
total_score: 25
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 0
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/feat-backend-record-job-action-type-merge-conver/web/src/routes/process/+page.svelte"
target_fingerprint: "sha256:35acd3abe84cadbd4cb7b55ea1a728452c71ab87a840c7fe0e55dd99ee0b738a"
target_path: /home/djdembeck/orca/workspaces/bragibooks/feat-backend-record-job-action-type-merge-conver/web/src/routes/process/+page.svelte
timestamp: 2026-10-26T21-52-52Z
slug: web-src-routes-process-page-svelte
closed: true
---
Method: dual-agent (A: CritiqueDesign · B: CritiqueDetector)

# Critique — job action type, branch vs `develop`

Scope: the branch's design changes on the Queue job cards (`web/src/routes/process/+page.svelte`) and the book record's Job history (`web/src/routes/books/[id]/+page.svelte`): the new action tag, the done-label change, and the empty-state copy.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|---|---:|---|
| 1 | Visibility of System Status | 3 | Status, action, and timestamps are all visible; no active-job card existed in the seed. |
| 2 | Match System / Real World | 3 | “Import”/“Metadata update” are direct; “M4B” assumes domain familiarity. |
| 3 | User Control and Freedom | 3 | Native disclosures and book links; active-job controls not rendered. |
| 4 | Consistency and Standards | 2 | Action tag is route-local CSS, 10.4px vs the documented 11px badge token, and duplicates itself in two files. |
| 5 | Error Prevention | 3 | History-only surface; no editing decision to get wrong. |
| 6 | Recognition Rather Than Recall | 3 | Operation visible, but not explicitly identified as an operation in the accessibility tree. |
| 7 | Flexibility and Efficiency | 2 | Eight history records, no filter/search (pre-existing). |
| 8 | Aesthetic and Minimalist Design | 2 | A second dark-outlined chip beside status; the two read as peers. |
| 9 | Error Recovery | 2 | Failure reason is specific, but the history row offers no recovery step (pre-existing). |
| 10 | Help and Documentation | 2 | Queue hint exists; no failure-specific guidance (pre-existing). |
| **Total** | | **25/40** | **Acceptable** |

## Design Specificity Verdict

**LLM assessment:** Content-specific, visually category-interchangeable. “M4B merge / Metadata update / Import” names Bragi's real workflow, and the dense rows fit the Record workspace. But the action renders as a second outlined chip, a pattern that could be lifted unchanged into any admin dashboard. On the record page the operation's `1.5px #92928a` outline is visually heavier than the status chip's tinted border, so the secondary category outshouts the state.

**Deterministic scan:** Static CLI: 0 findings on both changed markup files (exit 0), with a `develop` baseline also clean. Runtime overlay: 12 new `undersized-ui-text` findings from the single `font-size: 0.65rem` (10.4px) declaration — 8 on `/process`, 4 on `/books/1`. The other 16 overlay hits are pre-existing `.job-timestamp dt` rules, not this branch.

**Visual overlays:** Injection succeeded in a `[Human]` browser tab (24 overlay nodes on `/process`, 4 on `/books/1`). Live server was stopped, no HTML was modified, repo stayed clean.

## Overall Impression

The branch adds the right information — an operator can now tell a metadata update from a merge — but gives it the wrong rank. Two bordered chips of nearly equal weight turn a status list into tag soup, and the Queue's three-child flex heading splits status from action depending on title length. The single biggest opportunity: make operation a quiet, self-identifying descriptor and let status stay the only chip.

## What's Working

- Status honesty is preserved: `StatusBadge` keeps glyph + word + color with an `aria-label="Status: …"`.
- Generic completion now works: “Completed” is correct across merge, metadata-update, and import, with the operation supplying specificity.
- No mobile overflow: at 390×844 both pages stay within 390px; no tag clips internally.

## Priority Issues

### [P2] Operation competes with status

Both tags are bordered pills; the action's dark `--border-strong` boundary and 10.4px uppercase text make it read as state or control. On `/books/1`, `DONE` measures 73.4×22.5px and `METADATA UPDATE` 137.5×21.6px, 12px apart. The 10.4px size is below DESIGN.md's 11px badge token and the detector's floor.

**Fix:** one shared operation element, sentence case at 0.75rem with `--text-muted`/`--text-secondary` and no fill or border; status remains the only chip. **Command:** `$impeccable polish`.

### [P2] Queue heading splits action from status

`job-record__heading` lays identity, status, and action out as three separate flex children. In the 336px sidebar, shorter titles keep `DONE + IMPORT` on one row while longer titles push the action to a second row with status still right-aligned. The relationship varies by title length.

**Fix:** group status + action into one flex unit that wraps as a whole. **Command:** `$impeccable polish` / `$impeccable layout`.

### [P2] Unknown actions lie

`jobActionLabel()` maps any unrecognized value to "M4B merge"; a live `chapter_split` row rendered as `M4B MERGE`. The Queue loader also coalesces missing actions to merge.

**Fix:** preserve the explicit legacy-null default only; render an honest "Unknown action" for any other unmapped value. **Command:** `$impeccable clarify`.

### [P2] Action has no accessible identity

Status exposes `role`+`aria-label`; the action is bare `StaticText`, so the AX announcement fuses into `"done M4B merge"` with no boundary.

**Fix:** give the shared element `aria-label="Action: <label>"` (or an equivalent visually-hidden prefix). **Command:** `$impeccable audit` / `$impeccable polish`.

### [P3] Empty copy and section name disagree

The branch changes the record empty state to “No recorded activity yet.” while the section remains “Job history” and the Queue says “Recent job history.” Import and metadata-update rows are audit rows, not processing jobs; the new copy is the truthful one, the heading is now stale.

**Fix:** rename the record section to “Recorded activity” (and the Queue section to “Recent activity”) or revert the copy; pick one model. **Command:** `$impeccable clarify`.

## Persona Red Flags

- **Sam (accessibility):** hears `"The Left Hand of Darkness a1b2c3d4 done M4B merge"` — status and operation have no spoken boundary.
- **Alex (power user):** eight history cards, no filter; all seeded 8-char IDs share `a1b2c3d4` (pre-existing).
- **Casey (mobile):** no clipping, but rows become ragged as action chips drop below status on some cards and not others.

## Minor Observations

- `M4B MERGE` uppercase is correct for the badge convention but adds to the peer-chip reading.
- Longest mapped label (`Metadata update`) fits at 390px without wrapping.
- `ProcessingJob.action` is non-optional `JobAction`; the Queue types it `string` and coalesces `null`. Inconsistent contracts.
- Full UUID is shown on records, 8-char prefix on Queue (pre-existing).

## Questions Considered

- Would a quieter, sentence-case operation descriptor make the status chip easier to scan than a second badge?
- Should a missing legacy action remain a merge while an unrecognized non-null action is shown as unknown?
- Is the expanded Queue history an audit trail or a failure-finding tool that needs filtering?
