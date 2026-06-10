---
name: Bragibooks
description: Audiobook library cleanup and management tool for self-hosted collectors
colors:
  brand-gold: "#ffe08a"
  accent-blue: "#3273dc"
  ink: "#0a0a0a"
  body-text: "#4a4a4a"
  surface: "#ffffff"
  border: "#dbdbdb"
typography:
  body:
    fontFamily: "BlinkMacSystemFont, -apple-system, Segoe UI, Roboto, Oxygen, Ubuntu, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.5
  heading:
    fontFamily: "BlinkMacSystemFont, -apple-system, Segoe UI, Roboto, Oxygen, Ubuntu, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.25
  title:
    fontFamily: "BlinkMacSystemFont, -apple-system, Segoe UI, Roboto, Oxygen, Ubuntu, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif"
    fontSize: "2rem"
    fontWeight: 600
    lineHeight: 1.25
  label:
    fontFamily: "BlinkMacSystemFont, -apple-system, Segoe UI, Roboto, Oxygen, Ubuntu, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    lineHeight: 1.5
rounded:
  sm: "4px"
  md: "10px"
  full: "50%"
spacing:
  sm: "8px"
  md: "16px"
  lg: "24px"
  xl: "30px"
components:
  button-primary:
    backgroundColor: "{colors.accent-blue}"
    textColor: "{colors.surface}"
    rounded: "{rounded.sm}"
    padding: "8px 16px"
  button-primary-hover:
    backgroundColor: "#2366c2"
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.sm}"
    padding: "{spacing.md}"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    padding: "8px 12px"
---

# Design System: Bragibooks

## 1. Overview

**Creative North Star: "The Organized Shelf"**

A well-organized shelf: warm wood tones, everything in place, nothing louder than it needs to be. Bragibooks borrows the feeling of a curated personal library where every item has its position and the system works so smoothly you forget it's there. The gold brand surface reads as warm wood grain, the white cards as the books on the shelf, and the blue accent as the tab you pull to start reading.

This system rejects three aesthetic families outright: SaaS dashboards with their hero metrics and gradient chrome, playful UIs with decorative illustration and bounce, and cold enterprise tools with sterile data walls. Bragibooks lives in the self-hosted tool space, and its visual language says "reliable workshop" not "marketing funnel" or "admin console."

The interface is task-linear: import, match, confirm, wait. Visual weight follows that thread. Decorative elements that compete with the work are prohibited.

**Key Characteristics:**
- Warm gold brand surface carrying the identity, white content surfaces carrying the work
- Flat, tonal depth through background shifts, not shadows
- Single sans-serif family with weight contrast for hierarchy, not multiple typefaces
- Blue accent reserved for actions and progress, never for decoration
- Tactile controls that feel sturdy and confident, not invisible or precious

## 2. Colors

A warm-gold brand surface with a cool-blue functional accent. The split is deliberate: gold carries identity (nav, footer, preloader), blue carries action (buttons, links, progress).

