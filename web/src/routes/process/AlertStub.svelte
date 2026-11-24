<script lang="ts">
	import type { Snippet } from 'svelte';
	// Test stub for the real Alert: renders the message and the actions
	// slot (the page's banner actions are asserted in tests). Everything
	// else is inert.
	let { children, actions }: { children: Snippet; actions?: Snippet } = $props();
</script>

<div class="alert">
	{@render children()}
	{#if actions}
		{@render actions()}
	{/if}
</div>
