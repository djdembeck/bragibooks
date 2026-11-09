import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { IndexState, IndexStatus, Settings } from '#lib/types.js';

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: vi.fn(),
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));

// The settings page composes many presentational components; the poll
// contract under test lives in the script, so the components stub out.
vi.mock('#lib/components/index.js', () => ({
	PageHeader: () => null,
	Button: () => null,
	Alert: () => null,
	ErrorDetail: () => null,
	Skeleton: () => null,
	Spinner: () => null,
	DirPicker: () => null,
	Modal: () => null,
	PathFormatBuilder: () => null
}));

import SettingsPage from './+page.svelte';

function settingsFixture(): Settings {
	return {
		m4b_merge_binary: 'm4b-merge',
		input_dir: '/in',
		output_dir: '/out',
		completed_dir: '',
		num_cpus: 1,
		output_scheme: '{author}/{title}',
		region: 'us',
		indexing_enabled: true
	};
}

function indexState(status: IndexState['status'], generation: number): IndexStatus {
	return {
		enabled: true,
		state: {
			root: '/in',
			entries: 1,
			last_run_at: '',
			status,
			detail: '',
			generation
		}
	};
}

describe('settings index in-flight poll', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		postMock.mockReset();
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') return indexState('queued', 1);
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
		// Settle the mount-time load chain: settings -> index status.
		await vi.advanceTimersByTimeAsync(0);
	}

	function indexCallCount(): number {
		return getMock.mock.calls.filter((c) => c[0] === '/index').length;
	}

	it('polls while the scan is in flight and stops at its own terminal', async () => {
		await mountPage();
		// The initial /index read (queued, generation 1) started the
		// poller; each 1 s tick issues one more read.
		expect(indexCallCount()).toBe(1);
		await vi.advanceTimersByTimeAsync(3000);
		expect(indexCallCount()).toBe(4);

		// The scan's own generation reaches a terminal: the next tick
		// observes it and the poller stops.
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('done', 1) : (getMock.mock.calls.length ? {} : settingsFixture())
		);
		await vi.advanceTimersByTimeAsync(1000);
		const stoppedAt = indexCallCount();
		expect(stoppedAt).toBeGreaterThanOrEqual(5);
		// No further ticks: the loop ended.
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(stoppedAt);
	});

	it('follows a NEW in-flight generation to its terminal, then stops', async () => {
		await mountPage();
		// The captured scan (gen 1) never lands: a newer scan (gen 2) is
		// in flight. The poller keeps going — it must not stop on the
		// assumption that only gen 1 will ever complete.
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('running', 2) : {}
		);
		await vi.advanceTimersByTimeAsync(2000);
		expect(indexCallCount()).toBe(3);

		// The new scan reaches its terminal: the page state mirrors it
		// and the poll stops (the effect re-runs on the terminal state).
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('done', 2) : {}
		);
		await vi.advanceTimersByTimeAsync(1000);
		const stoppedAt = indexCallCount();
		expect(stoppedAt).toBe(4);
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(stoppedAt);
	});

	it('does not poll when the initial status is already terminal', async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') return indexState('done', 1);
			return {};
		});
		await mountPage();
		expect(indexCallCount()).toBe(1);
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(1);
	});
});