### Primary
- **Brand Gold** (#ffe08a): The signature color. Preloader background, hero/nav bar, footer. The visual "shelf" that frames the content. Used on every page as the structural brand element. Never used as text color directly; text on gold backgrounds uses Ink or Body Text depending on size.

### Secondary
- **Accent Blue** (#3273dc): The functional accent. Buttons (primary), links, spinner rings, progress fill. Appears only on interactive elements and progress indicators. Its restraint is the point: when you see blue, you know you can act.

### Neutral
- **Ink** (#0a0a0a): Near-black for headings and high-emphasis text. The dark anchor.
- **Body Text** (#4a4a4a): Gray for body copy, labels, secondary text. Warm enough to read comfortably on white backgrounds; never lighten further.
- **Surface** (#ffffff): White. The default card and panel background. The "pages on the shelf."
- **Border** (#dbdbdb): Light gray for dividers, input borders, progress track backgrounds. The shelf edge.

### Named Rules

**The Gold-Frame Rule.** Brand Gold frames the viewport (nav, footer, preloader) but never fills a content area. White is where the work happens. Gold is the shelf; white is the books.

**The Blue-Action Rule.** Accent Blue appears only on elements the user can click or watch progress. If it's not interactive or progressing, it's not blue. No blue decorations, no blue icons, no blue headings.

## 3. Typography

**Body Font:** System sans-serif stack (BlinkMacSystemFont, -apple-system, Segoe UI, Roboto, Oxygen, Ubuntu, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif)
**Display/Label Font:** Same family. Hierarchy through weight and size, not through typeface switches.

**Character:** Single-family type with weight contrast. The system font reads as familiar and native on every platform, which is the right feel for a self-hosted tool. Weight contrast (400 vs 600) and size steps create hierarchy without引入ing a second typeface.

### Hierarchy
- **Title** (600, 2rem / ~32px, line-height 1.25): Page-level headings. "Submit ASINs", "Settings". Rare; one per page.
- **Heading** (600, 1.5rem / ~24px, line-height 1.25): Section headings. "Loading Bragi Books...", book detail labels when used as section breaks.
- **Body** (400, 1rem / 16px, line-height 1.5): All readable content: book descriptions, form labels, status text. Max line length 65-75ch.
- **Label** (600, 1rem / 16px, line-height 1.5): Inline labels before values ("Title:", "Author(s):"). The semibold weight distinguishes the label from the value that follows.

### Named Rules

**The Single-Family Rule.** One typeface family, multiple weights. Adding a second font family needs a reason stronger than "it looks nice." The weight gap between label (600) and value (400) creates the hierarchy a second typeface would try to solve.

## 4. Elevation

No shadows. Depth is conveyed entirely through tonal layering: white cards and panels on the gold or off-white body background create separation through contrast, not through drop-shadow illusion. This is a flat system by conviction, not by default. The gold body background IS the depth cue: white sits "above" gold naturally.

The preloader overlay breaks this convention with a fixed-position gold surface at z-index 9999, but this is a transitional state, not a permanent elevation layer.

**The Flat-Shelf Rule.** Surfaces are flat. Depth comes from background tone, not `box-shadow`. If two surfaces need separation, shift the background lighter or darker. Never add a shadow to create hierarchy that a tone shift already provides.

## 5. Components

### Buttons
- **Shape:** Gently rounded (4px radius), confident proportions
- **Primary:** Accent Blue (#3273dc) background, white text, padding 8px 16px. Full-width inside card footers, auto-width inside forms.
- **Hover / Focus:** Darkened blue (#2366c2) on hover. Focus ring via browser default or 2px Accent Blue outline offset by 2px.
- **Secondary / Ghost:** White background, blue text, blue 1px border. Hover: light blue tint background.
- **Danger:** Red background (Bulma `is-danger`) for destructive confirmations only (remove item).

### Cards / Containers
- **Corner Style:** Gently rounded (4px radius)
- **Background:** Surface (#ffffff) on the gold or white body
- **Shadow Strategy:** None. Flat.
- **Border:** No border. The background contrast provides separation.
- **Internal Padding:** 16px (Bulma card default)

### Inputs / Fields
- **Style:** White background, Border (#dbdbdb) 1px stroke, 4px radius
- **Focus:** Blue border (#3273dc), thin blue focus ring
- **Error / Disabled:** Red border for validation errors, gray tint for disabled

### Navigation
- **Style:** Gold (#ffe08a) background bar. Bragi Books logo + title left-aligned. Breadcrumb navigation right-aligned with path markers.
- **Typography:** Title (600) for the logo text. Regular (400) for breadcrumb links, with semibold (600) for the active page.
- **Active state:** Active breadcrumb link gets `has-text-weight-light` treatment, `aria-current="page"`.
- **Mobile:** Stack nav vertically if needed; breadcrumb collapses to icon + active page name.

### Tabs
- **Style:** Toggle tabs (Bulma `is-toggle is-fullwidth`) on white background. Active tab gets filled background.
- **States:** Active tab filled, inactive tabs transparent with bottom border or text contrast.

### Modals
- **Style:** Standard Bulma modal pattern: semi-transparent dark overlay, centered white card.
- **Header:** Dark gray background (`modal-card-head`), white title.
- **Footer:** Flex layout with notification and action buttons side by side.

### Progress / Loading
- **Preloader:** Gold full-screen overlay with ripple spinner (blue rings) and indeterminate progress bar. Fade-out transition (0.3s ease-out).
- **Spinner:** Blue ripple animation on gold background.
- **Progress bar:** Blue fill on light gray track. Indeterminate mode (sliding 30% fill) during scanning; determinate mode (percentage fill) during processing.

## 6. Do's and Don'ts

### Do:
- **Do** use Brand Gold (#ffe08a) for nav, footer, and preloader: the structural frame.
- **Do** use Accent Blue (#3273dc) only for interactive elements (buttons, links) and progress indicators.
- **Do** maintain ≥4.5:1 contrast for body text. Body Text (#4a4a4a) on Surface (#ffffff) passes at 7.4:1.
- **Do** use weight contrast (600 vs 400) for label-value pairs instead of introducing a second typeface.
- **Do** let background tone shifts handle depth: white on gold is elevation enough.
- **Do** keep button labels as verb + object: "Submit ASINs", "Save settings", "Custom Search".
- **Do** use word break / overflow wrap on book descriptions and long file paths (already in place).
- **Do** support reduced motion: suppress spinner and progress animations via `prefers-reduced-motion: reduce`.

### Don't:
- **Don't** use blue for decoration, headings, or background fills. Blue means "you can click this" or "this is progressing."
- **Don't** add box-shadows to cards or containers. Tonal layering replaces shadow in this system.
- **Don't** use any SaaS dashboard patterns: hero metrics, gradient accents, card grids with icon + heading + text.
- **Don't** add playful or whimsical decoration: bouncing animations, cute illustrations, toy-like elements.
- **Don't** create cold enterprise layouts: dense unbroken data tables, sterile form walls, admin-panel monotony.
- **Don't** use Brand Gold as text color on white backgrounds; its contrast ratio (1.6:1) fails WCAG AA. Gold is a surface, not a text color.
- **Don't** add a second typeface family without a reason stronger than aesthetics. Weight contrast solves hierarchy.
- **Don't** use border-left or border-right greater than 1px as a colored accent stripe on cards or list items.
