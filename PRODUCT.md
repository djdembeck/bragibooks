<!-- impeccable:product-schema 1 -->

# Product

## Users

Solo self-hosting audiobook collectors and organizers who run Bragi Books on a home server (typically in Docker) to batch-clean, tag, match metadata, and repackage m4b/m4a files using `m4b-merge`. They manage files by folder, care about accurate metadata, and want to process imports in batch without babysitting the application.

## Positioning

The operations board for a personal audiobook pipeline. Bragi Books is not a library catalog, a SaaS dashboard, or a media player. It is a calm work surface for batch processing: select, match, queue, finish. Built for the person who runs the server, owns the books, and needs to see state.

## Brand Commitments

Capable, honest, uncluttered.

Voice is direct and specific. No buzzwords, no theatrical promises. The tool explains what it is doing, surfaces problems plainly, and gets out of the way.

## Operating Context

- **Platform:** Web application (SvelteKit / Svelte 5), accessed through a browser.
- **Hosting:** Self-hosted, commonly deployed via Docker on a home server.
- **Source material:** Local filesystem folders containing audiobook files (m4b, m4a, mp3).
- **Metadata provider:** AudiobookDB public API for title/author/narrator matching.
- **Processing engine:** `m4b-merge` for combining, tagging, and repackaging files.
- **Single user:** Designed for one operator. No multi-tenancy, no team features.

## Capabilities and Constraints

Bragi Books manages an audiobook library through a four-stage workflow:

1. **Intake** — browse source directories and select folders to process.
2. **Match** — confirm or correct AudiobookDB metadata for each source.
3. **Queue** — batch books into processing jobs.
4. **Finish** — track job progress, review completed books, handle errors.

Success means a reliable, low-friction batch workflow where users trust the state they see and can work through a queue without monitoring each step.

**Capabilities:**
- Browse and navigate local source directories, select folders and files for import.
- Match imported books against AudiobookDB metadata with candidate selection and custom search.
- Change, re-search, or remove a match before committing to processing.
- Queue matched books for batch processing via `m4b-merge`.
- Monitor active processing jobs with live output logs (SSE).
- Browse, filter, and paginate the completed book library.
- Configure directories, processing options, and API settings.

**Constraints:**
- Single-user only; no authentication, multi-tenancy, or role system.
- Relies on a local filesystem and `m4b-merge` being available to the backend process.
- Metadata matching depends on AudiobookDB availability and coverage.
- No offline mode; the web frontend requires the backend API.
- Processing is sequential per job; jobs run on the host machine.

## Evidence On Hand

- Current SvelteKit web application with routes for dashboard, import, match, process, books, and settings.
- Backend API endpoints for books CRUD, settings, job SSE streaming, and filesystem directory listing.
- Live components: Nav, Alert, Button, EmptyState, Logo, Modal, PageHeader, Skeleton, StatusBadge.
- State tracking for book statuses: pending, matched, processing, done, error.
- Delayed loading pattern for skeleton-first rendering.

## Product Principles

1. **Respect batch work.** The user selects many items at once; flows optimize for bulk actions, not one-at-a-time clicking.
2. **Show real state.** Every book, job, and folder displays its status honestly. Unknowns are labeled as unknown, not hidden.
3. **Calm density.** Information is packed where it belongs (tables, lists, settings) with enough air to scan comfortably.
4. **Forgiving matches.** Users can change a match, search again, or remove an item before committing to processing.
5. **Consistent controls.** The same button, input, and status vocabulary appears on every screen.

## Accessibility

Target WCAG 2.2 AA. Support keyboard navigation, visible focus indicators, and reduced motion. Do not rely on color alone for status — every state has a text label and/or shape alongside color. Clear labels for all form controls. Reduced motion preference disables non-essential animation and transitions.

## Anti-references

- SaaS-cream card dashboards: warm-tinted neutrals, generic hero cards, and rounded everything.
- Heavy decoration / hero metrics: big decorative numbers, ornamental motion, gradient text.
- The old yellow Bulma admin UI: busy yellow-and-white surfaces, inconsistent spacing, and excess chrome.
- Literal bookshelves, warm cream/serif treatments, decorative illustrations, noisy ornament, hidden state.
