import type { RuntimeEvidence } from './runtimeComparison';

export type EvidenceField = 'title' | 'author' | 'narrator' | 'runtime' | 'date' | 'publisher' | 'language';

export interface FieldComparison {
	state: 'match' | 'close' | 'different' | 'unavailable' | 'not_comparable';
	/** Independently read source value, never copied from selected catalog metadata. */
	source: string | null;
	selected: string | null;
	provenance: string;
	reason: string;
	durations?: RuntimeEvidence;
}

export interface IdentifierEvidence {
	identifier: string;
	provenance: string;
	state: 'verified' | 'conflict' | 'unresolved' | 'not_found' | 'lookup_failed';
	scope: 'book' | 'edition';
	/** The actual resolved catalog relationship, not a text-search hit. */
	bookId?: string;
	releaseId?: string;
	reason: string;
}

/** Optional presentation input. No extraction or identifier resolution is performed here. */
export interface MatchEvidenceReport {
	sourceRead: { status: 'not_read' | 'read' | 'partial' | 'failed'; detail: string };
	selection: { bookId: string; releaseId: string | null };
	origin: 'title' | 'manual' | 'book_id' | 'edition_id';
	identifiers: IdentifierEvidence[];
	comparisons: Partial<Record<EvidenceField, FieldComparison>>;
}

export interface EvidenceSelection {
	bookId: string | null;
	releaseId: string | null;
	loadedBookId: string | null;
	loadedReleaseId: string | null;
	ready: boolean;
}

export function currentEvidence(
	report: MatchEvidenceReport | undefined,
	selection: EvidenceSelection
): MatchEvidenceReport | undefined {
	if (!report || !selection.ready || !selection.bookId) return undefined;
	return report.selection.bookId === selection.bookId &&
		report.selection.releaseId === selection.releaseId &&
		selection.loadedBookId === selection.bookId &&
		selection.loadedReleaseId === selection.releaseId
		? report
		: undefined;
}

export function evidenceComparison(report: MatchEvidenceReport | undefined, field: EvidenceField) {
	return report && (report.sourceRead.status === 'read' || report.sourceRead.status === 'partial')
		? report.comparisons[field]
		: undefined;
}

export function comparisonMark(comparison: FieldComparison | undefined, field: EvidenceField) {
	if (!comparison || !comparison.source?.trim() || !comparison.selected?.trim() || !comparison.provenance.trim()) return null;
	if (comparison.state === 'match') return 'match';
	if (comparison.state === 'different') return 'different';
	if (comparison.state === 'close' && field === 'runtime') return 'close';
	return null;
}

export function conflictingEvidenceFields(report: MatchEvidenceReport | undefined): EvidenceField[] {
	if (!report || (report.sourceRead.status !== 'read' && report.sourceRead.status !== 'partial')) return [];
	return (Object.entries(report.comparisons) as [EvidenceField, FieldComparison][])
		.filter(([field, comparison]) => comparisonMark(comparison, field) === 'different')
		.map(([field]) => field);
}

export function evidenceBadge(report: MatchEvidenceReport | undefined, manual: boolean): {
	label: string;
	kind: 'neutral' | 'verified' | 'conflict';
	identityScope?: 'book' | 'edition';
} {
	const fallback = { label: manual ? 'Chosen manually' : 'Title suggestion', kind: 'neutral' as const };
	if (!report || report.sourceRead.status === 'not_read' || report.sourceRead.status === 'failed') return fallback;
	if (report.identifiers.some((id) => id.state === 'conflict') ||
		Object.entries(report.comparisons).some(([field, comparison]) => comparisonMark(comparison, field as EvidenceField) === 'different')) {
		return { label: 'Conflicting evidence', kind: 'conflict' };
	}
	const allResolved = report.identifiers.every((id) => id.state === 'verified');
	const verified = report.identifiers.filter((id) => id.state === 'verified' && id.identifier?.trim() &&
		id.provenance?.trim() && id.bookId === report.selection.bookId);
	if (report.sourceRead.status === 'read' && allResolved && report.origin === 'edition_id' && report.selection.releaseId &&
		verified.some((id) => id.scope === 'edition' && id.releaseId === report.selection.releaseId)) {
		return { label: 'Exact edition ID', kind: 'verified', identityScope: 'edition' };
	}
	if (allResolved && report.origin === 'book_id' && verified.some((id) => id.scope === 'book')) {
		return { label: 'Exact book ID · edition unverified', kind: 'neutral', identityScope: 'book' };
	}
	return fallback;
}

export const evidenceFieldLabels: Record<EvidenceField, string> = {
	title: 'Title', author: 'Author', narrator: 'Narrator', runtime: 'Runtime',
	date: 'Release date', publisher: 'Publisher', language: 'Language'
};

export const comparisonLabels: Record<FieldComparison['state'], string> = {
	match: 'Matches source', close: 'Runtime close', different: 'Differs from source',
	unavailable: 'Not available', not_comparable: 'Not comparable'
};

export const identifierLabels: Record<IdentifierEvidence['state'], string> = {
	verified: 'ID verified', conflict: 'ID conflict', unresolved: 'ID unresolved',
	not_found: 'ID not found', lookup_failed: 'ID lookup failed'
};
