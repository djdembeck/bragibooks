<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		children,
		loading = false,
		disabled = false,
		onclick,
		type = 'button',
		...rest
	}: {
		children?: Snippet;
		loading?: boolean;
		disabled?: boolean;
		onclick?: (event: MouseEvent) => void;
		type?: 'button' | 'submit' | 'reset';
	} & Record<string, unknown> = $props();
</script>

<button
	type={type}
	disabled={disabled || loading}
	{...rest}
	onclick={onclick}
>
	{#if children}{@render children()}{/if}
</button>
