<script lang="ts">
	import type { Snippet } from 'svelte';
	// Test stub for the real Button: renders a real <button> (the page's
	// controls are clicked in tests) and passes through the props the page
	// relies on. Everything else is inert.
	let {
		children,
		onclick,
		disabled = false,
		loading = false,
		class: className = ''
	}: {
		children: Snippet;
		onclick?: (e: MouseEvent) => void;
		disabled?: boolean;
		loading?: boolean;
		class?: string;
	} = $props();
</script>

<button class={className} disabled={disabled || loading} {onclick} type="button">
	{@render children()}
</button>
