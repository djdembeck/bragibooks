<script lang="ts">
	import type { Snippet } from 'svelte';

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
	class="relative w-full max-w-lg rounded-xl border border-[var(--border)] bg-[var(--surface)] p-0 text-[var(--text)] shadow-2xl backdrop:bg-black/60"
	onclose={onClose}
>
	<div class="flex flex-col max-h-[80vh]">
		{#if title}
			<div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-5 py-4">
				<h2 class="text-lg font-semibold">{title}</h2>
				<button
					type="button"
					class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
					onclick={() => dialog?.close()}
					aria-label="Close"
				>
					x
				</button>
			</div>
		{/if}
		<div class="overflow-y-auto px-5 py-4">
			{@render children()}
		</div>
		{#if footer}
			<div class="border-t border-[var(--border-subtle)] px-5 py-4">
				{@render footer()}
			</div>
		{/if}
	</div>
</dialog>
