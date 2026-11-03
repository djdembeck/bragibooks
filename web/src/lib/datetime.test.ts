import { describe, expect, it } from 'vitest';
import { elapsedLabel, formatElapsed, parseJobTimestamp } from './datetime';

// Naive-UTC source format (SQLite datetime('now')); offsets are derived from
// real epoch milliseconds so cases are independent of the viewer's time zone.
function at(epochMs: number): string {
	return new Date(epochMs).toISOString().replace('T', ' ').slice(0, 19);
}

const T0 = Date.parse('2026-10-20T10:00:00Z');
const MIN = 60_000;
const HOUR = 60 * MIN;

describe('parseJobTimestamp', () => {
	it('reads naive-UTC values as UTC in the local zone', () => {
		expect(parseJobTimestamp(at(T0))?.getTime()).toBe(T0);
	});

	it('returns null for empty or unparseable input', () => {
		expect(parseJobTimestamp('')).toBeNull();
		expect(parseJobTimestamp('   ')).toBeNull();
		expect(parseJobTimestamp('not a timestamp')).toBeNull();
	});
});

describe('formatElapsed', () => {
	const cases: [string | null, string | null, number, string][] = [
		// Closed durations (ended set): minute boundary shows seconds, hours drop them.
		[at(T0), `${at(T0)}.900`, 0, '0s'], // sub-second closed durations floor; Math.round would render 1s
		[at(T0), at(T0 + 12_000), 0, '12s'],
		[at(T0), at(T0 + 5 * MIN), 0, '5m 0s'],
		[at(T0), at(T0 + 5 * MIN + 27_000), 0, '5m 27s'],
		[at(T0), at(T0 + HOUR), 0, '1h 0m'],
		[at(T0), at(T0 + 27 * HOUR + 14 * MIN), 0, '27h 14m'],
		[at(T0), at(T0 - 3 * MIN), 0, '0s'], // negative delta clamps to zero
		// Live durations (ended null): second endpoint is nowMs, always seconds-bearing.
		[at(T0 + 40_000), null, T0, '0s'], // nowMs below start clamps to zero
		[at(T0), null, T0 + 900, '0s'], // sub-second renders 0s
		[at(T0), null, T0 + 12_000, '12s'],
		[at(T0), null, T0 + 4 * MIN + 27_000, '4m 27s'],
		[at(T0), null, T0 + 27 * HOUR + 14 * MIN + 9_000, '27h 14m 9s'],
		// Unparseable endpoints yield the em dash; the field never renders blank.
		[null, at(T0 + MIN), T0 + HOUR, '—'], // missing start
		['garbage', at(T0 + HOUR), 0, '—'], // malformed start
		[at(T0), 'garbage', T0 + HOUR, '—'], // malformed ended
	];

	for (const [since, ended, nowMs, expected] of cases) {
		it(`since=${String(since).slice(0, 19)} ended=${ended ?? 'now'} → ${expected}`, () => {
			expect(formatElapsed(since, ended, nowMs)).toBe(expected);
		});
	}
});

describe('elapsedLabel', () => {
	it('is Queued before a job starts, Elapsed after', () => {
		expect(elapsedLabel(null)).toBe('Queued');
		expect(elapsedLabel(undefined)).toBe('Queued');
		expect(elapsedLabel('2026-10-20 10:00:00')).toBe('Elapsed');
	});
});
