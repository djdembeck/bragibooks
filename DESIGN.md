---
name: Bragi Books
description: >
  A dark warm work surface for batch audiobook operations. Light warm text on
  brown-charcoal panels, near-white structural emphasis, orthogonal shapes, and
  a red/amber/green/blue status vocabulary. Gold brand accent for primary
  actions. Technical sans-serif for content, monospace for paths and job IDs.
  Four-stage workflow rail: Intake, Match, Queue, Finish.
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
  state-red: "oklch(0.62 0.18 25)"
  state-red-bg: "oklch(0.30 0.04 25)"
  state-red-border: "oklch(0.44 0.08 25)"
  state-amber: "oklch(0.78 0.12 85)"
  state-amber-bg: "oklch(0.32 0.04 85)"
  state-amber-border: "oklch(0.44 0.08 85)"
  state-green: "oklch(0.65 0.14 145)"
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
  focus-ring: "oklch(0.82 0.18 78 / 0.6)"
typography:
  body: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif"
  monospace: "ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace"
  base-size: "16px"
  base-weight: 400
  base-line-height: 1.5
  heading-weight: 700
  heading-line-height: 1.25
  heading-tracking: "-0.01em"
  label-weight: 600
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
  alert-padding: "0.875rem"
  modal-max-width: "32rem"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-text}"
    typography: "{typography.base-weight} / {typography.base-size}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    typography: "{typography.base-weight} / {typography.base-size}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text-secondary}"
    typography: "{typography.base-weight} / {typography.base-size}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  button-danger:
    backgroundColor: "{colors.state-red-bg}"
    textColor: "{colors.state-red}"
    typography: "{typography.base-weight} / {typography.base-size}"
    rounded: "{rounded.sm}"
    padding: "{spacing.button-padding}"
  input-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    typography: "{typography.base-weight} / {typography.base-size}"
    rounded: "{rounded.sm}"
    padding: "{spacing.input-padding}"
    height: "auto"
  checkbox:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.sm}"
    size: "1rem"
  radio:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.accent-text}"
    rounded: "9999px"
    size: "1rem"
  card-panel:
    backgroundColor: "{colors.bg-panel}"
    padding: "{spacing.card-padding}"
    rounded: "{rounded.md}"
  status-badge:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "0.125rem 0.5rem"
    size: "0.75rem"
  nav:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.text-secondary}"
    typography: "{typography.label-weight} / text-sm"
    rounded: "{rounded.sm}"
    padding: "0.625rem 0.75rem"
  workflow-rail:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.text-muted}"
    typography: "{typography.label-weight} / 0.6875rem"
    rounded: "{rounded.sm}"
    padding: "0.5rem 1rem"
  station-id:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.sm}"
    padding: "0.125rem 0.5rem"
    size: "0.75rem"
  page-header:
    textColor: "{colors.enamel}"
    typography: "{typography.heading-weight} / text-2xl"
    rounded: "{rounded.sm}"
  alert:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    typography: "{typography.base-weight} / text-sm"
    rounded: "{rounded.sm}"
    padding: "{spacing.alert-padding}"
  modal:
    backgroundColor: "{colors.bg-panel}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    padding: "0"
    width: "32rem"
  empty-state:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.sm}"
    padding: "2.5rem 1.5rem"
  skeleton:
    backgroundColor: "{colors.elevated}"
    rounded: "{rounded.sm}"
    height: "auto"
    width: "100%"
---

# Design System: Bragi Books

## Overview

**Signal Interlocking Panel** — a dark warm work surface for batch audiobook operations. The interface reads like a railway control board: light warm text on brown-charcoal panels, near-white structural emphasis, orthogonal shapes, and a red/amber/green/blue status vocabulary. Gold brand accent for primary actions. Technical sans-serif for content, monospace for paths and job IDs. The four-stage workflow — Intake, Match, Queue, Finish — is visible as an interlocking rail of connected stations.

This system is not a bookshelf, a SaaS dashboard, or an admin console. It is the operations panel for a personal audiobook pipeline: select, match, queue, finish. Calm, capable, honest about state.

### What This System Rejects

- SaaS dashboards with hero metrics, gradient chrome, and card grids
- Cool blue enterprise panels with sterile near-white text and decorative gradients
- Warm cream/serif bookshelf treatments with decorative illustrations
- Rounded-everything aesthetics and decorative motion

### Design Principles

1. **Warm, not cool.** Neutral axis at 75° OKLCH — brown charcoal, not blue grey.
2. **Dark surface, light text.** Deep warm pages with layered lighter panels. White-near headings for contrast.
3. **Flat depth.** Background tone shifts, not box-shadow.
4. **Orthogonal.** Barely rounded corners — the signal board is square.
5. **Honest state.** Every book, job, and folder shows its status plainly.
6. **Gold accent for action.** The brand gold draws the eye to interactive primary actions, distinct from semantic states.
7. **Status is never color alone.** Shape glyphs accompany every color signal.

