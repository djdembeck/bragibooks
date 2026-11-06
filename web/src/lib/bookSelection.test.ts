import { describe, expect, it } from 'vitest';
import { createBookSelection, type BookSelection } from '#lib/bookSelection.svelte.js';

function book(id: number) {
	return { id };
}

/**
 * A fresh selection model. Every test gets its own instance, so the table
 * rows below can assume an empty model unless a row seeds one.
 */
function newSelection(): BookSelection {
	return createBookSelection();
}

// Seed helpers: one select-all with a known baseline, rows for the
// truncated-baseline cases (a row below and above the cap).
const CAP = 1000;
const baseline = (ids: number[]) => new Set(ids);

describe('book selection model', () => {
	describe('isBookSelected', () => {
		it('selects only individually checked rows without a baseline', () => {
			const s = newSelection();
			expect(s.isBookSelected(book(1))).toBe(false);
			s.toggleBook(book(1));
			expect(s.isBookSelected(book(1))).toBe(true);
			expect(s.isBookSelected(book(2))).toBe(false);
		});

		it.each([
			['a baseline row stays selected', 3, false, true],
			['an off-baseline row stays unselected', 8, false, false],
			['an excluded baseline row is unselected', 3, true, false],
			['an added off-baseline row is selected', 12, true, true],
			['an unchecked off-baseline row is unselected', 12, false, false]
		] as const)('%s', (_name, id, toggleFirst, expected) => {
			const s = newSelection();
			s.applyBaseline(baseline([3, 4, 9]), CAP, false);
			if (toggleFirst) s.toggleBook(book(id));
			expect(s.isBookSelected(book(id))).toBe(expected);
		});
	});

	describe('toggleBook', () => {
		it('checks a row with no baseline', () => {
			const s = newSelection();
			s.toggleBook(book(7));
			expect(s.effectiveSelectedIds()).toEqual([7]);
		});

		it('unchecking a baseline row records an exclusion', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), CAP, false);
			s.toggleBook(book(2));
			expect(s.isBookSelected(book(2))).toBe(false);
			expect(s.countSelected()).toBe(2);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 3]);
		});

		it('re-checking a previously excluded baseline row clears the exclusion', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), CAP, false);
			s.toggleBook(book(2));
			s.toggleBook(book(2));
			expect(s.isBookSelected(book(2))).toBe(true);
			expect(s.countSelected()).toBe(3);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 2, 3]);
		});

		it('checking an off-baseline row adds it alongside the baseline', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), CAP, false);
			s.toggleBook(book(99));
			expect(s.countSelected()).toBe(3);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 2, 99]);
		});

		it('unchecking an added off-baseline row removes it', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), CAP, false);
			s.toggleBook(book(99));
			s.toggleBook(book(99));
			expect(s.isBookSelected(book(99))).toBe(false);
			expect(s.countSelected()).toBe(2);
		});
	});

	describe('countSelected', () => {
		it('counts the baseline minus exclusions, plus off-baseline additions', () => {
			const s = newSelection();
			// Truncated baseline: 1,000 ids in the list, 1,042 total matches.
			s.applyBaseline(baseline(Array.from({ length: CAP }, (_, i) => i + 1)), 1042, true);
			expect(s.total).toBe(1042);
			expect(s.truncated).toBe(true);
			expect(s.countSelected()).toBe(CAP);
			s.toggleBook(book(2)); // exclude a baseline row
			expect(s.countSelected()).toBe(CAP - 1);
			s.toggleBook(book(2)); // re-check it
			expect(s.countSelected()).toBe(CAP);
			s.toggleBook(book(2000)); // a row matched by filter, missing from the capped list
			expect(s.countSelected()).toBe(CAP + 1);
		});

		it('is zero in a fresh model and after clear', () => {
			const s = newSelection();
			expect(s.countSelected()).toBe(0);
			s.toggleBook(book(1));
			s.applyBaseline(baseline([1, 2]), 2, false);
			expect(s.countSelected()).toBe(2);
			s.clear();
			expect(s.countSelected()).toBe(0);
			expect(s.ids).toBeNull();
			expect(s.total).toBe(0);
			expect(s.truncated).toBe(false);
		});
	});

	describe('effectiveSelectedIds', () => {
		it('merges baseline-minus-exclusions with added rows, without duplicates', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), CAP, false);
			s.toggleBook(book(2)); // excluded
			s.toggleBook(book(3)); // exclude a baseline row…
			s.toggleBook(book(3)); // …then re-check it: it lands in added, exclusion cleared
			s.toggleBook(book(4)); // off-baseline
			expect(s.effectiveSelectedIds().sort((a, b) => a - b)).toEqual([1, 3, 4]);
		});

		it('is empty when the baseline is fully excluded', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1]), 1, false);
			s.toggleBook(book(1));
			expect(s.effectiveSelectedIds()).toEqual([]);
		});
	});

	describe('pruneSelectionId', () => {
		it('removes a baseline id from the sets; the uncapped total is untouched', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), 1042, true);
			s.pruneSelectionId(2);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 3]);
			expect(s.total).toBe(1042); // uncapped server total; the post-op refetch supplies truth
		});

		it('leaves the uncapped total alone for an added-only id (never in the capped baseline)', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), 1002, true);
			s.toggleBook(book(2000)); // matched by filter, absent from the capped list
			s.pruneSelectionId(2000);
			expect(s.total).toBe(1002);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 2]);
		});

		it('clears a pruned id out of excludedIds, so a later re-check works', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), 2, false);
			s.toggleBook(book(2)); // excluded
			s.pruneSelectionId(2);
			expect(s.isBookSelected(book(2))).toBe(false);
			s.toggleBook(book(2)); // now a plain off-baseline add, not an exclusion fight
			expect(s.isBookSelected(book(2))).toBe(true);
		});
	});

	describe('baseline lifecycle', () => {
		it('a new baseline replaces the old one and drops the row edits', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1]), 1, false);
			s.toggleBook(book(9)); // off-baseline add
			s.applyBaseline(baseline([5, 6]), 6, true);
			expect(s.countSelected()).toBe(2);
			expect(s.effectiveSelectedIds().sort()).toEqual([5, 6]);
			expect(s.total).toBe(6);
			expect(s.truncated).toBe(true);
		});

		it('deselectAll drops the baseline and adds but keeps exclusions', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), 2, false);
			s.toggleBook(book(1)); // excluded
			s.toggleBook(book(9)); // off-baseline add
			s.deselectAll();
			expect(s.ids).toBeNull();
			expect(s.countSelected()).toBe(0);
			expect(s.total).toBe(0);
			expect(s.isBookSelected(book(1))).toBe(false); // the exclusion survived
			expect(s.isBookSelected(book(9))).toBe(false); // the add was dropped
		});

		it('clearRowEdits drops exclusions and adds but keeps the baseline', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), 3, false);
			s.toggleBook(book(2)); // excluded
			s.toggleBook(book(9)); // off-baseline add
			s.clearRowEdits();
			expect(s.ids).toEqual(new Set([1, 2, 3]));
			expect(s.countSelected()).toBe(3);
			expect(s.isBookSelected(book(2))).toBe(true); // exclusion dropped
			expect(s.isBookSelected(book(9))).toBe(false); // add dropped
			expect(s.total).toBe(3);
		});

		it('dropBaseline keeps the row edits and resets total/truncated (a failed select-all)', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2]), 1042, true);
			s.toggleBook(book(7)); // off-baseline add
			s.dropBaseline();
			expect(s.ids).toBeNull();
			expect(s.total).toBe(0);
			expect(s.truncated).toBe(false);
			expect(s.countSelected()).toBe(1);
			expect(s.effectiveSelectedIds()).toEqual([7]);
		});

		it('the ids getter hands out a copy, so mutation cannot bypass the model', () => {
			const s = newSelection();
			s.applyBaseline(baseline([1, 2, 3]), 3, false);
			s.ids?.add(99);
			expect(s.countSelected()).toBe(3);
			expect(s.effectiveSelectedIds().sort()).toEqual([1, 2, 3]);
		});
	});
});
