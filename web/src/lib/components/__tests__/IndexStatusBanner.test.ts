import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	put: vi.fn(),
	post: vi.fn(),
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));

import IndexStatusBanner from '../IndexStatusBanner.svelte';
import type { IndexState, IndexStatus } from '../../types.js';

function stateFixture(): IndexState {
	return {
		root: '/in',
		entries: 1,
		last_run_at: '',
		status: 'done',
		detail: '',
		generation: 1
	};
}

function indexFixture(enabled: boolean): Record<string, unknown> {
	return {
		enabled,
		state: stateFixture()
	};
}

function statusFixture(overrides: Partial<IndexState>): IndexStatus {
	return { enabled: true, state: { ...stateFixture(), ...overrides } };
}

describe('index banner resident poll', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexFixture(true) : {}
		);
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		try {
			unmount(component);
		} catch {
			// not mounted (the test already unmounted it)
		}
		target.remove();
		vi.useRealTimers();
	});

	function indexCallCount(): number {
		return getMock.mock.calls.filter(c => c[0] === '/index').length;
	}

	it('keeps a 1 s cadence while the index is enabled, with no restart per response', async () => {
		component = mount(IndexStatusBanner, { target });
		flushSync();
		// The immediate first GET on mount.
		await vi.advanceTimersByTimeAsync(0);
		expect(indexCallCount()).toBe(1);

		// Each tick: one GET, then a 1 s delay — the response write must
		// not restart the loop (that collapsed the cadence to one GET per
		// network round-trip).
		await vi.advanceTimersByTimeAsync(3000);
		expect(indexCallCount()).toBe(4);
	});

	it('switches to a 10 s cadence once the index reports disabled', async () => {
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(indexCallCount()).toBe(1);

		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexFixture(false) : {}
		);
		// The 1 s tick (enabled) observes disabled...
		await vi.advanceTimersByTimeAsync(1000);
		expect(indexCallCount()).toBe(2);
		// ...and the next tick is 10 s out: 5 s passes with no request.
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(2);
		// ...and the 10 s tick fires.
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(3);
	});

	it('keeps polling through a transient read failure', async () => {
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(indexCallCount()).toBe(1);

		getMock.mockImplementationOnce(async () => {
			throw new Error('network blip');
		});
		await vi.advanceTimersByTimeAsync(1000);
		// The failed tick issued a request; the loop survived to the next
		// 1 s tick.
		expect(indexCallCount()).toBe(2);
		await vi.advanceTimersByTimeAsync(1000);
		expect(indexCallCount()).toBe(3);
	});

	it('stays hidden while queued (the new-count is not known yet)', async () => {
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? statusFixture({ status: 'queued' }) : {}
		);
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(target.querySelector('.index-banner')).toBeNull();
	});

	it('shows while running with new sources to add', async () => {
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? statusFixture({ status: 'running', added: 3, total: 3 }) : {}
		);
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		const banner = target.querySelector('.index-banner');
		expect(banner).not.toBeNull();
		expect(banner?.textContent).toContain('Adding 3 new sources');
	});

	it('stays hidden for a running scan that found nothing new', async () => {
		// The no-op rescan the 60 s staleness check triggers on every visit:
		// the walk ran, classify counted zero new paths, extraction ticks
		// over the unchanged set — the banner must not show.
		getMock.mockImplementation(async (path: string) =>
			path === '/index'
				? statusFixture({ status: 'running', added: 0, total: 5, extracted: 2 })
				: {}
		);
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(target.querySelector('.index-banner')).toBeNull();
	});

	it('stays hidden once the scan reaches a terminal', async () => {
		getMock.mockImplementation(async (path: string) =>
			path === '/index'
				? statusFixture({ status: 'done', last_run_at: '2026-11-10T12:00:00Z' })
				: {}
		);
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(target.querySelector('.index-banner')).toBeNull();
	});

	it('stops polling after unmount', async () => {
		component = mount(IndexStatusBanner, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(0);
		expect(indexCallCount()).toBe(1);

		unmount(component);
		target.remove();
		await vi.advanceTimersByTimeAsync(30000);
		expect(indexCallCount()).toBe(1);
	});
});