## Colors

All colors use OKLCH for perceptual uniformity. The neutral axis is 75° — a warm brown charcoal that rejects cool blue-grey or cream undertones.

### Surfaces

| Token | Value | Purpose |
| --- | --- | --- |
| `--bg` | `oklch(0.24 0.015 75)` | Page background — dark warm brown-charcoal work surface |
| `--bg-panel` | `oklch(0.30 0.012 75)` | Panel/card content background — lighter warm panel |
| `--surface` | `oklch(0.36 0.010 75)` | Input backgrounds, interactive surfaces |
| `--surface-hover` | `oklch(0.40 0.010 75)` | Hover state for surfaces |
| `--surface-pressed` | `oklch(0.44 0.010 75)` | Pressed/active state for surfaces |
| `--elevated` | `oklch(0.40 0.010 75)` | Skeleton placeholders, depth accent |

### Structural Lines

| Token | Value | Purpose |
| --- | --- | --- |
| `--border` | `oklch(0.52 0.010 75)` | Default borders |
| `--border-subtle` | `oklch(0.44 0.008 75)` | Dividers, nav separator |
| `--border-strong` | `oklch(0.60 0.012 75)` | Modal borders, high-emphasis edges |
| `--rail-line` | `oklch(0.42 0.012 75)` | Workflow rail track |

### Typography Colors

| Token | Value | Purpose |
| --- | --- | --- |
| `--text` | `oklch(0.96 0.005 75)` | Body text — light warm white |
| `--text-secondary` | `oklch(0.88 0.006 75)` | Secondary labels, nav inactive |
| `--text-muted` | `oklch(0.80 0.006 75)` | Disabled text, rail labels |
| `--text-placeholder` | `oklch(0.76 0.005 75)` | Input placeholder text |
| `--enamel` | `oklch(0.98 0.003 75)` | Headings, brand emphasis — near-white |

### Semantic States

Red/amber/green/blue — only used for status, never decorative.

| Token | Value | Meaning |
| --- | --- | --- |
| `--state-red` | `oklch(0.62 0.18 25)` | Error / stop / critical |
| `--state-red-bg` | `oklch(0.30 0.04 25)` | Red wash background (dark) |
| `--state-red-border` | `oklch(0.44 0.08 25)` | Red border |
| `--state-amber` | `oklch(0.78 0.12 85)` | Processing / warning / in-progress |
| `--state-amber-bg` | `oklch(0.32 0.04 85)` | Amber wash background (dark) |
| `--state-amber-border` | `oklch(0.44 0.08 85)` | Amber border |
| `--state-green` | `oklch(0.65 0.14 145)` | Done / success / go |
| `--state-green-bg` | `oklch(0.30 0.04 145)` | Green wash background (dark) |
| `--state-green-border` | `oklch(0.44 0.08 145)` | Green border |
| `--state-blue` | `oklch(0.65 0.12 250)` | Information / help / neutral notification |
| `--state-blue-bg` | `oklch(0.30 0.03 250)` | Blue wash background (dark) |
| `--state-blue-border` | `oklch(0.44 0.06 250)` | Blue border |

### Action Accent

Not a status — reserved for interactive primary actions and the active workflow station.

| Token | Value | Purpose |
| --- | --- | --- |
| `--accent` | `oklch(0.82 0.18 78)` | Primary buttons, active nav, active rail station — gold |
| `--accent-hover` | `oklch(0.88 0.18 78)` | Primary button hover — brighter gold |
| `--accent-pressed` | `oklch(0.76 0.18 78)` | Primary button active — deeper gold |
| `--accent-text` | `oklch(0.24 0.015 75)` | Text on gold accent background — dark surface color |
| `--accent-wash` | `oklch(0.82 0.18 78 / 0.08)` | Subtle gold tint (ready status badge) |

### Focus

| Token | Value | Purpose |
| --- | --- | --- |
| `--focus-ring` | `oklch(0.82 0.18 78 / 0.6)` | Semi-transparent gold focus ring for inputs |

## Typography

### Font Stacks

- **Body / UI:** `ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif`
- **Technical (paths, job IDs, output):** `ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace`

### Scale

| Role | Size | Weight | Line Height | Letter Spacing |
| --- | --- | --- | --- | --- |
| Base | 16px | 400 | 1.5 | normal |
| Heading | 2xl / 3xl | 700 | 1.25 | -0.01em |
| Label / Nav | 14px (text-sm) | 600 | — | — |
| Badge / Rail | 11px (0.6875rem) | 600–700 | — | 0.04–0.06em, uppercase |
| Input | 16px (inherit) | 400 | 1.5 | — |

