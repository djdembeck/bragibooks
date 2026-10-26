<script lang="ts">
	import { delayedLoad } from '../delayedLoad.svelte';

	// Mirrors the Home/Books/Import empty-state branch: the falsy initial
	// state must never render while the first load is in flight.
	let { responseDelay = 0 }: { responseDelay?: number } = $props();

	const dl = delayedLoad({ delay: 200 });
	let book = $state<string | null>(null);

	$effect(() => {
		dl.run(async () => {
			await new Promise((resolve) => setTimeout(resolve, responseDelay));
			book = 'Book 1';
		});
	});
</script>

{#if !dl.hasLoaded || dl.showSkeleton}
	<p>loading</p>
{:else if book === null}
	<p>empty</p>
{:else}
	<p>{book}</p>
{/if}
