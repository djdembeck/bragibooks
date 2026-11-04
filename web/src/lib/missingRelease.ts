import type { SourceASINEvidence } from './types.js';

const ASIN_PATTERN = /^B[A-Z0-9]{9}$/;
export const AUDIOBOOKDB_ADD_URL = 'https://audiobookdb.org/add';

export type MissingReleaseState = 'present' | 'ambiguous' | 'missing' | 'unknown';
export type MissingReleaseContext = 'batch' | 'editor' | 'search';

export interface MissingReleaseView {
	state: MissingReleaseState;
	asin: string | null;
	count: number;
	countCapped: boolean;
	provenance: 'embedded' | 'filename' | null;
	addUrl: string;
}

export interface MissingReleaseInline {
	lead: string;
	linkText: string;
	tail: string;
	noun: 'audiobook' | 'edition';
}

/** Normalize the API projection into the four states the nudge can speak to.
 *  A unique value is prefilled only when the backend marked the extraction
 *  complete and unambiguous; anything else degrades to the generic add link. */
export function missingReleaseView(
	evidence: SourceASINEvidence | null | undefined
): MissingReleaseView {
	const rawValue = typeof evidence?.value === 'string' ? evidence.value.trim().toUpperCase() : '';
	const rawCount = typeof evidence?.count === 'number' ? evidence.count : 0;
	const capped = evidence?.count_truncated === true;

	if (
		evidence?.status === 'present' &&
		ASIN_PATTERN.test(rawValue) &&
		rawCount <= 1 &&
		!capped
	) {
		const provenance =
			evidence.source === 'embedded' || evidence.source === 'filename'
				? evidence.source
				: null;
		return {
			state: 'present',
			asin: rawValue,
			count: 1,
			countCapped: false,
			provenance,
			addUrl: audiobookDbAddUrl(rawValue)
		};
	}

	if (evidence?.status === 'ambiguous') {
		const count = Number.isInteger(rawCount) && rawCount > 0 ? rawCount : 0;
		return {
			state: 'ambiguous',
			asin: null,
			count,
			countCapped: capped || count < 2,
			provenance: null,
			addUrl: AUDIOBOOKDB_ADD_URL
		};
	}

	if (evidence?.status === 'missing') {
		return {
			state: 'missing',
			asin: null,
			count: 0,
			countCapped: false,
			provenance: null,
			addUrl: AUDIOBOOKDB_ADD_URL
		};
	}

	return {
		state: 'unknown',
		asin: null,
		count: 0,
		countCapped: false,
		provenance: null,
		addUrl: AUDIOBOOKDB_ADD_URL
	};
}

export function audiobookDbAddUrl(asin?: string | null): string {
	const normalized = (asin ?? '').trim().toUpperCase();
	if (!ASIN_PATTERN.test(normalized)) return AUDIOBOOKDB_ADD_URL;
	return `${AUDIOBOOKDB_ADD_URL}?asin=${encodeURIComponent(normalized)}`;
}

export function missingReleaseBody(view: MissingReleaseView): string {
	if (view.state === 'present') {
		return "No record matched this source. If it isn't in the catalog, add it to AudiobookDB — the ASIN is filled in for you.";
	}
	if (view.state === 'missing') {
		return "No record matched this source. If it isn't in the catalog, you can add it manually. No ASIN was found in this source.";
	}
	if (view.state === 'ambiguous') {
		const count = view.countCapped || view.count < 2 ? 'multiple ASINs' : `${view.count} different ASINs`;
		return `No record matched this source. If it isn't in the catalog, you can add it manually. This source contains ${count}, so none was prefilled.`;
	}
	return "No record matched this source. If it isn't in the catalog, you can add it manually. No source ASIN is available to prefill.";
}

export function missingReleaseInline(
	view: MissingReleaseView,
	context: MissingReleaseContext
): MissingReleaseInline {
	if (context === 'batch') {
		return {
			lead: 'Choose one in Review, or ',
			linkText: 'add it to AudiobookDB',
			tail: '.',
			noun: 'audiobook'
		};
	}
	if (context === 'editor') {
		return {
			lead: 'Not the right book? ',
			linkText: 'Add to AudiobookDB',
			tail: view.state === 'present' ? '.' : ' manually.',
			noun: 'audiobook'
		};
	}
	return {
		lead: 'Not finding it? ',
		linkText: 'Add to AudiobookDB',
		tail: '.',
		noun: 'audiobook'
	};
}

export function missingReleaseLinkLabel(
	view: MissingReleaseView,
	noun: 'audiobook' | 'edition'
): string {
	if (view.state === 'present' && view.asin) {
		return `Add this ${noun} to AudiobookDB with ASIN ${view.asin} (opens in a new tab)`;
	}
	return `Add this ${noun} to AudiobookDB (opens in a new tab)`;
}

export function missingReleaseProvenanceLabel(
	provenance: MissingReleaseView['provenance']
): string {
	if (provenance === 'embedded') return "from the file's tags";
	if (provenance === 'filename') return 'from the source name';
	return 'from the source';
}

export const MISSING_RELEASE_HELPER =
	"Opens in a new tab. Sign in or create an account; entries may be reviewed. Once it's in the catalog, search again.";

/** The focused no-match block waits for a settled lookup: neither an in-flight
 *  search nor a failed one can stand in for a completed empty result. */
export function showPanelNudge(state: {
	loading: boolean;
	error: boolean;
	searching: boolean;
	searchError: boolean;
}): boolean {
	return !state.loading && !state.error && !state.searching && !state.searchError;
}

/** The wrong-match footer belongs to a loaded record and disappears while its
 *  catalog record or edition is changing. Search state is deliberately not
 *  part of this gate: the footer is the operator's wrong-match escape, not a
 *  claim that the catalog lacks the book. */
export function showEditorNudge(state: {
	selected: boolean;
	loading: boolean;
	error: boolean;
	releaseLoading: boolean;
	releaseError: boolean;
}): boolean {
	return (
		state.selected &&
		!state.loading &&
		!state.error &&
		!state.releaseLoading &&
		!state.releaseError
	);
}