### Headings

All heading levels (`h1`–`h4`) use `--enamel` color (near-white), `font-weight: 700`, `line-height: 1.25`, `letter-spacing: -0.01em`, `text-wrap: balance`.

## Layout

### Container

Max width `max-w-6xl` with horizontal margin auto and `px-4` (1rem) padding. Applied to nav, rail, and page content.

### Breakpoints

| Name | Value | Usage |
| --- | --- | --- |
| Mobile | `768px` | Nav wraps, brand title visible, responsive header margins |
| Tablet | `1024px` | Wider layout, full nav items visible |
| Desktop | `1216px` | Max-width container constraint |

### Workflow Rail

The four-stage workflow — Intake, Match, Queue, Finish — is rendered as a sticky interlocking rail below the primary navigation:

- **Stations:** Four labeled stops (Intake, Match, Queue, Finish) as vertical stacks — square dot above uppercase label
- **Dot:** `0.75rem` square (`border-radius: 0`), `2px solid --border`, `--surface` fill. Active fills `--enamel`.
- **Label:** `0.6875rem`, `font-weight: 600`, `letter-spacing: 0.04em`, uppercase, `--text-muted`. Active uses `--enamel`.
- **Segments:** `1px` height, `--border` color between station dots
- **Track:** `sticky`, `top: 0`, `z-index: 40`, `border-bottom: 2px solid var(--rail-line)`

## Elevation & Depth

Depth is communicated through background tone shifts, not box-shadow. The layer stack is:

```
--bg (0.24) → --bg-panel (0.30) → --surface (0.36) → --elevated (0.40)
```

Surfaces sit on slightly different tones; lighter content on darker background creates hierarchy without shadow. The **only** box-shadow used is on the modal dialog (`shadow-2xl`) for overlay distinction.

### Motion

| Token | Value | Usage |
| --- | --- | --- |
| `--ease-out` | `cubic-bezier(0.22, 1, 0.36, 1)` | Default easing for hover, focus, state transitions |
| `--duration-fast` | `100ms` | Hover and focus transitions |
| `--duration-normal` | `150ms` | Modal overlay, structural state changes |

Skeleton shimmer runs at `1.4s ease-in-out infinite` when animation is allowed.

### Reduced Motion

`prefers-reduced-motion: reduce` collapses all animation and transition durations to `0.01ms`. Skeleton shimmer becomes a static `--elevated` fill.

## Shapes

### Border Radii

Orthogonal design — corners are barely rounded. The signal board metaphor calls for sharp edges.

| Token | Value | Usage |
| --- | --- | --- |
| `--radius-sm` | `0.125rem` | Inputs, badges, small elements |
| `--radius-md` | `0.25rem` | Cards, panels, dialogs |
| `--radius-lg` | `0.25rem` | Same as md — no large radii |

Focus ring and `:focus-visible` outline use `--radius-sm`.

### Status Glyphs

Every status badge carries a shape glyph. Color is never the sole indicator.

| Status | Glyph | Shape Name | Meaning |
| --- | --- | --- | --- |
| Pending | `◇` | Open diamond | Idle, awaiting action |
| Processing | `◐` | Half-filled circle | In progress, running |
| Done | `◆` | Filled diamond | Completed, succeeded |
| Error | `✕` | Cross | Failed, critical |
| Ready | `▣` | Filled square | Matched and queued |

### Rail Station Dot

Square — `border-radius: 0`. Orthogonal. Active stations fill solid with `--enamel`.

## Components

### Button

Variants: `primary`, `secondary`, `ghost`, `danger`. Supports `loading` spinner and `href` (renders as `<a>`).

- **Primary:** `--accent` (gold) fill with `--accent-text` (dark), brightens on hover, deepens on press
- **Secondary:** `--surface` fill, `--border` stroke, lightens surface on hover
- **Ghost:** Transparent fill, `--text-secondary` text, surface fill on hover
- **Danger:** `--state-red-bg` fill, `--state-red` text, `--state-red-border` stroke

All share `inline-flex`, `gap-2`, `font-semibold`, `text-sm`, `padding: 0.5rem 1rem`, `border-radius: var(--radius-sm)`, `border: 1.5px solid transparent` (or colored border per variant). Disabled and loading states set `opacity: 0.45`, `cursor: not-allowed`.

### Input Field

Base styles for `<input>`, `<select>`, `<textarea>` set in `@layer base`:

