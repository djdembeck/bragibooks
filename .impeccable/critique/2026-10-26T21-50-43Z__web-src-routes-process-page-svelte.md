---
target: all design changes in this branch vs develop (per-job elapsed duration)
total_score: 28
max_score: 40
na_heuristics: ""
p0_count: 0
p1_count: 1
target_identity: "file:/home/djdembeck/orca/workspaces/bragibooks/feat-frontend-show-elapsed-duration-per-job-in-j/web/src/routes/process/+page.svelte"
target_fingerprint: "sha256:f9bdcab8f76e022c967169610a766af9d41f983fd2d93f302b9cfcddcd057308"
target_path: /home/djdembeck/orca/workspaces/bragibooks/feat-frontend-show-elapsed-duration-per-job-in-j/web/src/routes/process/+page.svelte
timestamp: 2026-10-26T21-50-43Z
slug: web-src-routes-process-page-svelte
closed: true
---
# Design critique: per-job Elapsed duration

Method: dual-agent (A: task/CritiqueDesignVisual, browser-rendered · B: task/DetectorEvidence)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 4 | Live duration ticks each second; status badge and description remain the primary state cue. |
| 2 | Match System / Real World | 2 | "Elapsed" measures queue wait on queued jobs and processing time on running jobs. |
| 3 | User Control and Freedom | 3 | No new control or trap; Cancel stays available where a job is active. |
| 4 | Consistency and Standards | 2 | Live and terminal durations use different precision; queued cards show Created and Elapsed for the same span. |
| 5 | Error Prevention | 3 | The state badge corrects the reading, but the duration label alone can be misread. |
| 6 | Recognition Rather Than Recall | 3 | Labels are visible; the queue-vs-processing distinction is not spelled out. |
| 7 | Flexibility and Efficiency | 3 | Live duration is at-a-glance; no comparison or sort, which this micro-feature does not need. |
| 8 | Aesthetic and Minimalist Design | 3 | Quiet incumbent grammar; the live value sits at the card's lowest type tier. |
| 9 | Error Recovery | 2 | An unparseable endpoint renders a blank dd under a live label. |
| 10 | Help and Documentation | 3 | Status description explains the state; no explicit queue-wait definition. |
| **Total** | | **28/40** | **Good** |

## Design Specificity Verdict

**LLM assessment:** The row is unmistakably Record workspace: same `dl` anatomy as Started/Done/Created, same 10.4px uppercase dt and 12px tabular dd, no new component or token. It is the quietest possible extension of the incumbent system. The product-specific weakness is semantic, not visual: the same "Elapsed" label means queue wait before a job starts and processing runtime after.

**Deterministic scan:** Source detector clean. URL scans of `/process` at 1440x900 and 390x844 report four `undersized-ui-text` warnings on the 10.4px timestamp labels (two new "Elapsed" labels, two pre-existing Started/Created). `/books/1` is clean; its only finding is a pre-existing advisory `straggler` on the byline.

**Visual overlays:** Not injected. URL scans provided rendered-page detector evidence; no user-visible overlay is claimed.

## Overall Impression

Structurally modest and coherent, with a genuinely useful live signal. The queued-job label is the one place the change undercuts the product's honesty principle: a job waiting four hours behind a batch reads as a four-hour process. The finish-line precision drop is smaller but visible.

## What's Working

- **Incumbent grammar.** The value joins the existing timestamp definition list rather than inventing a metric card or a new visual vocabulary.
- **Live without noise.** The one-second tick runs only while a job is active, uses tabular numerals, and is not in an aria-live region, so it informs sighted users without flooding screen readers.
- **Honest state vocabulary.** The status badge and job description remain, so the duration never carries state alone.

## Priority Issues

### 1. [P1] Queue wait is labelled as processing time

A queued job has no `started_at`, so the row measures `created_at` to now and prints "Elapsed". Verified: `QUEUED / ELAPSED 4m 46s / CREATED Oct 26, 2026, 4:38:21 PM`. A job remaining queued for hours reads like a hung process. A job cancelled before starting reads `CANCELLED / DONE … / ELAPSED 2m`, reporting queue wait as its lifetime. The fix is one label word: "Queued" while there is no start timestamp, "Elapsed" once processing has started.

### 2. [P2] Live duration loses precision at the finish line

Live jobs keep seconds (`12m 27s`); terminal jobs drop them above a minute (`12m`). At the moment the operator confirms completion, the number appears to lose time. The live boundary also produces `1h 0s` while terminal produces `1h 0m`, so the units change at the same edge. Keep `m s` for terminal durations under an hour and always include the minute component once hours appear.

### 3. [P3] Unparseable timestamps leave a blank value

`formatElapsed` returns an empty string when either endpoint cannot be parsed, while the label still renders. The product rule is that absent values are labelled plainly; `formatJobTime` already uses an em dash. Return `—`.

### 4. [P3] Timestamp labels fall below the readable floor on Queue

The Queue's dt labels are 10.4px, below the 11px functional-text floor; the new rows inherit the pre-existing scale. Book-record labels are 11.2px. Promote the Queue's timestamp labels to 11px so both routes sit on one dense-row tier.

## Persona Red Flags

**Alex (power user):** Gains a hang detector immediately: a climbing Elapsed with a frozen log is the fastest signal that m4b-merge stalled. He will also be the first to mis-triage a long queued job as a long-running one.

**Jordan (first-timer):** "Elapsed" is readable, but the queued card presents "Waiting for a processing slot", "QUEUED", "ELAPSED", and "CREATED" in one small block. The label, not the data, is the confusion.

**Self-hosting operator:** The strongest persona win. The queued-label cost lands hardest here, where a batch can sit behind a four-hour merge and the operator is deciding whether to intervene.

## Minor Observations

- A live queued card shows both Created (absolute) and Elapsed (relative) for the same span. Tolerable as absolute vs relative, but the queued-label fix removes the semantic ambiguity.
- Durations beyond 24 hours stay in hours (`26h 0m`), consistent with `formatRuntime` and the product's other duration vocabulary. Accepted intentional exception.
- Duration units are hard-coded English h/m/s beside locale-aware absolute timestamps. The product is single-user and English-only today.
- The changing value is not a `<time>` duration; it matches the incumbent absolute timestamps, which also do not use `<time>`.
- The doc comment's "#442" claim is stale: the base fixes #442 in #452, so absolute clock rendering is already correct. The delta claim should stand on its own.

## Questions to Consider

- Should "Elapsed" ever mean total time since enqueue, or is it exclusively processing time?
- Does a terminal duration need seconds at every scale, or only where the live value had them?
- Should the active card elevate the duration above the absolute timestamps, or is the tick itself enough?

Questions skipped: the request fixed the scope and order (critique → audit → polish all branch changes).
