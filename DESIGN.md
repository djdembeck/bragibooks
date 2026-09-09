---
name: Bragi Books
description: The Merge Bench — a dark tape-editor work surface for batch audiobook operations. Chapters go in one side, spliced m4b reels come out the other.
colors:
  bg: "oklch(0.24 0.015 75)"
  bg-panel: "oklch(0.30 0.012 75)"
  surface: "oklch(0.36 0.010 75)"
  surface-hover: "oklch(0.40 0.010 75)"
  surface-pressed: "oklch(0.44 0.010 75)"
  elevated: "oklch(0.40 0.010 75)"
  border: "oklch(0.52 0.010 75)"
  border-subtle: "oklch(0.44 0.008 75)"
  border-strong: "oklch(0.60 0.012 75)"
  rail-line: "oklch(0.42 0.012 75)"
  text: "oklch(0.96 0.005 75)"
  text-secondary: "oklch(0.88 0.006 75)"
  text-muted: "oklch(0.80 0.006 75)"
  text-placeholder: "oklch(0.76 0.005 75)"
  enamel: "oklch(0.98 0.003 75)"
  state-red: "oklch(0.69 0.18 25)"
  state-red-bg: "oklch(0.30 0.04 25)"
  state-red-border: "oklch(0.44 0.08 25)"
  state-amber: "oklch(0.78 0.12 85)"
  state-amber-bg: "oklch(0.32 0.04 85)"
  state-amber-border: "oklch(0.44 0.08 85)"
  state-green: "oklch(0.67 0.14 145)"
  state-green-bg: "oklch(0.30 0.04 145)"
  state-green-border: "oklch(0.44 0.08 145)"
  state-blue: "oklch(0.65 0.12 250)"
  state-blue-bg: "oklch(0.30 0.03 250)"
  state-blue-border: "oklch(0.44 0.06 250)"
  accent: "oklch(0.82 0.18 78)"
  accent-hover: "oklch(0.88 0.18 78)"
  accent-pressed: "oklch(0.76 0.18 78)"
  accent-text: "oklch(0.24 0.015 75)"
  accent-wash: "oklch(0.82 0.18 78 / 0.08)"
  provenance: "oklch(0.72 0.19 330)"
  provenance-bg: "oklch(0.72 0.19 330 / 0.10)"
  provenance-border: "oklch(0.72 0.19 330 / 0.30)"
  focus-ring: "oklch(0.82 0.18 78 / 0.6)"
typography:
  body:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif"
    fontSize: "16px"
    fontWeight: 400
    lineHeight: 1.5
  monospace:
    fontFamily: "ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace"
  heading:
    fontWeight: 700
    lineHeight: 1.25
    letterSpacing: "-0.01em"
  label:
    fontWeight: 600
    fontSize: "14px"
  badge:
    fontWeight: "600-700"
    fontSize: "11px"
    letterSpacing: "0.04-0.06em"
    textTransform: "uppercase"
rounded:
  sm: "0.125rem"
  md: "0.25rem"
  lg: "0.25rem"
spacing:
  container-max: "max-w-6xl"
  container-padding: "1rem"
  card-padding: "1rem"
  button-padding: "0.5rem 1rem"
  input-padding: "0.5rem 0.75rem"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-danger:
    backgroundColor: "{colors.error-bg}"
    textColor: "{colors.error}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  card-panel:
    backgroundColor: "{colors.bg-panel}"
    rounded: "{rounded.md}"
    padding: "{spacing.card-padding}"
  status-badge:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "0.125rem 0.5rem"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.sm}"
    padding: "{spacing.input-padding}"
---

# Design System: Bragi Books

## Overview

**Creative North Star: "The Merge Bench"**

Bragi Books is a tape editor's bench for a personal audiobook pipeline. The operator is the person at the machine: they route source folders in one side (Intake), confirm catalog metadata (Match), queue the join (Queue), and collect finished reels (Finish). Everything on the surface is bench equipment — a tape-path rail connecting the four stations, a splice meter per book, manifest reels for lists, catalog badges that mark which data came from the community catalog. The bench metaphor is not decoration; it is the interface's vocabulary for real pipeline state.

