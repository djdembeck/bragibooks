import { describe, expect, it } from 'vitest';
import {
	currentEvidence, evidenceBadge, evidenceComparison, comparisonMark, conflictingEvidenceFields,
	type EvidenceSelection, type MatchEvidenceReport, type FieldComparison
} from './matchEvidence';

function editionReport(): MatchEvidenceReport {
	return {
		sourceRead: { status: 'read', detail: 'Embedded tags read from all source files.' },
		selection: { bookId: 'book-a', releaseId: 'release-a' },
		origin: 'edition_id',
		identifiers: [{ identifier: 'release-a', provenance: 'Embedded release ID', state: 'verified', scope: 'edition', bookId: 'book-a', releaseId: 'release-a', reason: 'Resolved release belongs to this book.' }],
		comparisons: { narrator: { state: 'match', source: 'Source Reader', selected: 'Source Reader', provenance: 'Embedded narrator tag', reason: 'Same complete credit set.' } }
	};
}
const selection: EvidenceSelection = {
	bookId: 'book-a', releaseId: 'release-a', loadedBookId: 'book-a', loadedReleaseId: 'release-a', ready: true
};

describe('candidate-scoped source evidence', () => {
	it('does not invent evidence for existing records', () => {
		expect(currentEvidence(undefined, selection)).toBeUndefined();
		expect(evidenceBadge(undefined, false).kind).toBe('neutral');
		expect(evidenceBadge(undefined, true).kind).toBe('neutral');
		expect(evidenceComparison(undefined, 'title')).toBeUndefined();
	});

	it.each([
		{ ...selection, bookId: 'book-b' },
		{ ...selection, releaseId: 'release-b' },
		{ ...selection, loadedBookId: 'book-b' },
		{ ...selection, loadedReleaseId: 'release-b' },
		{ ...selection, ready: false }
	])('suppresses badge verification and marks while selection is stale or not loaded: %o', (changed) => {
		const report = currentEvidence(editionReport(), changed);
		expect(report).toBeUndefined();
		expect(evidenceBadge(report, true)).toEqual({ label: 'Chosen manually', kind: 'neutral' });
		expect(comparisonMark(evidenceComparison(report, 'narrator'), 'narrator')).toBeNull();
	});

	it('allows only a verified selected edition relationship to produce green', () => {
		const report = editionReport();
		expect(evidenceBadge(currentEvidence(report, selection), false)).toEqual({
			label: 'Exact edition ID', kind: 'verified', identityScope: 'edition'
		});
		report.identifiers[0].releaseId = 'release-b';
		expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
		report.identifiers[0].releaseId = 'release-a';
		report.identifiers[0].bookId = 'book-b';
		expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
	});

	it.each([
		{ field: 'identifier', value: '' },
		{ field: 'identifier', value: ' \t\n ' },
		{ field: 'identifier', value: undefined },
		{ field: 'provenance', value: '' },
		{ field: 'provenance', value: ' \t\n ' },
		{ field: 'provenance', value: undefined }
	])('uses neutral fallback for absent or blank $field ($value), even with verified relationships', ({ field, value }) => {
		for (const origin of ['book_id', 'edition_id'] as const) {
			const report = editionReport();
			report.origin = origin;
			report.identifiers[0].scope = origin === 'book_id' ? 'book' : 'edition';
			// Optional presentation inputs can arrive without a source value at runtime.
			Object.assign(report.identifiers[0], { [field]: value });
			expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
			expect(evidenceBadge(report, true)).toEqual({ label: 'Chosen manually', kind: 'neutral' });
		}
	});

	it('cannot verify an edition from partial extraction or a book-only selection', () => {
		const report = editionReport();
		report.sourceRead.status = 'partial';
		expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
		report.sourceRead.status = 'read';
		report.selection.releaseId = null;
		expect(evidenceBadge(report, false).kind).toBe('neutral');
		expect(currentEvidence(report, { ...selection, releaseId: null, loadedReleaseId: null })).toBe(report);
		expect(comparisonMark(evidenceComparison(report, 'narrator'), 'narrator')).toBe('match');
	});

	it('does not promote book identity to edition verification', () => {
		const report = editionReport();
		report.origin = 'book_id';
		report.identifiers[0].scope = 'book';
		expect(evidenceBadge(report, false)).toEqual({
			label: 'Exact book ID · edition unverified', kind: 'neutral', identityScope: 'book'
		});
		report.origin = 'edition_id';
		expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
	});

	it.each(['unresolved', 'not_found', 'lookup_failed'] as const)('keeps %s separate from conflict and blocks green', (state) => {
		const report = editionReport();
		report.identifiers.push({ ...report.identifiers[0], state });
		expect(evidenceBadge(report, false)).toEqual({ label: 'Title suggestion', kind: 'neutral' });
		expect(report.identifiers[1].state).toBe(state);
	});

	it('shows conflict rather than assurance when an identifier or comparable field disagrees', () => {
		const report = editionReport();
		report.identifiers[0].state = 'conflict';
		expect(evidenceBadge(report, false)).toEqual({ label: 'Conflicting evidence', kind: 'conflict' });
		report.identifiers[0].state = 'verified';
		report.comparisons.runtime = { state: 'not_comparable', source: null, selected: '2 hr', provenance: 'Embedded runtime tag', reason: 'Source runtime uses an unsupported format.' };
		report.comparisons.title = { state: 'different', source: 'Source title', selected: 'Other title', provenance: 'Embedded title tag', reason: 'Titles differ.' };
		expect(evidenceBadge(report, false)).toEqual({ label: 'Conflicting evidence', kind: 'conflict' });
		expect(conflictingEvidenceFields(report)).toEqual(['title']);
		report.sourceRead.status = 'failed';
		expect(conflictingEvidenceFields(report)).toEqual([]);
	});

	it.each(['not_read', 'failed'] as const)('never marks fields or verifies IDs when source status is %s', (status) => {
		const report = editionReport();
		report.sourceRead.status = status;
		expect(evidenceBadge(report, false).kind).toBe('neutral');
		expect(evidenceComparison(report, 'narrator')).toBeUndefined();
	});
});

describe('independent field evidence', () => {
	const comparison: FieldComparison = {
		state: 'match', source: 'Independent source title', selected: 'Independent source title',
		provenance: 'Embedded title tag', reason: 'Same title after safe normalization.'
	};
	it('distinguishes missing or noncomparable source data from actual disagreement', () => {
		expect(comparisonMark(comparison, 'title')).toBe('match');
		expect(comparisonMark({ ...comparison, state: 'different', selected: 'Other title' }, 'title')).toBe('different');
		for (const state of ['unavailable', 'not_comparable'] as const) {
			expect(comparisonMark({ ...comparison, state }, 'title')).toBeNull();
		}
		expect(comparisonMark({ ...comparison, source: null }, 'title')).toBeNull();
		expect(comparisonMark({ ...comparison, selected: null }, 'title')).toBeNull();
		expect(comparisonMark({ ...comparison, provenance: '' }, 'title')).toBeNull();
	});
	it('uses only a supplied runtime-close verdict without calculating a tolerance', () => {
		expect(comparisonMark({ ...comparison, state: 'close', source: '10h 1m', selected: '10h' }, 'runtime')).toBe('close');
		expect(comparisonMark({ ...comparison, state: 'close' }, 'title')).toBeNull();
	});
});
