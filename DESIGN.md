---
name: Bragi Books
description: The Record workspace — a neutral white-and-gold work surface for batch audiobook operations.
colors:
  bg: "#f5f5f4"
  panel: "#ffffff"
  surface: "#f8f8f6"
  surface-hover: "#efefec"
  surface-pressed: "#e7e7e2"
  elevated: "#eeeee9"
  border: "#d7d7d0"
  border-subtle: "#e3e3dc"
  border-strong: "#92928a"
  text: "#292923"
  text-secondary: "#4c4c43"
  text-muted: "#65655a"
  text-placeholder: "#727267"
  brand-gold: "#ffe08a"
  gold-hover: "#ffda70"
  gold-pressed: "#f4cd62"
  gold-ink: "#302609"
  gold-wash: "#fff4d3"
  action: "#705413"
  provenance: "#8a286d"
  provenance-bg: "#f8edf5"
  provenance-border: "#dcc2d5"
  state-red: "#b3261e"
  state-red-bg: "#fdecea"
  state-red-border: "#e7b9b5"
  state-amber: "#80530b"
  state-amber-bg: "#fff4d6"
  state-amber-border: "#dfc58d"
  state-green: "#286442"
  state-green-bg: "#eaf3eb"
  state-green-border: "#b6d0bc"
  state-blue: "#4c5155"
  state-blue-bg: "#f0f1ef"
  state-blue-border: "#c9cdc6"
typography:
  display:
    fontFamily: "Public Sans, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "clamp(1.5rem, 1.35rem + 0.6vw, 2rem)"
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Public Sans, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 700
    letterSpacing: "-0.025em"
  body:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif"
    fontSize: "16px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontWeight: 600
    fontSize: "14px"
  badge:
    fontWeight: 600
    fontSize: "11px"
    letterSpacing: "0.05em"
    textTransform: "uppercase"
  monospace:
    fontFamily: "ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace"
rounded:
  sm: "0.375rem"
  md: "0.625rem"
  lg: "0.75rem"
spacing:
  content-max: "90rem"
  content-padding: "2rem 2.5rem 3rem"
  card-padding: "1rem"
  button-padding: "0.5rem 1rem"
  input-padding: "0.5rem 0.75rem"
  nav-min-height: "4.25rem"
components:
  button-primary:
    backgroundColor: "{colors.brand-gold}"
    textColor: "{colors.gold-ink}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-primary-hover:
    backgroundColor: "{colors.gold-hover}"
  button-primary-active:
    backgroundColor: "{colors.gold-pressed}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-secondary-hover:
    backgroundColor: "{colors.surface-hover}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-ghost-hover:
    backgroundColor: "{colors.surface-hover}"
    textColor: "{colors.text}"
  button-danger:
    backgroundColor: "{colors.state-red-bg}"
    textColor: "{colors.state-red}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.input-padding}"
  badge-status:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "0.125rem 0.5rem"
  card-panel:
    backgroundColor: "{colors.panel}"
    rounded: "{rounded.md}"
    padding: "{spacing.card-padding}"
---

# Design System: Bragi Books

## Overview

**Creative North Star: "The Record Workspace"**

Bragi Books is a work surface for its operator. Neutral white and graphite panels carry the work — import lists, match rows, the queue, the finished library — while the original develop gold is the chrome and the action: the sticky top bar, the primary buttons, the pale selection wash. The app runs in Operate mode: a batch of sources is reviewed at a glance, exceptions go to a focused record inspector, and the included books are approved in one counted save. Density comes from honest rows, hairline borders, and state that reads at 16px — not from ornament.

The earlier dark tape-editor world (Merge Bench, station rail, splice meters) is retired and replaced by this neutral/gold app-wide rollout. What did not change is the brand: the exact original stacked-books mark and favicon restored from develop, Public Sans 700 headings, and the unchanged system body face.

