<script lang="ts">
	import type { Snippet } from 'svelte';

	// The books page drives the removal dialog through bind:open and the
	// footer snippet; the stub renders both when open (the confirm flow
	// under test needs its buttons reachable).
	let {
		open = $bindable(false),
		title,
		children,
		footer,
		closeDisabled = false,
		beforeClose
	}: {
		open?: boolean;
		title?: string;
		children: Snippet;
		footer?: Snippet;
		closeDisabled?: boolean;
		beforeClose?: () => boolean;
	} = $props();
</script>

{#if open}
	<div role="dialog" aria-label={title}>
		{@render children()}
		{#if footer}{@render footer()}{/if}
	</div>
{/if}
