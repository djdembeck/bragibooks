<script lang="ts">
	import type { Snippet } from 'svelte';
	import '../app.css';
	import { Nav } from '#lib/components/index.js';

	let { children }: { children: Snippet } = $props();
</script>

<svelte:head>
	<title>Bragi Books</title>
</svelte:head>

<div class="app-shell--workspace flex min-h-screen flex-col">
	<Nav />
	<main id="main-content" tabindex="-1" class="workspace-main flex-1">
		{@render children()}
	</main>
</div>
