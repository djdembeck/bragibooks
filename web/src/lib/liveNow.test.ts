import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import LiveNowProbe from './__tests__/LiveNowProbe.svelte';

// liveNow is a .svelte.ts rune module compiled for the client build by
// vitest, so it must run during real component initialisation. These tests
// mount LiveNowProbe, which wraps liveNow behind a $state gate (flipping
// the gate re-runs the effect) and exposes the resulting clock.

const BASE = 1_700_000_000_000;

describe('liveNow', () => {
	let target: HTMLElement;

	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(BASE);
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		target.remove();
		vi.useRealTimers();
	});

	it('ticks once per second while the gate is open', () => {
		const component = mount(LiveNowProbe, { target });
		flushSync();
		component.setGate(true);
		flushSync();
		const clock = component.getClock();
		expect(clock.now).toBe(BASE);

		vi.advanceTimersByTime(5000);
		expect(clock.now).toBe(BASE + 5000);

		unmount(component);
	});

	it('does not tick while the gate is closed', () => {
		const component = mount(LiveNowProbe, { target });
		flushSync();
		const clock = component.getClock();
		expect(clock.now).toBe(BASE);

		vi.advanceTimersByTime(10_000);
		expect(clock.now).toBe(BASE);

		unmount(component);
	});

	it('starts ticking once the gate opens', () => {
		const component = mount(LiveNowProbe, { target });
		flushSync();
		const clock = component.getClock();
		expect(clock.now).toBe(BASE);

		component.setGate(true);
		flushSync();

		vi.advanceTimersByTime(3000);
		expect(clock.now).toBe(BASE + 3000);

		unmount(component);
	});
});
