<script lang="ts">
	import { Button } from '$lib/components';
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		actionLabel,
		actionHref,
		onaction,
		children
	}: {
		title: string;
		description?: string;
		actionLabel?: string;
		actionHref?: string;
		onaction?: () => void;
		children?: Snippet;
	} = $props();
</script>

<div class="flex flex-col items-center justify-center rounded-sm border border-dashed border-[var(--border)] bg-[var(--surface)] px-6 py-10 text-center">
	<p class="text-base font-semibold text-[var(--text)]">{title}</p>
	{#if description}
		<p class="mt-1 max-w-xs text-sm text-[var(--text-secondary)]">{description}</p>
	{/if}
	{#if children}
		<div class="mt-4">{@render children()}</div>
	{/if}
	{#if actionLabel && (actionHref || onaction)}
		<div class="mt-4">
			{#if actionHref}
				<a href={actionHref} class="btn btn-primary">{actionLabel}</a>
			{:else if onaction}
				<Button variant="primary" onclick={onaction}>{actionLabel}</Button>
			{/if}
		</div>
	{/if}
</div>
