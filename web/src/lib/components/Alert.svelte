<script lang="ts">
	let {
		variant = 'info',
		children,
		onretry
	}: {
		variant?: 'info' | 'success' | 'warning' | 'error';
		children: import('svelte').Snippet;
		onretry?: () => void;
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

	const iconLabel = $derived.by(() => {
		switch (variant) {
			case 'success': return 'ok';
			case 'warning': return 'warn';
			case 'error': return 'err';
			default: return 'info';
		}
	});
</script>

<div class="rounded-sm border border-2 p-3.5 {styles}" role="alert" aria-label={iconLabel}>
	<div class="flex items-start justify-between gap-4">
		<div class="text-sm leading-relaxed">{@render children()}</div>
		{#if onretry}
			<button
				type="button"
				class="shrink-0 text-sm font-bold text-[var(--accent)] hover:underline focus-visible:outline-[var(--accent)]"
				onclick={onretry}
			>
				Retry
			</button>
		{/if}
	</div>
</div>
