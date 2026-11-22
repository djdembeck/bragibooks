<script lang="ts">
	import type { Snippet } from 'svelte';
	import '../app.css';
	import { Nav, IndexStatusBanner } from '#lib/components/index.js';
	import { loadEngineCapabilities } from '#lib/engineCapabilities.js';

	let { children }: { children: Snippet } = $props();

	// The engine's capability document (path-token vocabulary) is fetched
	// once app-wide; path builders parse stored formats into token parts at
	// mount, so the page must not render before the vocabulary is loaded.
	let capsReady = $state(false);
	$effect(() => {
		void loadEngineCapabilities().then(() => (capsReady = true));
	});
</script>

<svelte:head>
	<title>Bragi Books</title>
</svelte:head>

<div class="app-shell--workspace flex min-h-screen flex-col">
	<Nav />
	<!-- App-wide source-index progress: visible on every page, including
	     Settings, while a scan is in flight. -->
	<IndexStatusBanner />
	<main id="main-content" tabindex="-1" class="workspace-main flex-1">
		{#if capsReady}
			{@render children()}
		{:else}
			<div class="caps-loading" role="status">Loading engine capabilities…</div>
		{/if}
	</main>
</div>

<style>
	.caps-loading {
		padding: 2rem 1rem;
		color: var(--text-muted);
		font-size: 0.9rem;
	}
</style>
