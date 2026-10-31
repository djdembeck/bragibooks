---
target: web/src/routes/process/+page.svelte
total_score: 28
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
p2_count: 2
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/queue-live-output/web/src/routes/process/+page.svelte"
target_fingerprint: "sha256:f9dea8310110954697ac7659491417fe2ad7759dc3c25d4a9867e491db30bbd2"
target_path: /home/djdembeck/orca/workspaces/bragibooks/queue-live-output/web/src/routes/process/+page.svelte
timestamp: 2026-10-28T20-02-08Z
slug: web-src-routes-process-page-svelte
---
# Critique — Queue live-output surface

Target: `web/src/routes/process/+page.svelte` (JobRecord active card + styles), live at `/process`.
Method: dual-agent (A: design review `CritiqueA` · B: detector/browser evidence `CritiqueB`). Assessments ran in isolated contexts; A never saw detector output.

## Design Specificity Verdict

**Authored for Bragi, with one hollow abstraction.** The card is unmistakably this product: the amber running triad (rail, stage line, QUEUED/RUNNING chip), the 8-char job id, mono destination, and the activity label all reuse the incumbent Queue grammar. The strongest decision is the **Destination vs Output profile** distinction: the queue-time snapshot is labeled *Output profile* and never pretends to be the final path; the real Destination appears only after m4b-merge reports it. What weakens it: the stage line is a coarser re-translation of the log it sits beside. While the card says "Merging audio", the log may say `Copying audio chunk 7/30…`, and the rail remains purely indeterminate. That determinate signal is discarded.

Detector: CLI scan of the page returned **0 findings** (exit 0). The live overlay found one verified anti-pattern: the section description wraps with "works." orphaned on its last line (straggler, 15% of measure) at both 1440 and 390. No false positives. Overlay was visible during assessment in the evidence tab; that tab was closed afterward, so no overlay persists.

## Design Health Score

| # | Heuristic | Score | Key issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 4 | Status chip, stage line, rail, ticking elapsed time, pinned log, header status. No ETA. |
| 2 | Match System / Real World | 3 | "Waiting for a processing slot" is plain; raw `.m4b` paths and engine log lines are not explained. |
| 3 | User Control and Freedom | 3 | Per-card cancel, optimistic Cancelling state, card exits to history. No pause/resume or batch cancel. |
| 4 | Consistency and Standards | 3 | Tokens/chip/timestamps follow DESIGN.md; the stage line invents a second dot+text status unit outside StatusBadge. |
| 5 | Error Prevention | 2 | One-tap "Cancel job" with no confirmation. More seriously, a book with a queued job stays in the ready list and can be selected again, producing a backend 409. |
| 6 | Recognition Rather Than Recall | 3 | Full destination inline, full UUID in title, history in the same grammar. The log cannot be copied as a block. |
| 7 | Flexibility and Efficiency | 2 | No keyboard path to per-card actions, no expand-all logs, no density preference. |
| 8 | Aesthetic and Minimalist Design | 2 | Nine anatomy slots in a 336px column; path wraps three lines; log is ~38% of card height; left column empty. |
| 9 | Error Recovery | 3 | SSE→polling fallback, heartbeat whitelist, role=alert errors with ErrorDetail; cancel failure has no in-card retry. |
| 10 | Help and Documentation | 3 | In-situ copy teaches states; m4b-merge is audience vocabulary. Long paths and log orientation need nudge, not docs. |
| **Total** | | **28/40** | **Good** — strong honest status; the envelope fights the content width. |

All ten heuristics apply; max 40.

## Cognitive Load

1 failure, 1 borderline: progressive disclosure fails because every running card force-opens its log and grows vertically as lines arrive. However, processing is sequential, so only one card is normally running and expanded; the batch-growth scenario is bounded. Visual noise floor is borderline: six 12px styles form a dense middle band. No decision point has more than four visible options (the card has exactly one action).

## Emotional Journey

Queued arrival is calm and informative; pickup (rail, chip, stage) is a small positive peak; the long haul is flat — no ETA, no chunk count, stage text repeats for many minutes; completion is a disappearance into collapsed history with no end-of-batch beat. The destination line is the one moment of trust mid-run. The tail is where the operator lives, and it needs one more honest signal.

## What's Working

1. **Destination honesty.** Output profile and real Destination are distinct and appear at the right times.
2. **Stage + reduced-motion fallback.** Stage comes from engine markers; the rail is removed under reduced motion while chip, stage, and log remain.
3. **Pinned live log.** `role="log"`, mono, scans like a terminal, and says "Waiting for the first line…" before output exists.

## Priority Issues