The material is a dark warm workbench: brown-charcoal surfaces (OKLCH hue 75°), light warm text, near-white enamel for structural emphasis. Depth is tonal, not shadowed — a surface is a different mix of the same brown, stacked `bg → bg-panel → surface → elevated`. Corners are orthogonal (2–4px) because a bench is square. Gold exists only where a hand would go: primary buttons, the active page, the brand badge. A magenta reserved for provenance marks data that came from AudiobookDB — it is a tag, never a status and never a decoration. Status colors (red/amber/green/graphite, blue for information) always ship with a shape and a word.

**Key Characteristics:**
- Dark warm brown-charcoal surfaces, OKLCH hue 75° axis
- Tonal depth by surface mixing; no box-shadows (one exception: the modal overlay)
- Orthogonal geometry: 2–4px radii, square dots, miter joints
- Gold = action only; magenta = provenance only; red/amber/green/blue = status only
- Monospace for paths, job IDs, tabular data; system sans for everything else
- Every status is shape + text + color; reduced motion collapses all animation

## Colors

### Surfaces (Neutral — the workbench material)
- **Bench Ground** (`oklch(0.24 0.015 75)`): page background; the bench top.
- **Bench Panel** (`oklch(0.30 0.012 75)`): cards and workbench sections laid on the ground.
- **Bench Face** (`oklch(0.36 0.010 75)`): inputs and interactive faces; lighter still.
- **Bench Hover / Pressed / Elevated** (`0.40 / 0.44 / 0.40`): interaction rest-states and skeleton fills.
- **Structural Lines**: graphite borders at three weights — `border-subtle` (0.44) for containment, `border` (0.52) for definition, `border-strong` (0.60) for emphasis edges; `rail-line` (0.42) is the tape track itself.

### Text
- **Enamel** (`oklch(0.98 0.003 75)`): near-white; headings, brand wordmark, active markers. Structural emphasis, not body copy.
- **Text** (`0.96`), **Text Secondary** (`0.88`), **Text Muted** (`0.80`), **Text Placeholder** (`0.76`): a four-step warm ramp; muted carries disabled and honest "Unknown" labels in italic.

### Named Rules
**The Action-Only Gold Rule.** `--accent` marks where a hand goes: primary buttons, the active utility link, the brand badge. Never decoration, never status, never a heading color.

**The Provenance-Magenta Rule.** `--provenance` marks data sourced from AudiobookDB (AbdTag, match provenance, ADB diff panels) and is always paired with a text label. It is never applied to status, actions, or empty decoration.

**The Status-Never-Decorative Rule.** Red/amber/green signal state; blue signals information. They never color furniture, borders, or text emphasis.

## Typography

**Body Font:** system sans stack (`ui-sans-serif, system-ui, …`)
**Technical Font:** system mono stack (`ui-monospace, 'Cascadia Code', 'JetBrains Mono', …`) — paths, job IDs, output, tabular numerals

**Character:** workhorse UI sans plus a disciplined mono; no display face, no serif — the bench reads technical, not literary.

### Hierarchy
- **Heading** (700, `clamp(1.5rem → 2rem)`, 1.25, −0.01–0.02em): page titles; enamel.
- **Title** (600–700, 1.125–1.25rem): workbench section titles, selected-match records.
- **Body** (400, 16px, 1.5): default copy; prose kept to 65ch.
- **Label** (600, 14px): nav links, field labels; uppercase tracked for section identity.
- **Badge/Mono Readout** (600–700, 11px, 0.04–0.06em, uppercase): status badges, station labels, tabular data.

## Layout

Single operator at one machine; container `max-w-6xl`, 1rem side padding. Pages are stations: a sticky header (logo + utility links, tape strip on station pages) then the route's work surface. Lists are manifest rows — full-width horizontal cards with cover left, identity center, status right; the whole card is the target. The dashboard owns the four workflow bays at full width (2×2 under 640px); no other page repeats them. Spacing rhythm: 1rem inside, 1–1.5rem between sections, more space above headings than below.

## Elevation & Depth

**The Tonal-Depth Rule.** Depth is background mixing, never shadow: `bg → bg-panel → surface → elevated`. The only shadow in the system is the modal overlay (`shadow-2xl`-class distinction for the floating dialog). Things that need to float use a stronger border instead (`border-strong`).