**Key Characteristics:**
- Neutral white/graphite work surfaces; gold reserved for the header, primary actions, and selection wash
- Pale-gold selection (#fff4d3); dark gold/graphite (#705413) for secondary actions and focus, not blue
- Public Sans 700 (self-hosted, OFL-licensed) headings; system sans body; mono for paths, IDs, output
- Flat panels: 4–12px radii by role, 1px hairlines; shadows reserved for their roles (button seat, picker, modal)
- Honest state: absent values labeled plainly; StatusBadge is glyph + word + color; reduced motion collapses all animation
- Original develop branding: stacked-books logo and favicon, unmodified

## Colors

Neutral stone ground with one gold accent and a four-state guard.

### Primary
- **Brand Gold** (`#ffe08a`): the top bar, primary buttons, the skip link — always paired with dark gold ink (`#302609`). Hover `#ffda70`, pressed `#f4cd62`.
- **Pale Gold Wash** (`#fff4d3`, `--workspace-selected`): selection — selected rows and lists, the pressed mode segment, text/in-field selection.

### Secondary
- **Dark Gold** (`#705413`, `--workspace-action` / `--focus-ring`): inline links, checked boxes and radio dots, the primary button's border (hover `#806115`), and the 3px keyboard focus outline. The pinned replacement for blue/cobalt on secondary and focus.
- **Graphite Ink Range** (`#292923` → `#4c4c43` → `#65655a`): headings, body, muted copy.
- **Button Boundary** (`#92701c`): the 1.5px border that seats the gold button on light surfaces.

### Tertiary
- **Provenance Magenta** (`#8a286d`; wash `#f8edf5`, border `#dcc2d5`): the AudiobookDB-sourced data mark. The only magenta.

### Neutral
- **Ground** `#f5f5f4`: page background.
- **Panel** `#ffffff`: cards, panels, lists, dialogs.
- **Surface** `#f8f8f6`: inputs, toolbars, striped header rows.
- **Surface Hover / Pressed** `#efefec` / `#e7e7e2`: row and control hover, pressed states.
- **Elevated** `#eeeee9`: skeleton fills.
- **Lines**: `#d7d7d0` hairline, `#e3e3dc` divider, `#92928a` strong border and input stroke.
- **State guards**: red `#b3261e` / `#fdecea` / `#e7b9b5`; amber `#80530b` / `#fff4d6` / `#dfc58d`; green `#286442` / `#eaf3eb` / `#b6d0bc`; inform blue-grey `#4c5155` / `#f0f1ef` / `#c9cdc6` (declared, sparse).

### Named Rules
**The Gold-Where-Hands-Go Rule.** Gold (`#ffe08a`) marks where a hand goes: the top bar, primary buttons, and the pale selection wash. It is never heading ink, never status, never decoration.

**The Provenance-Magenta Rule.** Magenta (`#8a286d`) marks data sourced from AudiobookDB and always ships with the "AbDB" word mark. Never status, never action, never ornament.

**The Dark-Gold Focus Rule.** Keyboard focus and secondary actions draw from the dark gold/graphite family (`#705413`). Blue and cobalt are not focus or selection colors in this system.

## Typography

**Display Font:** Public Sans 700 — Latin subset, self-hosted at `/fonts/public-sans-latin-700.woff2`, SIL OFL 1.1 (license travels at `/fonts/PublicSans-LICENSE.txt`; no hosted CSS or font requests at runtime).
**Body Font:** system sans (`ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, …`).
**Technical Font:** system mono (`ui-monospace, 'Cascadia Code', 'JetBrains Mono', …`) for paths, job IDs, and output.

**Character:** a confident, slightly tight humanist voice for headings and the wordmark over a quiet system face for the row work; mono whenever a value is a path, ID, or machine output.

### Hierarchy
- **Display** (700, `clamp(1.5rem, 1.35rem + 0.6vw, 2rem)`, 1.25, −0.02em): page titles under the page-header rule. Public Sans scopes to h1–h3 app-wide, so record titles (h3) and other section headings carry it too.
- **Title** (700–650, 1.0625–1.25rem): wordmark (20px 700, −0.025em) and section headings (17px 650).
- **Body** (400, 16px, 1.5): prose kept to 65ch; dense rows step down 14 → 13 → 11px.
- **Label** (600, 14px): nav links, field labels, batch counts.
- **Badge** (600, 11px, 0.05em, uppercase): status tags.

### Named Rules
**The Two-Voice Rule.** Public Sans carries the headings (h1–h3, including record titles) and the wordmark; body, labels, and row content stay on the system face, and mono is reserved for paths, IDs, and output.

## Layout

One operator at one surface. Content is a full-width composition capped at 90rem, padded 2rem/2.5rem (1.25rem/1rem under 900px). The sticky gold header rides on top (4.25rem + 1px); below 900px it becomes a fixed two-row 6.5625rem header with the destinations moved to a scrollable second row. Pages open with the page header — title, optional description, hairline rule — then the route's own working surface. The Queue keeps its hierarchy: ready work and active jobs in two columns (1fr / 21rem, collapsing at 1050px), job history as collapsible sections below. Buttons, nav links, and inputs share a 44px (2.75rem) minimum.

## Elevation & Depth

Panels are flat at rest: white cards on the `#f5f5f4` ground, `#f8f8f6` toolbars and inputs, 1px hairlines for containment. Shadows serve specific roles — the gold button's seat, the floating picker, the modal — plus inset shadows used to draw a 1px inner selection outline (the pressed mode segment).

### Shadow Vocabulary
- **Button seat** (`box-shadow: 0 1px 2px #29292314`): the faint grounding shadow under primary gold buttons at rest.
- **Picker** (`box-shadow: 0 4px 14px rgb(41 41 35 / 0.1)`): the floating candidate panel on Match.
- **Modal** (Tailwind `shadow-2xl`, `0 25px 50px -12px rgb(0 0 0 / 0.25)`): floating dialogs with their dimmed backdrop.
- **Inset selection outline** (`box-shadow: inset 0 0 0 1px var(--border)`): the 1px inner edge on the pressed Batch/Focused segment.

### Named Rules
**The Flat-Panel Rule.** Surfaces are flat at rest; depth arrives as tone, hairline, and state — shadow only for its named roles above.

## Shapes

Rounding runs on four steps by role: 6px (`sm`) for buttons, inputs, checkboxes, badges, alerts, and provenance chips; 10px (`md`) for cards, panels, dialogs, and cover art; 12px (`lg`) available for enclosure rounding. A 4px corner (literal value, no token) appears only on Match's route-local elements: the review-mode segment, small row chips, and compact buttons. The workspace writes containment with 1px hairlines. Dots are fully round. No hard offset shadows.

## Components

### Buttons
- **Shape:** 6px radius, `0.5rem 1rem` padding, 44px min-height, 14px semibold.
- **Primary:** gold fill, `#302609` ink, 1.5px `#92701c` border + micro shadow; brightens to `#ffda70` on hover, `#f4cd62` on press; disabled at 0.45 opacity.
- **Secondary:** `#f8f8f6` fill, `#d7d7d0` border; hover darkens the fill and strengthens the border to `#92928a`.
- **Ghost:** transparent, secondary ink; surface fill on hover.
- **Danger:** red-wash fill, red ink, 2px red border.
- **Focus:** 3px `#705413` outline, 3px offset; loading swaps in a spinner with `aria-busy`, label stays visible.

### Navigation
- **Style:** gold bar (`#ffe08a`) with a 1px `#d7b85f` bottom edge; brand is the stacked-books mark (44px wide; 32px under 900px) plus a 20px 700 −0.025em wordmark in ink.
- **Links:** 14px 600 `#302609` on gold; hover `#f3cf73`.
- **Active:** white chip with a 1px `#705413` border and `#292923` ink (`aria-current="page"`); Settings sits right-aligned in the same grammar.
- **Mobile:** destinations move to a horizontally scrollable second row; targets stay ≥44px.

### Page Header
Title in the display face with an optional 15px secondary description (65ch) above a 1px `#d7d7d0` rule; 1.5rem below the rule. Route-local sticky heads (Match) keep the same hairline with mode controls and a counted approval action on the right.

### Inputs / Fields
- **Style:** `#f8f8f6` fill, 1px `#92928a` border, 6px radius, 44px min-height.
- **Focus:** border to `#292923`, 3px `#705413` focus outline; hover border `#65655a`; placeholder `#727267`.
- **Checkboxes / radios:** 16px, 6px-corner squares (circles for radios); the checked checkbox is solid dark gold `#705413` with a white tick, while the checked radio keeps a white face with a dark-gold border and dot — dark controls deliberately distinct from gold primary actions; indeterminate is a white dash on the dark-gold field.

### Status Badge
11px 600, 0.05em uppercase, 1.5px border, 6px corner (`rounded-sm`), 12px icon + word, never color alone: pending (graphite) · processing (amber) · done (green) · error (red) · ready (gold wash + strong border).

### Alert
`role="alert"` panel, 2px state border, 14px copy, 14px padding; success/warning/error take their state washes; Retry is a dark-gold text action.

### Mode Toggle
Segmented pair (Batch / Focused) on Match: `#f8f8f6` track, 1px border, 6px outer / 4px inner radius; the pressed segment takes the pale gold `#fff4d3` fill with a 1px inset hairline.

### Removal Pass (Match, Batch review)
A deliberate mode entered from the batch bar's "Select to remove". The row checkbox stops meaning "include in this save" and becomes a removal selector: the bar takes the `#fdecea` state-red wash with a `#e7b9b5` bottom edge, checked boxes paint `#b3261e` with a white tick, and picked rows sit on the same red wash with "Selected for removal" in red 12px. The bar counts "N of M sources selected to remove", carries the files-stay sentence, and pairs a ghost Cancel with a red-wash "Remove N records" button. On wide screens the bar docks under the sticky page head while the list scrolls; under 761px the same actions dock to a fixed bottom bar (white panel, 1px `#e7b9b5` top edge) and the toolbar keeps only select-all and the count. Confirmation is a native dialog listing every source name plus its selected record title, with the files sentence in the footer and Cancel focused on open. Per-row Remove keeps its inline two-step band for single removals.

### Provenance Tag
"AbDB" chip: `#f8edf5` fill, 1px `#dcc2d5` border, `#8a286d` 12px 600 uppercase; hover draws the full-provenance border; the full "AudiobookDB" label is secondary ink, un-cased. An anchor when a record URL exists.

### Cover Art
Square always: 3rem row thumbnails (6px corner) and the 16rem record-page hero (10px). The placeholder spine stands in for missing art — never a stretched image.

## Do's and Don'ts

### Do:
- **Do** keep the chrome gold: top bar and primary actions on `#ffe08a` with `#302609` ink; selection in the pale wash `#fff4d3`.
- **Do** draw secondary actions and keyboard focus from the dark gold/graphite family (`#705413`, 3px outline, 3px offset).
- **Do** build work surfaces neutral: `#ffffff` panels on the `#f5f5f4` ground, 1px `#d7d7d0` hairlines, radii within the 4–12px scale by role.
- **Do** render StatusBadge as glyph + word + color (never color alone), and label absent state plainly ("No catalog match", "details unavailable") — never hidden.
- **Do** collapse all animation under `prefers-reduced-motion` (shimmer, transitions, chase).
- **Do** keep the original develop branding: the stacked-books mark at `/images/logo-small-black.svg`, `favicon.ico`, and the Public Sans OFL license living beside the font file.
- **Do** keep destructive selection in its own mode: the include-in-save box never doubles as a removal selector, and a multi-record delete always lists the exact source names before it runs.

### Don't:
- **Don't** reintroduce tape-editor framing — no station rail, equipment, meters, tape strips, or kickers/eyebrows; the Merge Bench vocabulary is retired.
- **Don't** style focus, selection, or secondary state blue or cobalt.
- **Don't** use shadow for decorative depth — it has named roles (button seat, picker, modal, inset selection outline); and keep radii to the 4-step scale.
- **Don't** redraw, replace, or invent the logo or favicon.
- **Don't** render raw JSON in operator-facing errors — the sentence walks the payload; the raw data stays under the ErrorDetail expander.
