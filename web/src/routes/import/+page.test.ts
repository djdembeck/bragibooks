import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
// vi.mock hoists above these, so the factory can name them directly.
import ProbeAlert from './ProbeAlert.svelte';
import ProbeButton from './ProbeButton.svelte';

const { getMock, putMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: vi.fn(),
	put: putMock,
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));

// The enable-retry contract under test lives in the script, so the
// page's presentational imports stub out. Alert and Button render just
// enough (the children + actions snippets, a clickable button that
// respects `loading`) to drive the flow.
vi.mock('#lib/components/index.js', () => ({
	PageHeader: () => null,
	Alert: ProbeAlert,
	ErrorDetail: () => null,
	Button: ProbeButton,
	Skeleton: () => null,
	EmptyState: () => null
}));
vi.mock('@lucide/svelte', () => ({
	ChevronUp: () => null,
	ChevronDown: () => null,
	Folder: () => null,
	FileAudio: () => null,
	File: () => null
}));

import ImportPage from './+page.svelte';

function settingsFixture(): Record<string, unknown> {
	return {
		m4b_merge_binary: 'm4b-merge',
		input_dir: '/in',
		output_dir: '/out',
		completed_dir: '',
		num_cpus: 1,
		output_scheme: '{author}/{title}',
		region: 'us',
		indexing_enabled: false
	};
}

describe('import index-enable failure recovery', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		putMock.mockReset();
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path.startsWith('/directories')) return { path: '/in', entries: [] };
			return {};
		});
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		try {
			unmount(component);
		} catch {
			// not mounted (an earlier step failed)
		}
		target.remove();
		vi.useRealTimers();
	});

	async function mountPage() {
		component = mount(ImportPage, { target });
		flushSync();
		// Settle the mount-time load chain: settings -> directory listing.
		await vi.advanceTimersByTimeAsync(0);
	}

	function enableButton(): HTMLButtonElement {
		const button = [...target.querySelectorAll('button')].find(
			b => b.textContent?.trim() === 'Enable index'
		);
		if (!button) throw new Error('Enable index button not rendered');
		return button;
	}

	function putCount(): number {
		return putMock.mock.calls.filter(c => c[0] === '/settings').length;
	}

	it('keeps the Enable button usable after a failed PUT, so the retry path works', async () => {
		putMock.mockRejectedValue(new Error('network down'));
		await mountPage();

		const button = enableButton();
		expect(button.disabled).toBe(false);

		button.click();
		await vi.advanceTimersByTimeAsync(0);
		expect(putCount()).toBe(1);
		// The failure is reported with the original message...
		expect(target.textContent).toContain(
			"Couldn't enable source indexing. Check your connection and try again."
		);
		// ...and the busy flag must NOT survive the failure: the button is
		// clickable again (the loading guard at the top of enableIndex used
		// to block every retry once the first PUT failed).
		await vi.advanceTimersByTimeAsync(0);
		expect(button.disabled).toBe(false);

		button.click();
		await vi.advanceTimersByTimeAsync(0);
		expect(putCount()).toBe(2);
	});

	it('clears the busy flag on a successful PUT as well', async () => {
		putMock.mockResolvedValue({ ...settingsFixture(), indexing_enabled: true });
		await mountPage();

		enableButton().click();
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(0);
		expect(putCount()).toBe(1);
		// Success: the busy flag clears — the flow advanced past the PUT
		// instead of wedging the button in loading state. The re-init
		// may re-render the prompt, so re-query: no disabled Enable
		// button may remain.
		const after = [...target.querySelectorAll('button')].find(
			b => b.textContent?.trim() === 'Enable index'
		);
		if (after) expect(after.disabled).toBe(false);
	});
});
