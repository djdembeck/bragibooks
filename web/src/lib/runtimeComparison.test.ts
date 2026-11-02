import { describe, expect, it } from 'vitest';
import { compareRuntimeDurations, type DurationReading, type RuntimeEvidence } from './runtimeComparison';

const exact = (minutes: number): DurationReading => ({ milliseconds: minutes * 60_000, precision: 'exact' });
const rounded = (minutes: number): DurationReading => ({ milliseconds: minutes * 60_000, precision: 'minute-rounded' });
const compare = (source: DurationReading, catalog: DurationReading) =>
	compareRuntimeDurations({ source, catalog });

describe('compareRuntimeDurations', () => {
	it('reports exact source-longer and catalog-longer differences symmetrically', () => {
		expect(compare(exact(667), exact(666))).toMatchObject({
			direction: 'source-longer', lowerMinutes: 1, upperMinutes: 1, exactWholeMinutes: true,
			underMinute: false, severity: 'small'
		});
		expect(compare(exact(666), exact(667))).toMatchObject({
			direction: 'catalog-longer', lowerMinutes: 1, upperMinutes: 1, exactWholeMinutes: true,
			underMinute: false, severity: 'small'
		});
	});

	it('reports exact equality as zero neutral difference', () => {
		expect(compare(exact(666), exact(666))).toMatchObject({
			direction: 'equal', lowerMinutes: 0, upperMinutes: 0, exactWholeMinutes: true,
			underMinute: false, severity: 'neutral'
		});
	});

	it('keeps a 40-second exact gap under one minute despite minute-display rounding', () => {
		const result = compare(
			{ milliseconds: 666 * 60_000 + 40_000, precision: 'exact' },
			exact(666)
		);
		expect(result).toMatchObject({
			direction: 'source-longer', lowerMinutes: 0, upperMinutes: 1, exactWholeMinutes: false,
			underMinute: true, severity: 'neutral', sourceLabel: '11h 7m', catalogLabel: '11h 6m',
			sourceRounded: true, catalogRounded: false
		});
		expect(result?.sourceLabel).not.toMatch(/\bsec(?:ond)?s?\b/i);
		expect(result?.catalogLabel).not.toMatch(/\bsec(?:ond)?s?\b/i);
	});

	it('rounds fractional exact difference bounds outward', () => {
		const result = compare(
			{ milliseconds: 10 * 60_000 + 30_000, precision: 'exact' },
			exact(6)
		);
		expect(result).toMatchObject({ direction: 'source-longer', lowerMinutes: 4, upperMinutes: 5, severity: 'small' });
	});

	it('marks rounded overlap and zero-touch as uncertain', () => {
		expect(compare(rounded(666), rounded(666))).toMatchObject({
			direction: 'uncertain', lowerMinutes: 0, severity: 'neutral'
		});
		expect(compare(rounded(667), rounded(666))).toMatchObject({
			direction: 'uncertain', lowerMinutes: 0, severity: 'neutral'
		});
		expect(compare(rounded(666), { milliseconds: 666 * 60_000 + 30_000, precision: 'exact' })).toMatchObject({
			direction: 'uncertain', lowerMinutes: 0, severity: 'neutral'
		});
	});

	it('uses conservative outward intervals and minimum-gap severity for rounded nonoverlap', () => {
		expect(compare(rounded(676), rounded(666))).toMatchObject({
			direction: 'source-longer', lowerMinutes: 9, upperMinutes: 11, severity: 'noticeable'
		});
		expect(compare(rounded(666), rounded(676))).toMatchObject({
			direction: 'catalog-longer', lowerMinutes: 9, upperMinutes: 11, severity: 'noticeable'
		});
	});

	it('supports mixed exact and rounded evidence without overstating the gap', () => {
		expect(compare(exact(676), rounded(666))).toMatchObject({
			direction: 'source-longer', lowerMinutes: 9, upperMinutes: 11, severity: 'noticeable'
		});
		expect(compare(exact(666), rounded(676))).toMatchObject({
			direction: 'catalog-longer', lowerMinutes: 9, upperMinutes: 11, severity: 'noticeable'
		});
	});

	it('marks positive durations below half a minute as Under 1 min', () => {
		const result = compare(
			{ milliseconds: 20_000, precision: 'exact' },
			exact(1)
		);
		expect(result?.sourceLabel).toBe('Under 1 min');
		expect(result?.sourceRounded).toBe(true);
	});

	it.each([
		[3 * 60_000, 'small'],
		[5 * 60_000 - 1, 'small'], [5 * 60_000, 'noticeable'],
		[10 * 60_000 - 1, 'noticeable'], [10 * 60_000, 'moderate'],
		[30 * 60_000 - 1, 'moderate'], [30 * 60_000, 'large'],
		[60 * 60_000 - 1, 'large'], [60 * 60_000, 'very-large']
	] as const)('uses conservative severity for an exact %i-millisecond difference', (differenceMs, severity) => {
		expect(compare(
			{ milliseconds: 100 * 60_000 + differenceMs, precision: 'exact' }, exact(100)
		)?.severity).toBe(severity);
	});

	it('rejects absent data, invalid measurements, and unknown precision', () => {
		expect(compareRuntimeDurations(undefined)).toBeNull();
		const invalid: RuntimeEvidence[] = [
			{ source: exact(1), catalog: { milliseconds: 0, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: -1, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: Number.NaN, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: Number.POSITIVE_INFINITY, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: 60_000.5, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: Number.MAX_SAFE_INTEGER, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: Number.MAX_SAFE_INTEGER - 60_000 + 1, precision: 'exact' } },
			{ source: exact(1), catalog: { milliseconds: 60_001, precision: 'minute-rounded' } },
			{ source: exact(1), catalog: { milliseconds: 60_000, precision: 'approximate' } as unknown as DurationReading },
			{ source: exact(1), catalog: { milliseconds: 60_000 } as DurationReading }
		];
		for (const evidence of invalid) expect(compareRuntimeDurations(evidence)).toBeNull();
	});
});
