<script lang="ts">
	import type { Snippet } from 'svelte';
	import Spinner from './Spinner.svelte';

	interface Props {
		children: Snippet;
		variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
		type?: 'button' | 'submit' | 'reset';
		disabled?: boolean;
		loading?: boolean;
		onclick?: (e: MouseEvent) => void;
		href?: string;
		class?: string;
		[key: string]: unknown;
	}

	let {
		children,
		variant = 'primary',
		type = 'button',
		disabled = false,
		loading = false,
		onclick,
		href,
		class: className = '',
		...rest
	}: Props = $props();

	const isDisabled = $derived(disabled || loading);
	const baseClass = $derived.by(() => {
		switch (variant) {
			case 'secondary':
				return 'btn btn-secondary';
			case 'ghost':
				return 'btn btn-ghost';
			case 'danger':
				return 'btn bg-[var(--state-red-bg)] text-[var(--state-red)] border-2 border-[var(--state-red-border)]';
			default:
				return 'btn btn-primary';
		}
	});
</script>

{#if href}
	<a href={href} class="{baseClass} {className}" {...rest}>
		{#if loading}
			<Spinner size="1rem" />
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
		{...rest}
	>
		{#if loading}
			<Spinner size="1rem" />
		{/if}
		{@render children()}
	</button>
{/if}
