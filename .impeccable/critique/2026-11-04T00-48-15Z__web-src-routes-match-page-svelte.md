---
target: web/src/routes/match/+page.svelte
total_score: 31
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 0
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/suggest-to-add-to-audiobookdb/web/src/routes/match/+page.svelte"
target_fingerprint: "sha256:c15e2aa37fbb8dab8642c68be7d724cf86b88fde71ca8e1ded735095cb4ff1ea"
target_path: /home/djdembeck/orca/workspaces/bragibooks/suggest-to-add-to-audiobookdb/web/src/routes/match/+page.svelte
timestamp: 2026-11-04T00-48-15Z
slug: web-src-routes-match-page-svelte
closed: true
---
# Critique report — Match missing-release handoff

Method: dual-agent (A: reviewer-design · B: task). A reviewed the committed 1440/390 captures and source; B exercised the live disposable app in a fresh managed tab. Detector and browser evidence stayed out of A’s context until A finished.

## Design Health Score

| # | Heuristic | Score | Key issue |
|---|---|---:|---|
| 1 | Visibility of System Status | 3 | The block shows the ASIN state; compact placements don’t show whether an ASIN will be prefilled. |
| 2 | Match System / Real World | 2 | “Not the right book?” followed by “Add this edition” can imply an edition of the selected wrong record. |
| 3 | User Control and Freedom | 4 | Search, review, and the existing selection remain available; the handoff changes no Bragi state. |
| 4 | Consistency and Standards | 3 | Supporting information varies between the block and the inline contexts. |
| 5 | Error Prevention | 3 | Prefill is carefully gated, but compact placements hide the identifier being carried. |
| 6 | Recognition Rather Than Recall | 3 | The block shows ASIN and provenance; batch and footer require recall or opening the external page. |
| 7 | Flexibility and Efficiency | 3 | Available at useful recovery points, though still one external action per source. |
| 8 | Aesthetic and Minimalist Design | 3 | Quiet treatment fits; focused zero-results can show the block and search repeat together. |
| 9 | Error Recovery | 4 | Avoids equating no match with absence and gives a concrete next step. |
| 10 | Help and Documentation | 3 | The block explains new tab, sign-in, and review; the wrong-match footer does not. |
| **Total** | | **31/40** | **Good** |

## Design Specificity Verdict

Strongly product-specific, with a copy/disclosure seam. The handoff is grounded in Bragi’s source-versus-record model and its ASIN evidence, not a generic missing-data card. The block is the strongest expression: cautious claim, actual ASIN, provenance, and a complete next-step explanation. The wrong-match footer is the weak seam — it changes from “book” to “edition” and drops the context the block supplies.

Deterministic scan: `impeccable detect --json` on the two Svelte surface files returned 0 findings. The detector scope excluded `missingRelease.ts`; the helper is covered by 16 unit tests. Browser evidence: 0 console errors, 0 horizontal overflow at 1440×900 and 390×844; link contrast 7.08:1; all links target=_blank / rel=noopener noreferrer with descriptive accessible names; focus-visible outlines verified by real Tab presses; target sizes match the committed tiers (block 44px; search/footer 32px desktop / 44px phone; batch 24px desktop / 32px phone).

Visual overlays: no injected overlay exists. B found no live-server/overlay verb for the already-running app and used a fresh-tab browser fallback; no overlay was claimed.

## Overall Impression

A restrained, useful recovery path on a dense operations surface. The block turns a dead end into a credible handoff without asserting absence. The biggest opportunity is consistency: make the wrong-match footer describe the source audiobook, and decide deliberately whether compact placements should expose the carried ASIN or leave that evidence in the block.

## What’s Working

- Honest uncertainty: “No record matched this source” and “If it isn’t in the catalog” avoid turning a failed search into a catalog claim.
- Hierarchy suits the task: the block is a distinct recovery step; the batch action stays compact beside the 44px Review action.
- The block explains consequences: new tab, sign-in, possible review, and searching again make it a workflow rather than a dead-end link.

## Priority Issues

1. [P2] Make the wrong-match action describe the source, not an edition. “Not the right book?” is followed by “Add this edition to AudiobookDB” (`web/src/lib/missingRelease.ts`; focused editor). “This edition” can read as an edition of the selected wrong record. Fix: use the block’s source-level wording, “Add this audiobook to AudiobookDB.”
2. [P2] Decide whether compact placements must show the prefilled ASIN. The block shows ASIN and provenance; batch and footer links show only the action, while their accessible names include the ASIN. Keep the block as the single evidence surface (compact links stay small) or add a compact `B0…` cue beside inline actions; do not add full provenance to batch.
3. [P3] Set expectations on the standalone wrong-match handoff. The footer lacks the block’s new-tab/sign-in/review note. Keep the compact icon-only treatment or add one short visible line; avoid repeating the full block helper.

## Persona Red Flags

- Alex, power user: the batch link is easy to reach but doesn’t expose whether it will carry an ASIN; Alex must open Source details or the external page to verify.
- Jordan, first-timer: “Not the right book?” → “Add this edition” is an avoidable interpretation step. The block explains the flow; the footer doesn’t.
- Sam, keyboard/low-vision user: links are in visual tab order with descriptive names and visible focus. The batch target is 24px desktop / 32px phone — compliant with WCAG 2.2 AA’s 24px minimum, but less forgiving than the app’s 44px controls.

## Minor Observations

- Focused zero-results can show the no-match block and the short search repeat together; understandable reinforcement, slightly redundant.
- “ASIN” is appropriate operator language; a first-time user may not know the identifier.
- The compact target sizes are a deliberate product decision and match the committed scale; do not “fix” them back to 44px without revisiting the density tradeoff.

## Questions to Consider

- Should the wrong-match footer describe adding the source audiobook or an edition?
- Should every handoff show the validated ASIN and provenance, or should compact contexts keep that in the block and accessible name?
- Should the wrong-match footer repeat the new-tab/sign-in note, or is the external-link icon enough?
