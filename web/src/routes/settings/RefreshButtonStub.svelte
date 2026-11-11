<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		children,
		loading = false,
		disabled = false,
		onclick
	}: {
		children?: Snippet;
		loading?: boolean;
		disabled?: boolean;
		onclick?: (event: MouseEvent) => void;
	} = $props();
</script>

<button type="button" disabled={disabled || loading} onclick={onclick}>
	{#if children}{@render children()}{/if}
</button>