- Block, full-width, `--surface` background, `--text` color
- `1px solid var(--border)` border, `var(--radius-sm)` radius, `padding: 0.5rem 0.75rem`
- Hover: border → `--text-muted`
- Focus: border → `--text`, `box-shadow: 0 0 0 2px var(--focus-ring)`
- Placeholder: `--text-placeholder`

Select has an inline SVG chevron (square stroke, miter joints) positioned at right.

### Checkbox / Radio

Custom-styled form controls:

- **Checkbox:** `1rem` square, `var(--radius-sm)`, `1.5px --border` stroke. Checked fills `--accent` with `✓` mark.
- **Radio:** Circular (`border-radius: 9999px`). Checked fills `--accent` with centered dot.
- Both use `--focus-ring` outline on focus-visible.

### Card / Panel

Compact content container: `--bg-panel` background, `1.5px solid var(--border-subtle)` border, `var(--radius-md)` radius, `1rem` padding.

### Status Badge

Compact inline status indicator: `text-xs`, `font-semibold`, `rounded-sm`, `px-2 py-0.5`, `1.5px solid` border. Each status has color + background + border + shape glyph (see Shapes section above). Status is never conveyed by color alone.

### Navigation

Sticky header with `--bg` background and `--border-subtle` bottom border. Contains:

- **Brand:** Logo mark in `--accent` (gold) square + "Bragi Books" in `--enamel` (hidden on mobile)
- **Primary links:** `flex` row, `gap-0.5`, scrollable overflow. Each link is `nav-link` with `--text-secondary` text, `--surface-hover` on hover. Active link uses `--accent` (gold) fill with `--accent-text` (dark) and `--accent` border

### Station ID Badge

Page header identity tag: `text-xs`, `font-bold`, `letter-spacing: 0.06em`, uppercase. `--surface` background, `--border` stroke, `--text-secondary` text.

### Page Header

Combines `<h1>` (`text-2xl` / `text-3xl`, `font-bold`, `tracking-tight`, `--enamel`) with optional station ID badge and description (`--text-secondary`, `max-w-prose`). Responsive margin: `mb-5` / `mb-7`.

### Alert

`role="alert"` container with `border-2` and `rounded-sm` corners. Variants map to semantic state backgrounds:

- Info: `--surface` / `--border`
- Success: `--state-green-bg` / `--state-green-border`
- Warning: `--state-amber-bg` / `--state-amber-border`
- Error: `--state-red-bg` / `--state-red-border`

Optional retry button in `--accent` text.

### Modal

HTML `<dialog>` element: `max-w-lg`, `--bg-panel` background, `--border-strong` border, `shadow-2xl`. Backdrop uses `--enamel/50` overlay. Header (when titled) has `--border-subtle` separator. Close button is `--text-muted` with `--surface-hover` on hover.

### Empty State

Centered placeholder: `--surface` background, dashed `--border` border, `rounded-sm`. Title in `--text` (`font-semibold`), description in `--text-secondary` (`max-w-xs`). Optional primary action button.

### Skeleton

Loading placeholder in two modes:

- **Static:** `--elevated` fill, `var(--radius-sm)` radius
- **Animated:** Shimmer gradient cycling between `--surface` and `--elevated` at `1.4s ease-in-out infinite`

Supports `line`, `circle`, and `rect` shape variants.

## Do's and Don'ts

### Do

- Use `--enamel` for headings, active rail station dots, and brand emphasis text
- Use `--accent` only for primary buttons, active nav links, and active workflow stations
- Maintain ≥4.5:1 contrast for body text (`--text` on `--bg` passes comfortably)
- Use weight contrast (700 vs 600 vs 400) for typographic hierarchy
- Let background tone shifts handle depth: `--bg` → `--bg-panel` → `--surface` is the layer stack
- Keep button labels as verb + object: "Submit ASINs", "Save settings", "Browse folders"
- Support reduced motion: collapse all transitions to `0.01ms`, make skeleton static
- Always include a shape glyph with status color — color is never the sole indicator
- Use monospace for file paths, job IDs, and technical output
- Let the workflow rail communicate task position
- Use blue state (`--state-blue`) for informational, non-critical notifications

### Don't

- Use cool blue-grey, cream, or golden neutral tones — the system is warm brown charcoal
- Add box-shadows to cards or containers — tonal layering replaces shadow
- Use any SaaS dashboard patterns: hero metrics, gradient accents, card grids with icon + heading + text
- Add serif fonts for body text or bookshelf metaphors — technical sans-serif only
- Create decorative illustrations, ornamental motion, or bounce animations
- Use large border radii — the design is orthogonal
- Convey status by color alone — every state has a shape glyph
- Add a second typeface family without a reason stronger than aesthetics
- Use `--accent` for decoration — it signals interactivity
- Hide unknown state — label it honestly
