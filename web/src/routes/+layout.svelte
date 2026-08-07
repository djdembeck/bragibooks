<script lang="ts">
	import '../app.css';
	import { Nav } from '$lib/components';
</script>

<svelte:head>
	<title>Bragi Books</title>
</svelte:head>

<!--
  THESIS
  Bragi Books is a calm operations board for a solo self-hosting audiobook
  collector. The signal interlocking panel metaphor maps to a four-stage
  workflow: Intake → Match → Queue → Finish. Every screen is a station on
  that rail. Status is always text + shape + color. No SaaS chrome, no
  warm cream, no hidden state.

  OWN-WORLD
  Light cool work surface. Graphite text on near-white panels. Enamel-black
  structural lines. Orthogonal radii. Red/amber/green reserved for semantic
  states only. Technical sans-serif body, monospace for paths and job IDs.
  WCAG 2.2 AA, visible focus, keyboard navigation, reduced motion.

  STORY
  The user is an operator at a signal box. Each route is a station. The rail
  shows where you are and where the workflow leads. Panels are compact, dense,
  and honest. Buttons are actions, not decorations. Status badges carry shape
  and label so color blindness and reduced motion never hide state.

  FIRST VIEWPORT
  The dashboard opens on the workflow rail and a dense manifest: recent books
  and a continuation action (import or process). No hero, no oversized stat
  cards, no decorative greeting. The operator sees state and gets to work.

  FORM
  Shared tokens in app.css: surfaces, text, semantic states (red/amber/green),
  focus rings, orthogonal radii, reduced-motion kill switch. Components preserve
  existing prop contracts. The Nav is sticky with a station rail beneath it.

  FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, and DESIGN.md
-->

<div class="flex min-h-screen flex-col bg-[var(--bg)] text-[var(--text)]">
	<Nav />
	<main class="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:py-8">
		<slot />
	</main>
</div>
