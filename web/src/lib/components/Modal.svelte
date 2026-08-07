<script lang="ts">
	import type { Snippet } from 'svelte';
	import { X } from '@lucide/svelte';

	let {
		open = $bindable(false),
		title,
		children,
		footer
	}: {
		open?: boolean;
		title?: string;
		children: Snippet;
		footer?: Snippet;
	} = $props();

	let dialog: HTMLDialogElement | undefined = $state();

	$effect(() => {
		if (!dialog) return;
		if (open) {
			dialog.showModal();
		} else {
			dialog.close();
		}
	});

	function onClose() {
		open = false;
	}
</script>

<dialog
	bind:this={dialog}
	class="relative w-full max-w-lg rounded-sm border border-[var(--border-strong)] bg-[var(--bg-panel)] p-0 text-[var(--text)] shadow-2xl backdrop:bg-[var(--enamel)]/50"
	onclose={onClose}
>
	<div class="flex flex-col max-h-[80vh]">
		{#if title}
			<div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-4 py-3">
				<h2 class="text-base font-bold text-[var(--enamel)]">{title}</h2>
				<button
					type="button"
					class="rounded-sm p-1.5 text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
					onclick={() => dialog?.close()}
					aria-label="Close"
				>
					<X class="h-4 w-4" />
				</button>
			</div>
		{/if}
		<div class="overflow-y-auto px-4 py-3">
			{@render children()}
		</div>
		{#if footer}
			<div class="border-t border-[var(--border-subtle)] px-4 py-3">
				{@render footer()}
			</div>
		{/if}
	</div>
</dialog>
