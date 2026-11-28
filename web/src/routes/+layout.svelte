<script lang="ts">
	import type { Snippet } from 'svelte';
	import '../app.css';
	import { Nav, IndexStatusBanner } from '#lib/components/index.js';
	import { engineCapabilitiesLoaded, loadEngineCapabilities } from '#lib/engineCapabilities.svelte.js';

	let { children }: { children: Snippet } = $props();

	// The engine's capability document (path-token vocabulary) is fetched
	// once app-wide; path builders parse stored formats into token parts at
	// mount, so the page must not render before the vocabulary is loaded.
	// A failed load (server restarting, transient 5xx) retries with
	// backoff, bounded: the capsReady gate is untouched, the shell just
	// keeps asking. When the attempts are exhausted the loading screen
	// offers a manual retry.
	const capsMaxAttempts = 5;
	let capsAttempt = $state(0);
	let capsReady = $state(false);
	$effect(() => {
		const attempt = capsAttempt;
		if (capsReady) return;
		void loadEngineCapabilities().then(() => {
			if (engineCapabilitiesLoaded()) {
				capsReady = true;
				return;
			}
			if (attempt + 1 < capsMaxAttempts) {
				// Backoff, then re-run the effect for the next attempt
				// (the load dedupes, so a concurrent swap-refresh is not
				// doubled). A newer attempt (a manual retry) supersedes
				// this timer.
				setTimeout(() => {
					if (!engineCapabilitiesLoaded() && capsAttempt === attempt) {
						capsAttempt = attempt + 1;
					}
				}, 500 * 2 ** attempt);
			}
		});
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
			<div class="caps-loading" role="status">
				Loading engine capabilities…
				{#if capsAttempt + 1 >= capsMaxAttempts}
					<!-- The bounded retries are spent: the server may
					     still be down. A manual retry restarts the chain. -->
					<button
						class="caps-retry"
						type="button"
						onclick={() => (capsAttempt = 0)}
					>
						Still loading? Retry
					</button>
				{/if}
			</div>
		{/if}
	</main>
</div>

<style>
	.caps-loading {
		padding: 2rem 1rem;
		color: var(--text-muted);
		font-size: 0.9rem;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.75rem;
	}

	.caps-retry {
		padding: 0.4rem 0.9rem;
		font-size: 0.85rem;
		color: var(--text-primary);
		background: var(--bg-panel);
		border: 1px solid var(--border);
		border-radius: 0.375rem;
		cursor: pointer;
	}

	.caps-retry:hover {
		border-color: var(--accent, var(--text-secondary));
	}
</style>
