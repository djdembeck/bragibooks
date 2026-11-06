import { describe, expect, it } from 'vitest';
import {
	audiobookDbAddUrl,
	missingReleaseBody,
	missingReleaseInline,
	missingReleaseLinkLabel,
	missingReleaseProvenanceLabel,
	missingReleaseView,
	showEditorNudge,
	showBlockNudge,
	showPanelNudge
} from './missingRelease.js';

describe('missingReleaseView', () => {
	it('prefills one validated embedded ASIN and keeps its provenance', () => {
		const view = missingReleaseView({
			status: 'present',
			value: 'B0DNRN1H54',
			count: 1,
			source: 'embedded'
		});
		expect(view).toEqual({
			state: 'present',
			asin: 'B0DNRN1H54',
			count: 1,
			countCapped: false,
			provenance: 'embedded',
			addUrl: 'https://audiobookdb.org/add?asin=B0DNRN1H54'
		});
	});

	it('normalizes case and whitespace from filename evidence', () => {
		const view = missingReleaseView({
			status: 'present',
			value: ' b0dnrn1h54 ',
			source: 'filename'
		});
		expect(view.asin).toBe('B0DNRN1H54');
		expect(view.provenance).toBe('filename');
	});

	it('does not prefill a malformed value', () => {
		const view = missingReleaseView({ status: 'present', value: 'not-an-asin' });
		expect(view.state).toBe('unknown');
		expect(view.asin).toBeNull();
		expect(view.addUrl).toBe('https://audiobookdb.org/add');
	});

	it('does not prefill when the projection is internally ambiguous or capped', () => {
		expect(missingReleaseView({ status: 'present', value: 'B0DNRN1H54', count: 2 }).state).toBe(
			'unknown'
		);
		expect(
			missingReleaseView({
				status: 'present',
				value: 'B0DNRN1H54',
				count: 1,
				count_truncated: true
			}).state
		).toBe('unknown');
	});

	it('keeps an exact ambiguous count when the evidence was not capped', () => {
		const view = missingReleaseView({
			status: 'ambiguous',
			count: 3,
			count_truncated: false
		});
		expect(view.state).toBe('ambiguous');
		expect(view.count).toBe(3);
		expect(view.countCapped).toBe(false);
		expect(view.asin).toBeNull();
		expect(missingReleaseBody(view)).toContain('3 different ASINs');
	});

	it('says "multiple" instead of inventing a count when capped or unknown', () => {
		for (const view of [
			missingReleaseView({ status: 'ambiguous', count: 3, count_truncated: true }),
			missingReleaseView({ status: 'ambiguous', count: 0 })
		]) {
			expect(view.countCapped).toBe(true);
			expect(missingReleaseBody(view)).toContain('multiple ASINs');
			expect(missingReleaseBody(view)).not.toContain('3 different');
		}
	});

	it('distinguishes a complete missing read from unknown evidence', () => {
		const missing = missingReleaseView({ status: 'missing' });
		expect(missing.state).toBe('missing');
		expect(missingReleaseBody(missing)).toContain('No ASIN was found in this source.');

		const unknown = missingReleaseView(undefined);
		expect(unknown.state).toBe('unknown');
		expect(missingReleaseBody(unknown)).toContain('No source ASIN is available to prefill.');
		expect(missingReleaseBody(unknown)).not.toContain('No ASIN was found');
	});
});

describe('audiobookDbAddUrl', () => {
	it('opens the bare add page when no valid ASIN is available', () => {
		expect(audiobookDbAddUrl()).toBe('https://audiobookdb.org/add');
		expect(audiobookDbAddUrl('')).toBe('https://audiobookdb.org/add');
		expect(audiobookDbAddUrl('B0SHORT')).toBe('https://audiobookdb.org/add');
	});

	it('encodes a validated ASIN and normalizes case', () => {
		expect(audiobookDbAddUrl(' b0dnrn1h54 ')).toBe(
			'https://audiobookdb.org/add?asin=B0DNRN1H54'
		);
	});
});

