<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		children,
		loading = false,
		onclick
	}: {
		children?: Snippet;
		loading?: boolean;
		onclick?: (event: MouseEvent) => void;
	} = $props();
</script>

<button type="button" disabled={loading} onclick={onclick}>
	{#if children}{@render children()}{/if}
</button>
