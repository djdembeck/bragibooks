---
target: Import and Match output overrides
total_score: 28
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 0
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/feat-import-per-group-output-directory-and-path/web/src/routes/import/+page.svelte"
target_fingerprint: "sha256:68d4ee871087d37a0e8ca9fc56c5fa3403f6e786247aed1a2420d0050793712d"
target_path: /home/djdembeck/orca/workspaces/bragibooks/feat-import-per-group-output-directory-and-path/web/src/routes/import/+page.svelte
timestamp: 2026-10-26T22-23-48Z
slug: web-src-routes-import-page-svelte
---
## Critique — Import and Match output overrides

**Score: 28/40 — Good (70%).** The new flow fits Bragi Books’ neutral “Record workspace” and keeps the batch task legible. The main gap: per-book output exceptions and their save status are less visible than match status.

### Heuristic scores

| # | Heuristic | Score | Finding |
|---:|---|---:|---|
| 1 | Visibility of system status | 3 | Initial skeletons render immediately, bypassing the shared 200 ms delay. |
| 2 | Match to real world | 3 | “Path format” is technical, though the example helps. |
| 3 | User control and freedom | 3 | Import offers Browse and Clear; per-book edits have no visible draft/discard state. |
| 4 | Consistency and standards | 2 | Match override fields are 32 px high versus the documented 44 px field/button minimum. |
| 5 | Error prevention | 3 | Invalid overrides show inline errors and block submission. |
| 6 | Recognition rather than recall | 3 | Batch review marks an override but does not show its destination. |
| 7 | Flexibility and efficiency | 3 | Import is efficient in batches; per-book exceptions require Focused review. |
| 8 | Aesthetic and minimalist design | 3 | Calm, clear grouping; no dominant visual clutter. |
| 9 | Error recovery | 3 | Inline validation and retry/error UI exist; unsaved override edits are not explicit. |
| 10 | Help and documentation | 2 | The format field gives little guidance compared with Settings. |

### Design direction and task load

The changes suit a self-hosted audiobook pipeline: filesystem intake, batch matching, neutral surfaces, and restrained gold actions. The optional import override is appropriately progressive.

**Cognitive load: low, one checklist failure.** Batch review says `per-book` without showing the destination, so operators must open Focused review to identify the exception. Other groupings and decision points stay manageable.

The import screen quickly establishes source and selection state. Match clearly communicates unresolved records and the counted save action. Confidence drops around output decisions: batch review hides the differing destination, and Focused review does not show whether an edit will be saved.

### What works

- Import explains that selected sources move to Match.
- The optional output controls stay collapsed until needed; Browse, Clear, and inline validation are present.
- Neutral surfaces, row boundaries, and gold primary actions match the committed design.

### Priority issues

1. **[P2] Show the effective per-book destination in Batch review — `web/src/routes/match/+page.svelte`.**  
   The batch row shows a `per-book` chip but not the overridden directory or format. Operators must leave the batch scan to find the exception. Add a compact directory/format summary there and keep the full path in Focused review. The new chip is 10.5 px; `DESIGN.md` specifies 11 px badges.

2. **[P2] Make per-book edits’ save state explicit — Match Focused review.**  
   The override inputs bind to candidate state, but the batch save only commits included, ready matches. Unmatched or excluded candidates can be edited without a visible indication that those edits are still pending. Clarify the existing commit condition and show pending/eligibility state; do not silently change the save contract.

3. **[P2] Align output controls with the shared control pattern — Import and Match.**  
   The Import override expander measures 20 px high. The new Match inputs are 32 px high, use 12 px text, and override focus styling with a 2 px graphite outline; the design specifies 44 px controls and a 3 px dark-gold focus outline. Match’s per-book directory input also lacks the Browse action available in Import.

4. **[P2] Preserve the delayed-skeleton behavior on first load — `DirPicker`, Overview, Books, and Import.**  
   The new `!dl.hasLoaded` / initial `loading = true` branches show skeletons immediately, bypassing the shared 200 ms delay and risking a placeholder flash on fast responses. Keep the initial empty state suppressed while gating the skeleton on the delay.

### Detector evidence

The CLI detector returned no findings across the five changed UI files. The browser overlay found three import labels at both desktop and mobile sizes: the source-name warning is a false positive (`#292923` text); the date and size for unsupported `old-notes.txt` are genuinely dimmed by 50% opacity on already-muted text. Those two low-contrast labels predate this branch, so I have not folded them into its polish scope. On Match, the overlay flagged the new 10.5 px `per-book` chip as undersized functional text.

### Persona red flags

- **Alex, power user:** Must open Focused review to see which batch destination differs.
- **Sam, accessibility-dependent user:** Match override inputs fall below the documented 44 px target; labels and error associations are present.
- **Casey, distracted mobile user:** The import expander is compact, and per-book directory editing requires manual path entry.

### Minor observations

- The format example is `author/title`; Settings provides a more guided `PathFormatBuilder`.
- On mobile, the import expander sits below the main action and is easy to overlook.

### Questions to consider

- Should Batch review show a compact directory/format summary, or intentionally keep the `per-book` marker as a prompt to open Focused review?
- Should override edits remain committed only with included, ready matches, with pending state shown, or should per-source overrides have an independent save path?
- Should the per-book directory field reuse the existing directory picker?
- Should initial skeletons appear immediately, or only after the shared delay while the empty state remains suppressed?