## Shapes

Orthogonal, barely rounded: radius steps 2px (`sm`) and 4px (`md`/`lg` — same as md; there is no large radius in this system). Dots are squares (rail stations, status glyphs-as-shapes, active markers). Icons come from a single set (Lucide, 1.5–2.5 stroke) drawn inline in SVG; unicode glyphs never stand in for drawn icons except inside the fixed status-glyph vocabulary (◇ ◐ ◆ ✕ ▣).

## Components

### Buttons
- **Shape:** square corners at 2px, `0.5rem 1rem`, min-height 44px
- **Primary:** gold fill, dark text (`--accent` / `--accent-text`); brightens on hover, deepens on press
- **Secondary:** face fill with graphite border; border strengthens on hover
- **Ghost:** transparent, secondary text; face fill on hover
- **Danger:** red wash fill, red text, red border
- **Focus:** 2px text-white outline offset 2px (keyboard visible everywhere)

### Status Badge
- **Style:** 11px uppercase semibold, 2px radius, 1.5px border; always glyph + word + color
- **Vocabulary:** pending ◇ graphite · matched/ready ◐/▣ · processing ◐ amber · done ◆ green · error ✕ red

### Splice Meter
The bench's status light for the join operation. Pure geometry on a tape bar: pending = two dashed stubs ("Unspooled"); matched = two solid green halves with a gap ("Halves proofed"); processing = amber bar with progress fill and a chase light ("Splicing"); done = solid green bar with a diagonal seam notch cut in the panel color and a check at the right end ("Spliced"); error = red halves with a jagged break ("Splice snapped"). Redundant with StatusBadge by design: the badge is the accessible literal; the meter speaks bench.

### Tape Rail (StationRail)
The slim workflow strip under the header on station pages: four equal slots (Intake/Match/Queue/Finish), solid 2px `rail-line` track segments between the dots that stop at the dot edges, active station = filled enamel square dot + enamel label, `aria-current="step"`. No counts — the dashboard owns the bays.

### ADB Provenance Tag (AbdTag)
Small magenta chip reading "AbDB", optionally with the full label. It is an anchor when a record URL exists. The only magenta in the system.

### Match Listbox
Rich candidate picker replacing a native select on the match station: trigger styled as a field, panel with square cover thumbnails, title, author, narrator, and year per row. Full combobox ARIA (expanded/activedescendant/controls), arrow + Enter + Escape keyboard, click-outside closes.

### Cards / Containers
- **Corner:** 4px · **Background:** bench panel on bench ground · **Border:** 1.5px `border-subtle` (workbench rows must allow the listbox popover to overflow — `overflow: visible` on those rows)
- **Padding:** 1rem · **Row anatomy:** art left (square), identity center, state right

### Inputs / Fields
- **Style:** face fill, graphite border, 2px radius
- **Focus:** border → text white, 2px translucent gold ring; placeholder at 0.76
- **Help:** expandable "steps" details for setup flows (API key)

## Do's and Don'ts

### Do:
- **Do** draw depth with surface mixes; `bg → bg-panel → surface → elevated` (0.24 → 0.30 → 0.36 → 0.40).
- **Do** pair every status color with its shape glyph and word.
- **Do** use monospace for any path, ID, or numeric readout (`tabular-nums` for tables).
- **Do** keep gold interactive: if it isn't clickable or actively marking you, it isn't gold.
- **Do** keep the whole manifest card as the click target.
- **Do** collapse all animation under `prefers-reduced-motion` (chase, pulse, shimmer, ping).
- **Do** label absent state honestly: "Not generated", "Unknown", "No processing jobs yet" — italic muted, never hidden.

### Don't:
- **Don't** add box-shadows (exception: modal) — depth is tonal.
- **Don't** exceed 4px radius anywhere.
- **Don't** apply gold to static emphasis, headings, or status.
- **Don't** apply magenta outside provenance contexts.
- **Don't** render raw JSON in operator-facing errors — walk the payload to a sentence, keep the raw under a "Technical detail" expander.
- **Don't** use serif faces or bookshelf imagery — this is a bench, not a library.
- **Don't** repeat the workflow bays outside the dashboard.
- **Don't** stand unicode in for drawn icons outside the status-glyph vocabulary.
