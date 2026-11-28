<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		children,
		actions
	}: {
		children?: Snippet;
		actions?: Snippet;
	} = $props();
</script>

<div role="status">
	{#if children}
		{@render children()}
	{/if}
	{#if actions}
		{@render actions()}
	{/if}
</div>
