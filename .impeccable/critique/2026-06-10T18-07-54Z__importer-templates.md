---
target: importer/templates/
total_score: 25
p0_count: 1
p1_count: 1
timestamp: 2026-06-10T18-07-54Z
slug: importer-templates
---
## Bragibooks Importer Templates: Design Critique

### Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Progress bar exists for directory streaming but no feedback during import submission or match processing |
| 2 | Match System / Real World | 4 | Audiobook terminology (ASIN, narrator, series) used correctly; directory tree mirrors filesystem |
| 3 | User Control and Freedom | 2 | No undo after import; remove icon has no tooltip; no back path from Match page |
| 4 | Consistency and Standards | 3 | Bulma conventions followed but action placement inconsistent across pages; modal button order varies |
| 5 | Error Prevention | 2 | Submit disabled for invalid ASINs is good; no confirmation before bulk import; no duplicate detection |
| 6 | Recognition Rather Than Recall | 3 | Cover images shown during matching; search has placeholder but no example format |
| 7 | Flexibility and Efficiency | 2 | Fuzzy search is strong; no keyboard shortcuts; no bulk ASIN entry; no import history |
| 8 | Aesthetic and Minimalist Design | 3 | Flat, no shadows; brand gold+white is clean. book_list metadata list is verbose (7+ rows per book) |
| 9 | Error Recovery | 2 | Error tab exists but shows opaque messages with no retry or fix path; directory error has retry button |
| 10 | Help and Documentation | 1 | No help links, no tooltips, no onboarding; settings blockquote shows keywords but no examples |
| **Total** | | **25/40** | **Functional but rough (62.5%)** |

### Anti-Patterns Verdict

**LLM assessment**: No AI-generated slop detected. The interface is straightforward Bulma product UI with no gradient text, glassmorphism, hero-metric templates, tracked eyebrows, numbered section markers, identical card grids, or decorative motion. The design vocabulary is standard web component (panels, cards, modals, tabs) used for its intended purpose. This reads as a developer-built tool, not an AI scaffold.

**Deterministic scan**: 1 finding across 7 HTML templates + 3 JS files.
- **layout-transition** (warning): `transition: width 0.2s ease-out` on `.progress-fill` in `base.html:151`. Animating `width` triggers layout recalculation. Acceptable in context (single-pass progress bar on a loading overlay, 0.2s duration) but technically a layout property animation.

No browser overlay injection was possible (dev server unavailable due to missing binary dependency, no X server for headless browser).

### Overall Impression

Bragibooks has a clear, honest visual identity that aligns with its brand (calm, capable, warm). The gold+white flat system is distinctive without being loud. But the interface is built for the *happy path* only. The moment something goes wrong, or the user pauses to think "did I do this right?", the tool has no answers. The Error tab is a graveyard, the Match page hides why submit is disabled, and no step in the workflow says "here's what will happen next." A tool that feels calm when it works and scary when it doesn't isn't calm; it's fragile.

The single biggest opportunity: **make every high-stakes moment reversible or at least transparent**. A summary before import, a confirmation before ASIN submission, and a retry action on errors would transform the emotional journey from anxiety-driven to confidence-driven.

### What's Working

1. **Flat, warm visual system.** No shadows, no gradients, no decorative fluff. Brand Gold (#ffe08a) as body background creates a warm canvas. White content areas sit "above" the gold naturally through tonal contrast, exactly matching the designed "organized shelf" metaphor.

2. **Streaming progress on the import page.** `fetchAndRenderDirectories()` shows real-time directory scanning with file counts and current file name. The indeterminate-to-determinate progress bar transition is a strong pattern for self-hosted tools where scans can take 30+ seconds.

3. **Fuzzy search in the directory tree.** `fuzzyMatch()` allows partial matching (searching "dune" matches "Frank Herbert/Dune"). A genuinely useful power-user feature that serves the tool's primary audience.

### Priority Issues

**[P0] Error tab is a dead end**
- **Why it matters**: Users see failed imports with no path to recovery. Error message is opaque. No retry, no logs, no link to fix. Users abandon the tool or re-import everything blindly.
- **Fix**: Add a Retry button per errored book that re-queues it. Show which step failed. Link to logs. Even a simple "Re-try this book" button changes the Error tab from graveyard to triage.
- **Suggested command**: $impeccable harden

**[P1] No import confirmation before processing**
- **Why it matters**: User selects 50 directories, clicks Next, and import starts immediately. No summary, no estimated count, no undo. High anxiety at the moment of commitment.
- **Fix**: Show a confirmation step: "You've selected X directories. Import will begin." with Start/Go back buttons.
- **Suggested command**: $impeccable clarify

**[P2] Submit button disabled on Match page with no explanation**
- **Why it matters**: `checkAllSelectsHaveValue()` disables submit if any ASIN select doesn't have a 10-char value. Button sits grayed out saying nothing. User has no idea which row is incomplete.
- **Fix**: Add aria-describedby pointing to help message. Show red border on unfilled selects. Display "Match all books to continue."
- **Suggested command**: $impeccable clarify

**[P2] Match page auto-search fails silently**
- **Why it matters**: If `fetchOptions()` returns no results, the select dropdown shows the raw path as the only option. No "No results found" message, no nudge to Custom Search.
- **Fix**: Show inline message: "No automatic match found. Try Custom Search." Auto-highlight unmatched rows.
- **Suggested command**: $impeccable clarify

**[P3] Inconsistent modal button ordering**
- **Why it matters**: Search modal: Search then Cancel (left-to-right). Remove modal: Yes then No (left-to-right). Standard convention puts primary on the right for confirmations.
- **Fix**: Standardize: Cancel/discard on the left, confirm/primary on the right.
- **Suggested command**: $impeccable polish

### Persona Red Flags

**Alex (Power User)**: Has 500+ audiobooks, imports weekly, wants speed.
- No bulk edit on Match page: must open each Custom Search modal individually
- No keyboard shortcuts: Enter to submit, Esc to close modal, arrow keys for tabs
- Processing tab is opaque: shows count but not which books are stuck
- No import history

**Jordan (First-Timer)**: Has 30 audiobooks, anxious about doing it wrong.
- Directory tree is intimidating: does "select the folder" mean the parent or the audio files inside?
- ASIN is unexplained: no "What's this?" link
- No success feedback after matching: page redirects silently
- Error tab is scary: red text, no guidance, no retry

### Minor Observations

- book_list.html L68: Two-column layout (is-three-quarter) overflows on mobile, no responsive breakpoint
- match.html L59: Remove icon (fa-times) has no aria-label
- base.html L274: Hero header gold+black logo contrast ~2.5:1 — worth verifying
- setting.html L19-30: Blockquote for output keywords reads like a cited quote, not help text
- match.js L114: Option text concatenates title+author+narrator — unreadable for long titles
- base.html L411: Footer repeats version info already in pre-loader
- directory_contents.html L9: Indentation via 5x &nbsp; per depth level is fragile
- XSS vector: book_list.html:75 renders book.long_desc|safe without sanitization

### Questions to Consider

- Why does the Match page exist as a separate step? Could import and match merge into a single flow?
- What happens when two directories contain the same book? Is duplicate detection missing or intentionally avoided?
- Why is Processing a black box? The backend could expose granular status.
- Is the Error tab a graveyard or a triage queue? If triage, where are the tools?
