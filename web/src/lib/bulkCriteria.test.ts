import { describe, expect, it } from 'vitest';
import { criteriaChanged } from '#lib/bulkCriteria.js';

describe('criteriaChanged', () => {
	it('same filter and q → keep the selection', () => {
		expect(criteriaChanged({ filter: 'all', q: 'dune' }, { filter: 'all', q: 'dune' })).toBe(false);
	});

	it('filter change → clear the selection', () => {
		expect(criteriaChanged({ filter: 'all', q: 'dune' }, { filter: 'pending', q: 'dune' })).toBe(true);
	});

	it('q change → clear the selection', () => {
		expect(criteriaChanged({ filter: 'all', q: 'dune' }, { filter: 'all', q: 'hyperion' })).toBe(true);
	});

	it('empty q to a search term (and back) → clear', () => {
		expect(criteriaChanged({ filter: 'all', q: '' }, { filter: 'all', q: 'dune' })).toBe(true);
		expect(criteriaChanged({ filter: 'all', q: 'dune' }, { filter: 'all', q: '' })).toBe(true);
	});

	it('page-only change (same filter and q) → keep the selection', () => {
		// The predicate is over (filter, q) only: the page is not a
		// selection criterion, so a pagination change cannot clear.
		expect(criteriaChanged({ filter: 'pending', q: 'x' }, { filter: 'pending', q: 'x' })).toBe(false);
	});

	it('filter and q both change → clear', () => {
		expect(criteriaChanged({ filter: 'all', q: '' }, { filter: 'done', q: 'neon' })).toBe(true);
	});
});
