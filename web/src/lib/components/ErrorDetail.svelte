<script lang="ts">
	// Collapsible "Technical detail" expander for the raw error payload.
	// Operators get the plain sentence; the untouched payload stays one click
	// away. Renders nothing when there is no raw detail to show. When the fix
	// lives in Settings (bad path, missing binary), a direct link is offered
	// right under the sentence.
	let {
		raw,
		label = 'Technical detail',
		needsSettings = false,
		class: className = 'mt-1'
	}: {
		raw?: string | null;
		label?: string;
		needsSettings?: boolean;
		class?: string;
	} = $props();
</script>

{#if needsSettings}
	<a
		href="/settings"
		class="inline-flex min-h-11 items-center text-xs font-medium text-[var(--text-secondary)] underline underline-offset-2 hover:text-[var(--text)]"
	>Fix in Settings</a
	>
{/if}

{#if raw}
	<details class={className}>
		<summary
			class="cursor-pointer select-none text-xs text-[var(--text-muted)] hover:text-[var(--text-secondary)]"
		>
			{label}
		</summary>
		<p
			class="mt-1 break-all rounded-sm border border-[var(--border-subtle)] bg-[var(--surface)] p-2 font-mono text-xs text-[var(--text-muted)]"
		>
			{raw}
		</p>
	</details>
{/if}