### [P1] Autoscroll yanks the operator back to the bottom while they read earlier output
- **What:** `autoscroll()` sets `scrollTop = scrollHeight` on every output change; there is no at-bottom check. Verified by both assessments: scrollTop was 0, a new SSE line arrived, and scrollTop jumped to the bottom (e.g. 0 → 920).
- **Why:** diagnosis means scrolling up to see discovery/lookup/merge history. The stream makes that impossible.
- **Fix:** only pin when the container is already within ~4px of the bottom; when detached, leave position alone and offer a "Jump to live" affordance.
- **Command:** `$impeccable polish`.

### [P1] Active jobs are newest-first, so a queued card can sit above the running job
- **What:** `jobs` is newest-first and `activeJobs` preserves that order. In the verified run, the queued job appeared above the running job that had the live output.
- **Why:** the live card is the reason to look at this section; burying it under a waiting card inverts priority and makes the "View jobs" jump land on the wrong card.
- **Fix:** sort active jobs running-first, then queued in FIFO order; keep stable order when timestamps are not yet hydrated.
- **Command:** `$impeccable polish`.

### [P1] A book with a queued job remains selectable in "Ready to process"
- **What:** queuing does not change the book's status until the worker starts it, so a waiting book stays in the ready list with an include checkbox. Selecting it and pressing Start produces a backend 409 ("book already has an active processing job"). Verified in the queued-card screenshot and against the API's active-job guard.
- **Why:** the interface offers an action it knows will fail; the operator sees an error after committing a batch.
- **Fix:** exclude books with active jobs from the ready list (and count/select-all); the queued card already represents them.
- **Command:** `$impeccable harden`.

### [P2] The live log is not keyboard-scrollable
- **What:** the scrollable `<pre role="log">` has no `tabindex`; keyboard-only users cannot enter it to read earlier lines, and the autoscroll bug makes that worse.
- **Why:** WCAG 2.1.1 keyboard access; log diagnosis is the surface's core value.
- **Fix:** `tabindex="0"` on the log (the workspace `[tabindex]:focus-visible` rule already supplies the 3px focus ring), plus the at-bottom fix.
- **Command:** `$impeccable audit`.

### [P2] "Processing now" and its description claim more than the cards show
- **What:** the section count includes queued jobs and the description says live output streams here, while queued cards have no stream and say "Waiting for a processing slot." The detector also catches the description's orphaned "works."
- **Why:** the header reads as a status claim that contradicts the cards; a first-timer cannot tell whether waiting work is already running.
- **Fix:** call the section "Active jobs", describe running and waiting jobs accurately, and set `text-wrap: pretty` on the section description to eliminate the straggler.
- **Command:** `$impeccable clarify`.

## Persona Red Flags

**Alex (power user):** the autoscroll yank makes reading a log impossible while output flows; no way to open several logs; destination paths wrap mid-token; the full UUID is only in a title attribute; batch cancel is one card at a time.

**Sam (accessibility-dependent):** the log is not focusable/scrollable by keyboard; cancel's accessible name reads the full UUID; the progressbar is indeterminate and exposes no numeric value, which is acceptable for an indeterminate rail but leaves reduced-motion users with only the 12px stage word.

**Jordan (first-timer):** raw engine lines (`m4b_merge::discovery`) have no frame; "Output profile" is meaningful only after learning Settings; the card presents nine dense facts before a plain sentence about what is happening.

## Minor Observations

- "QUEUED 5s" and the "QUEUED" chip sit near "Created"; they look like two timestamps of one fact.
- The 8-char id sits under the author line and reads as a second author.
- Output-profile path uses a lighter muted tier than Destination; two adjacent greys at 12px.
- Cancel is a ghost Button overridden red via a global selector; the design system has a danger variant, though the quiet treatment may be deliberate for a single-user tool.
- A's recommendation to replace the indeterminate rail with determinate chunk progress is not adopted: DESIGN.md pins an indeterminate 4px rail, and the chunk format is not a guaranteed engine contract. A's recommendation to keep a static rail under reduced motion is also not adopted; DESIGN.md explicitly removes the rail under reduced motion and keeps stage/badge/log.
- The running card's log auto-opens by design; users can close it and it stays closed across output updates (verified).

## Questions to Consider

- Should the batch-level view be a table row set rather than N cards with the same nine slots?
- Can the card surface one honest progress signal beyond the indeterminate rail without inventing engine guarantees?
- If the destination is known, should completion verify that the file exists at that path, turning a promise into a checked fact?
- Does cancel need protection proportional to elapsed work, or is one-tap correct for a single-operator tool?
