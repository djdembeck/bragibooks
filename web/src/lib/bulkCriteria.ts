/**
 * The books catalog's selection baseline ("select all matching") is defined
 * by the filter + search term. A change to either redefines what "all
 * matching" means, so the selection is dropped; a page change moves within
 * the same matching set and keeps it.
 */
export interface CatalogCriteria {
	filter: string;
	q: string;
}

/** True when the selection-relevant criteria (filter, q) changed. */
export function criteriaChanged(prev: CatalogCriteria, next: CatalogCriteria): boolean {
	return prev.filter !== next.filter || prev.q !== next.q;
}
