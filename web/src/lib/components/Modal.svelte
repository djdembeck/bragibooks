<script lang="ts">
	import type { Snippet } from 'svelte';
	import { X } from '@lucide/svelte';

	let {
		open = $bindable(false),
		title,
		children,
		footer,
		// Optional close gate: return false to keep the dialog open (the
		// caller is responsible for explaining why). Covers the close
		// button and Escape so a high-stakes dialog cannot be dismissed
		// by accident.
		beforeClose,
		// Visual counterpart to beforeClose: when the caller refuses every
		// dismissal for now (a deletion in flight), the X must not look
		// live. Escape stays gated by beforeClose.
		closeDisabled = false
	}: {
		open?: boolean;
		title?: string;
		children: Snippet;
		footer?: Snippet;
		beforeClose?: () => boolean;
		closeDisabled?: boolean;
	} = $props();

	// One id per dialog instance: two dialogs can be mounted on one page,
	// and a shared id would give both the same accessible name.
	const titleId = $props.id();

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

	function requestClose() {
		if (closeDisabled) return;
		if (beforeClose && !beforeClose()) return;
		dialog?.close();
	}

	// Native <dialog> normally closes on Escape, but some environments
	// (headless browsers, embedded webviews) suppress the cancel event.
	// Close explicitly so Escape is deterministic everywhere.
	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			requestClose();
		}
	}

	function onCancel(e: Event) {
		e.preventDefault();
		requestClose();
	}
</script>

<dialog
	bind:this={dialog}
	aria-labelledby={title ? titleId : undefined}
	class="w-[calc(100%-2rem)] max-w-lg rounded-[var(--radius-md)] border border-[var(--border-strong)] bg-[var(--bg-panel)] p-0 text-[var(--text)] shadow-2xl backdrop:bg-[var(--enamel)]/50"
	onclose={onClose}
	onkeydown={onKeydown}
	oncancel={onCancel}
>
	<div class="flex flex-col max-h-[80vh]">
		{#if title}
			<div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-4 py-3">
				<h2 id={titleId} class="text-base font-bold text-[var(--enamel)]">{title}</h2>
				<button
					type="button"
					class="min-h-11 min-w-11 rounded-sm p-1.5 text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent disabled:hover:text-[var(--text-muted)]"
					onclick={requestClose}
					disabled={closeDisabled}
					aria-label="Close dialog"
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