describe('nudge copy', () => {
	it('keeps the batch action beside the existing state chip', () => {
		const copy = missingReleaseInline(missingReleaseView({ status: 'missing' }), 'batch');
		expect(copy.lead).toBe('Choose one in Review, or ');
		expect(copy.linkText).toBe('add it to AudiobookDB');
		expect(copy.tail).toBe('.');
	});

	it('uses the search context at a zero-result search', () => {
		const copy = missingReleaseInline(missingReleaseView({ status: 'missing' }), 'search');
		expect(copy.lead).toBe('Not finding it? ');
		expect(copy.linkText).toBe('Add to AudiobookDB');
		expect(copy.tail).toBe('.');
	});

	it('names the source, not an edition, in the wrong-match editor', () => {
		const present = missingReleaseInline(
			missingReleaseView({ status: 'present', value: 'B0DNRN1H54' }),
			'editor'
		);
		expect(present.linkText).toBe('Add to AudiobookDB');
		expect(present.linkText).not.toContain('edition');
		expect(present.tail).toBe('.');

		const view = missingReleaseView({ status: 'ambiguous', count: 2 });
		const copy = missingReleaseInline(view, 'editor');
		expect(copy.lead).toBe('Not the right book? ');
		expect(copy.linkText).toBe('Add to AudiobookDB');
		expect(copy.tail).toBe(' manually.');
	});

	it('names the ASIN and the new tab only when one exists', () => {
		const present = missingReleaseView({ status: 'present', value: 'B0DNRN1H54' });
		expect(missingReleaseLinkLabel(present, 'Add to AudiobookDB')).toBe(
			'Add to AudiobookDB with ASIN B0DNRN1H54 (opens in a new tab)'
		);
		expect(missingReleaseLinkLabel(missingReleaseView({ status: 'missing' }), 'add it to AudiobookDB')).toBe(
			'add it to AudiobookDB (opens in a new tab)'
		);
	});

	it('labels the ASIN provenance without inventing a source', () => {
		expect(missingReleaseProvenanceLabel('embedded')).toBe("from the file's tags");
		expect(missingReleaseProvenanceLabel('filename')).toBe('from the source name');
		expect(missingReleaseProvenanceLabel(null)).toBe('from the source');
	});
});

describe('nudge gates', () => {
	const settled = { loading: false, error: false, searching: false, searchError: false };
	const settledEditor = {
		selected: true,
		loading: false,
		error: false,
		releaseLoading: false,
		releaseError: false
	};

	it('hides the no-match block until the lookup is settled', () => {
		expect(showPanelNudge(settled)).toBe(true);
		expect(showPanelNudge({ ...settled, loading: true })).toBe(false);
		expect(showPanelNudge({ ...settled, error: true })).toBe(false);
		expect(showPanelNudge({ ...settled, searching: true })).toBe(false);
		expect(showPanelNudge({ ...settled, searchError: true })).toBe(false);
	});

	it('drops the block while the zero-result search repeat is on screen', () => {
		const settledBlock = { ...settled, searched: false, resultCount: 0 };
		// Before any search the block is the only handoff, so it stays.
		expect(showBlockNudge(settledBlock)).toBe(true);
		// A settled zero-result search renders the repeat under the search box;
		// the block must yield rather than show the same handoff twice.
		expect(showBlockNudge({ ...settledBlock, searched: true })).toBe(false);
		// Results mean a record exists to pick; the repeat is not rendered.
		expect(showBlockNudge({ ...settledBlock, searched: true, resultCount: 3 })).toBe(true);
		// The settled-lookup gate still holds while a new search runs.
		expect(showBlockNudge({ ...settledBlock, searched: true, resultCount: 3, searching: true })).toBe(false);
	});

	it('shows the wrong-match footer only for a loaded record', () => {
		expect(showEditorNudge(settledEditor)).toBe(true);
		expect(showEditorNudge({ ...settledEditor, selected: false })).toBe(false);
		expect(showEditorNudge({ ...settledEditor, loading: true })).toBe(false);
		expect(showEditorNudge({ ...settledEditor, error: true })).toBe(false);
		expect(showEditorNudge({ ...settledEditor, releaseLoading: true })).toBe(false);
		expect(showEditorNudge({ ...settledEditor, releaseError: true })).toBe(false);
	});
});
