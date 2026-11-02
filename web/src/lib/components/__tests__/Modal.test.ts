import { flushSync, mount, unmount } from 'svelte';
import { describe, expect, it } from 'vitest';
import ModalProbe from './ModalProbe.svelte';

function mountProbe() {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const component = mount(ModalProbe, { target });
	flushSync();
	return {
		dialog: target.querySelector('dialog') as HTMLDialogElement,
		boundText: () => target.querySelector('#probe-bound')?.textContent?.trim(),
		setOpen(next: boolean) {
			component.setOpen(next);
			flushSync();
		},
		teardown() {
			unmount(component);
			target.remove();
		}
	};
}

describe('Modal native close lifecycle', () => {
	it('ignores an old queued close after reopening, but accepts a fresh close', () => {
		const probe = mountProbe();
		try {
			expect(probe.dialog.open).toBe(true);
			// Browsers queue native close events; happy-dom emits them synchronously.
			// Hold the real close event at capture, then deliver it after reopening
			// to deterministically exercise the browser ordering without replacing
			// Modal or the dialog's native showModal/close implementations.
			let queuedClose: Event | undefined;
			probe.dialog.addEventListener(
				'close',
				(event) => {
					queuedClose = event;
					event.stopImmediatePropagation();
				},
				{ capture: true, once: true }
			);
			probe.setOpen(false);
			expect(probe.dialog.open).toBe(false);
			expect(queuedClose).toBeDefined();

			probe.setOpen(true);
			expect(probe.dialog.open).toBe(true);
			probe.dialog.dispatchEvent(new Event(queuedClose!.type));
			flushSync();
			expect(probe.dialog.open).toBe(true);
			expect(probe.boundText()).toBe('open');

			probe.dialog.close();
			flushSync();
			expect(probe.dialog.open).toBe(false);
			expect(probe.boundText()).toBe('closed');
		} finally {
			probe.teardown();
		}
	});

	it('still closes through the visible close control', () => {
		const probe = mountProbe();
		try {
			const close = probe.dialog.querySelector('[aria-label="Close dialog"]') as HTMLButtonElement;
			close.click();
			flushSync();
			expect(probe.dialog.open).toBe(false);
			expect(probe.boundText()).toBe('closed');
		} finally {
			probe.teardown();
		}
	});
});
