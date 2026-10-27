import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// liveNow is a .svelte.ts rune module. The Svelte compiler is deliberately
// kept out of this test: the gating and interval cadence are a pure function
// of the active() callback, Date.now(), and window, so the test installs
// minimal plain-JS stand-ins for the two runes — `$state` as an identity
// holder and `$effect` a synchronous runner that records each effect's
// body and teardown — and loads the module as plain TypeScript.
const g = globalThis as Record<string, unknown>;
let bodies: Array<() => void>;
let cleanups: Array<() => void>;
let intervals: Array<{ fn: () => void; ms: number; id: unknown }>;
let cleared: unknown[];

beforeEach(() => {
	bodies = [];
	cleanups = [];
	intervals = [];
	cleared = [];
	g.$state = (value: unknown) => value;
	g.$effect = (fn: () => void) => {
		bodies.push(fn);
		const teardown = fn();
		if (typeof teardown === 'function') cleanups.push(teardown);
	};
	// Delegate to the live globals so vi.useFakeTimers() intercepts the
	// timers and vi.advanceTimersByTime drives the tick; the counters keep
	// the bookkeeping independent of the timer engine.
	g.window = {
		setInterval: (fn: () => void, ms: number) => {
			const id = globalThis.setInterval(fn, ms);
			intervals.push({ fn, ms, id });
			return id;
		},
		clearInterval: (id: unknown) => {
			globalThis.clearInterval(id as NodeJS.Timeout);
			cleared.push(id);
		}
	};
});

afterEach(() => {
	delete g.$state;
	delete g.$effect;
	delete g.window;
	vi.useRealTimers();
});

const { liveNow } = await import('./liveNow.svelte');

const BASE = 1_700_000_000_000;

// Svelte re-runs the effect when tracked state changes; driving the
// recorded teardown and then the body by hand is the observable
// consequence of that re-run here.
function rerun() {
	cleanups.splice(0).forEach((teardown) => teardown());
	bodies.forEach((body) => body());
}

describe('liveNow', () => {
	it('ticks once per second while the gate is open', () => {
		vi.useFakeTimers();
		vi.setSystemTime(BASE);

		const clock = liveNow(() => true);
		expect(clock.now).toBe(BASE);
		expect(intervals).toHaveLength(1);
		expect(intervals[0].ms).toBe(1000);

		vi.advanceTimersByTime(5000);
		expect(clock.now).toBe(BASE + 5000);

		rerun();
		expect(cleared).toEqual([intervals[0].id]);
		// The re-run reschedules a fresh interval, so ticking continues.
		vi.advanceTimersByTime(1000);
		expect(clock.now).toBe(BASE + 6000);
	});

	it('starts no interval while the gate is closed', () => {
		vi.useFakeTimers();
		vi.setSystemTime(BASE);

		const clock = liveNow(() => false);
		expect(clock.now).toBe(BASE);
		expect(intervals).toHaveLength(0);

		vi.advanceTimersByTime(10_000);
		expect(clock.now).toBe(BASE);

		rerun();
		expect(cleared).toHaveLength(0);
	});

	it('starts the interval and ticks again once the gate opens', () => {
		vi.useFakeTimers();
		vi.setSystemTime(BASE);

		let active = false;
		const clock = liveNow(() => active);
		expect(clock.now).toBe(BASE);
		expect(intervals).toHaveLength(0);

		active = true;
		rerun();
		expect(intervals).toHaveLength(1);
		expect(intervals[0].ms).toBe(1000);

		vi.advanceTimersByTime(3000);
		expect(clock.now).toBe(BASE + 3000);
	});
});
