<script lang="ts">
	import '../app.css';
	import { Nav } from '$lib/components';
</script>

<svelte:head>
	<meta charset="utf-8" />
	<meta name="viewport" content="width=device-width, initial-scale=1" />
	<link rel="icon" href="%sveltekit.assets%/favicon.svg" />
	<title>Bragi Books</title>
</svelte:head>

<div class="flex min-h-screen flex-col bg-[var(--bg)] text-[var(--text)]">
	<Nav />
	<main class="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:py-8">
		<slot />
	</main>
</div>
