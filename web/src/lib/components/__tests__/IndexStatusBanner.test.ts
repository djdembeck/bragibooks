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

function indexFixture(enabled: boolean): Record<string, unknown> {
	return {
		enabled,
		state: {
			root: '/in',
			entries: 1,
			last_run_at: '',
			status: 'done',
			detail: '',
			generation: 1
		}
	};
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
