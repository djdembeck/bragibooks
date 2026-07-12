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

	const styles = {
		info: 'bg-[var(--info-bg)] text-[var(--text)] border-[var(--info)]/20',
		success: 'bg-[var(--success-bg)] text-[var(--text)] border-[var(--success)]/20',
		warning: 'bg-[var(--warning-bg)] text-[var(--text)] border-[var(--warning)]/20',
		error: 'bg-[var(--error-bg)] text-[var(--text)] border-[var(--error)]/20'
	};
</script>

<div class="rounded-lg border p-4 {styles[variant]}" role="alert">
	<div class="flex items-start justify-between gap-4">
		<div class="text-sm leading-relaxed">{@render children()}</div>
		{#if onretry}
			<button
				type="button"
				class="shrink-0 text-sm font-medium text-[var(--accent)] hover:underline"
				onclick={onretry}
			>
				Retry
			</button>
		{/if}
	</div>
</div>
