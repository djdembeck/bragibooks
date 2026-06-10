# Product

## Register

product

## Users

Self-hosted audiobook collectors running Bragibooks on a NAS or home server. Typically processing a few books at a time, occasionally a small batch. They value reliability over flash, and expect the same calm competence they get from their other self-hosted tools. Sessions are short and task-driven: select folders, match metadata, confirm, wait.

## Product Purpose

Bragibooks is the web frontend for m4b-merge: it lets audiobook collectors clean up, tag, and organize their library by matching files against Audible metadata. Success is a frictionless path from "messy folder of audio files" to "properly tagged, organized audiobook" with minimal manual intervention. The interface should feel like a well-organized shelf: everything in its place, easy to find, nothing screaming for attention.

## Brand Personality

Calm, capable, warm. The Norse-mythology namesake (Bragi, god of poetry) carries through as understated craft, not theatrical spectacle. The tool does its job quietly and well. Warmth comes from competence and care, not decorative friendliness.

## Anti-references

- No generic SaaS dashboards: card grids, hero metrics, gradient accents, dark nav with neon highlights.
- No playful or whimsical aesthetics: cute illustrations, bouncing animations, toy-like UI.
- No cold enterprise tools: sterile dense data tables, admin-panel sterility, wall-of-text forms.
- The product lives in the self-hosted tool space, not in a marketing funnel or a corporate dashboard.

## Design Principles

1. **Quiet competence.** The interface works so smoothly you barely notice it. No celebratory animations for completing a basic task; no decorative elements that compete with the work.
2. **Show the work, not the scaffolding.** Audiobook covers, titles, and processing status are the content. UI chrome stays out of the way. When a book is processing, show progress; when it's done, show the result.
3. **Warm minimalism.** Warmth through color temperature and tone, not through excess decoration. A well-chosen palette and comfortable typography do more than icons and illustrations.
4. **Task-linear, not feature-dense.** The workflow is import, match, confirm, wait. The UI should reflect that single thread. Settings are separate because they're a different mode, not because they need equal prominence.
5. **Accessible by default.** Self-hosted tools often get accessibility shortcuts. Bragibooks won't. Clean contrast, keyboard navigation, screen-reader-friendly status updates.

## Accessibility & Inclusion

WCAG 2.1 AA compliance. Contrast ratios ≥4.5:1 for body text, ≥3:1 for large text. Keyboard-navigable forms and modals. Screen-reader-friendly processing status (already uses `aria-live` on the loader). Reduced-motion support for spinner and progress animations.
