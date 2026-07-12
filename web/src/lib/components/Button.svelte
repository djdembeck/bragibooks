<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		children: Snippet;
		variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
		type?: 'button' | 'submit' | 'reset';
		disabled?: boolean;
		loading?: boolean;
		onclick?: (e: MouseEvent) => void;
		href?: string;
		class?: string;
	}

	let {
		children,
		variant = 'primary',
		type = 'button',
		disabled = false,
		loading = false,
		onclick,
		href,
		class: className = ''
	}: Props = $props();

	const isDisabled = $derived(disabled || loading);
	const baseClass = $derived.by(() => {
		switch (variant) {
			case 'secondary':
				return 'btn btn-secondary';
			case 'ghost':
				return 'btn btn-ghost';
			case 'danger':
				return 'btn bg-[var(--error-bg)] text-[var(--error)] border border-[var(--error)]/30 hover:brightness-125';
			default:
				return 'btn btn-primary';
		}
	});
</script>

{#if href}
	<a href={href} class="{baseClass} {className}">
		{#if loading}
			<span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true"></span>
		{/if}
		{@render children()}
	</a>
{:else}
	<button
		{type}
		class="{baseClass} {className}"
		disabled={isDisabled}
		aria-disabled={isDisabled}
		aria-busy={loading}
		onclick={onclick}
	>
		{#if loading}
			<span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true"></span>
		{/if}
		{@render children()}
	</button>
{/if}
