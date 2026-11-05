/**
 * Selection model for the books catalog: a "select all matching" baseline
 * (the server-capped ID list) plus row-level edits layered on top of it.
 *
 *   effective selection = (baseline − excluded) ∪ added
 *
 * The model owns no catalog state: counts are over the effective set only,
 * and the page derives everything else (bar visibility, the select-all
 * tick) from these values.
 */
export interface BookSelection {
	/** Baseline matching IDs from a select-all (server-capped); null = none. */
	readonly ids: Set<number> | null;
	/** Full match count at select-all time (uncapped). */
	readonly total: number;
	/** True when the baseline was truncated to the server cap (1,000). */
	readonly truncated: boolean;

	/** Record a successful select-all; the new baseline redefines the set, so row edits reset. */
	applyBaseline(ids: Set<number>, total: number, truncated: boolean): void;
	/** Drop only the baseline (a failed select-all keeps the row edits). */
	dropBaseline(): void;
	/** Drop the baseline and individual checks, keeping exclusions. */
	deselectAll(): void;
	/** Drop the row-level edits (exclusions and individual checks). */
	clearRowEdits(): void;
	/** Drop baseline + row edits + counters (the selection no longer exists). */
	clear(): void;

	isBookSelected(book: { id: number }): boolean;
	toggleBook(book: { id: number }): void;
	/** Count selected books across the WHOLE matching set, not just the visible page. */
	countSelected(): number;
	/** The book IDs a bulk operation sends — the effective selection. */
	effectiveSelectedIds(): number[];
	/** Drop one id the moment its operation succeeds, so bar counts and row washes track the live selection. */
	pruneSelectionId(id: number): void;
}

export function createBookSelection(): BookSelection {
	let allMatchedIds = $state<Set<number> | null>(null);
	let allMatchedTotal = $state(0);
	let allMatchedTruncated = $state(false);
	// Rows the operator unchecked after a select-all.
	let excludedIds = $state<Set<number>>(new Set());
	// Rows the operator checked individually (with no select-all baseline).
	let addedIds = $state<Set<number>>(new Set());

	function applyBaseline(ids: Set<number>, total: number, truncated: boolean) {
		allMatchedIds = ids;
		allMatchedTotal = total;
		allMatchedTruncated = truncated;
		// The new baseline redefines the set; individual edits no longer apply.
		excludedIds = new Set();
		addedIds = new Set();
	}

	function dropBaseline() {
		allMatchedIds = null;
	}

	function deselectAll() {
		allMatchedIds = null;
		allMatchedTotal = 0;
		allMatchedTruncated = false;
		addedIds = new Set();
	}

	function clearRowEdits() {
		excludedIds = new Set();
		addedIds = new Set();
	}

	function clear() {
		allMatchedIds = null;
		allMatchedTotal = 0;
		allMatchedTruncated = false;
		excludedIds = new Set();
		addedIds = new Set();
	}

	// Effective selection for a book: the baseline minus exclusions,
	// plus individually checked rows.
	function isBookSelected(book: { id: number }): boolean {
		if (allMatchedIds) {
			if (excludedIds.has(book.id)) return false;
			if (allMatchedIds.has(book.id)) return true;
			return addedIds.has(book.id);
		}
		return addedIds.has(book.id);
	}

	function toggleBook(book: { id: number }) {
		const next = new Set(addedIds);
		const nextExcluded = new Set(excludedIds);
		if (isBookSelected(book)) {
			if (allMatchedIds?.has(book.id)) {
				// Uncheck a baseline row: record the exclusion.
				nextExcluded.add(book.id);
				next.delete(book.id);
			} else {
				next.delete(book.id);
			}
		} else {
			if (allMatchedIds?.has(book.id)) {
				// Re-check a previously excluded baseline row.
				nextExcluded.delete(book.id);
			}
			next.add(book.id);
		}
		addedIds = next;
		excludedIds = nextExcluded;
	}

	function countSelected(): number {
		if (!allMatchedIds) return addedIds.size;
		const base = Math.max(0, allMatchedIds.size - excludedIds.size);
		// Added rows that are not in the baseline (a book matched by filter
		// but missing from the capped ID list, checked individually).
		let extra = 0;
		for (const id of addedIds) if (!allMatchedIds.has(id)) extra++;
		return base + extra;
	}

	function effectiveSelectedIds(): number[] {
		const ids = new Set<number>();
		if (allMatchedIds) {
			for (const id of allMatchedIds) if (!excludedIds.has(id)) ids.add(id);
		}
		for (const id of addedIds) ids.add(id);
		return [...ids];
	}

	function pruneSelectionId(id: number) {
		// allMatchedTotal counts the matches in the (capped) baseline list,
		// so it only decrements for rows that were in that list; a row checked
		// individually outside the baseline was never counted there.
		const wasInBaseline = allMatchedIds?.has(id) ?? false;
		if (allMatchedIds) {
			if (wasInBaseline) allMatchedIds = new Set(allMatchedIds);
			excludedIds = new Set(excludedIds);
		}
		if (allMatchedIds?.has(id)) allMatchedIds.delete(id);
		excludedIds.delete(id);
		addedIds = new Set(addedIds);
		addedIds.delete(id);
		if (wasInBaseline) allMatchedTotal = Math.max(0, allMatchedTotal - 1);
	}

	return {
		get ids() {
			return allMatchedIds;
		},
		get total() {
			return allMatchedTotal;
		},
		get truncated() {
			return allMatchedTruncated;
		},
		applyBaseline,
		dropBaseline,
		deselectAll,
		clearRowEdits,
		clear,
		isBookSelected,
		toggleBook,
		countSelected,
		effectiveSelectedIds,
		pruneSelectionId
	};
}
