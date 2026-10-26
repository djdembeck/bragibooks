<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		variant = 'info',
		children,
		onretry,
		// ARIA role of the root element. "alert" stays the default for
		// errors; informational banners pass "status" so they do not
		// interrupt.
		role = 'alert',
		// Right-aligned action slot (Dismiss and similar). The legacy
		// onretry button still renders after it.
		actions
	}: {
		variant?: 'info' | 'success' | 'warning' | 'error';
		children: Snippet;
		onretry?: () => void;
		role?: 'alert' | 'status' | 'note' | 'region';
		actions?: Snippet;
	} = $props();

	const styles = $derived.by(() => {
		switch (variant) {
			case 'success':
				return 'bg-[var(--state-green-bg)] text-[var(--text)] border-[var(--state-green-border)]';
			case 'warning':
				return 'bg-[var(--state-amber-bg)] text-[var(--text)] border-[var(--state-amber-border)]';
			case 'error':
				return 'bg-[var(--state-red-bg)] text-[var(--text)] border-[var(--state-red-border)]';
			default:
				return 'bg-[var(--surface)] text-[var(--text)] border-[var(--border)]';
		}
	});

</script>

<div class="rounded-sm border border-2 p-3.5 {styles}" role={role}>
	<div class="flex items-start justify-between gap-4">
		<div class="text-sm leading-relaxed">{@render children()}</div>
		{#if actions || onretry}
			<div class="flex shrink-0 items-center gap-1">
				{#if actions}
					{@render actions()}
				{/if}
				{#if onretry}
					<button
						type="button"
						class="inline-flex min-h-11 min-w-11 shrink-0 items-center justify-center px-2 text-sm font-bold text-[var(--workspace-action)] hover:underline focus-visible:outline-[var(--focus-ring)]"
						onclick={onretry}
					>
						Retry
					</button>
				{/if}
			</div>
		{/if}
	</div>
</div>
