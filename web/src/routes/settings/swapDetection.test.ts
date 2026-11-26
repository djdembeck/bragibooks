import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Settings } from '#lib/types.js';

// T17: swap detection must compare the server's response against the last
// SERVER-CONFIRMED binary. The draft (settings.m4b_merge_binary) is mutated
// by the input BEFORE the save runs, so a draft-to-draft comparison
// (pre-fix) can never see a real swap... and, conversely, a save whose
// response matches the confirmed value must NOT refresh even when the
// draft was just edited.
const { getMock, putMock, refreshMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	refreshMock: vi.fn(async () => {})
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: vi.fn(),
	put: putMock,
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));
vi.mock('#lib/components/index.js', () => {
	const Null = () => null;
	return {
		PageHeader: Null,
		Button: () => null,
		Alert: Null,
		ErrorDetail: Null,
		Skeleton: Null,
		Spinner: Null,
		DirPicker: Null,
		Modal: Null,
		PathFormatBuilder: Null
	};
});
// The real capabilities module caches app-wide; the swap test only needs
// refreshEngineCapabilities to be observable.
vi.mock('#lib/engineCapabilities.svelte.js', () => ({
	getEngineCapabilities: () => null,
	loadEngineCapabilities: async () => {},
	refreshEngineCapabilities: refreshMock
}));

import SettingsPage from './+page.svelte';

function settingsFixture(binary: string): Settings {
	return {
		m4b_merge_binary: binary,
		input_dir: '/in',
		output_dir: '/out',
		completed_dir: '',
		num_cpus: 1,
		output_scheme: '{author}/{title}',
		region: 'us',
		indexing_enabled: true
	};
}

describe('settings binary swap detection', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		putMock.mockReset();
		refreshMock.mockReset();
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture('old-bin');
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index')
				return {
					enabled: true,
					state: { root: '/in', entries: 1, last_run_at: '', status: 'done', detail: '', generation: 1 }
				};
			return {};
		});
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
		vi.useRealTimers();
	});

	async function mountPage() {
		component = mount(SettingsPage, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(0);
	}

	function binaryInput(): HTMLInputElement {
		const input = target.querySelector<HTMLInputElement>('#m4b_merge_binary');
		if (!input) throw new Error('m4b_merge_binary input not rendered');
		return input;
	}

	function setDraft(value: string) {
		const input = binaryInput();
		input.value = value;
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
	}

	function form(): HTMLFormElement {
		const form = target.querySelector<HTMLFormElement>('form.settings-form');
		if (!form) throw new Error('settings form not rendered');
		return form;
	}

	async function saveAndSettle() {
		form().requestSubmit();
		// The save is async: settle the put() round trip and the refresh.
		for (let i = 0; i < 10 && !putMock.mock.calls.length; i++) {
			await vi.advanceTimersByTimeAsync(0);
		}
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(0);
	}

	it('refreshes capabilities when the server response DIFFERS from the confirmed binary', async () => {
		await mountPage();
		// A real swap: the operator types the new binary AND the server
		// confirms it.
		setDraft('new-bin');
		putMock.mockResolvedValue(settingsFixture('new-bin'));
		await saveAndSettle();
		expect(putMock).toHaveBeenCalledTimes(1);
		expect(refreshMock).toHaveBeenCalledTimes(1);
	});

	it('does NOT refresh when the response matches the confirmed binary, even after a draft edit', async () => {
		await mountPage();
		// The operator edits the draft, the server keeps the confirmed
		// value (e.g. an edit the save did not carry, or a cancel). The
		// pre-fix draft comparison (new !== old) would refresh here.
		setDraft('new-bin');
		putMock.mockResolvedValue(settingsFixture('old-bin'));
		await saveAndSettle();
		expect(putMock).toHaveBeenCalledTimes(1);
		expect(refreshMock).not.toHaveBeenCalled();
	});

	it('refreshes when the SERVER changed the binary without a draft edit', async () => {
		await mountPage();
		// No draft edit at all: the server response is the only swap
		// signal.
		putMock.mockResolvedValue(settingsFixture('new-bin'));
		await saveAndSettle();
		expect(refreshMock).toHaveBeenCalledTimes(1);
	});

	it('tracks the baseline across saves: a second identical save does not refresh', async () => {
		await mountPage();
		setDraft('new-bin');
		putMock.mockResolvedValue(settingsFixture('new-bin'));
		await saveAndSettle();
		expect(refreshMock).toHaveBeenCalledTimes(1);
		// The confirmed binary is now 'new-bin': saving the same value
		// again is not a swap.
		setDraft('new-bin');
		await saveAndSettle();
		expect(putMock).toHaveBeenCalledTimes(2);
		expect(refreshMock).toHaveBeenCalledTimes(1);
	});
});
