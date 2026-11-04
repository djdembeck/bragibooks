import { describe, expect, it } from 'vitest';
import { libraryEmpty, nextAction, type NextActionCounts } from './nextAction';

const COUNTS: NextActionCounts = {
	errors: 0,
	pending: 0,
	matched: 0,
	processing: 0,
	queueRunning: false,
};

describe('nextAction', () => {
	const cases: Array<{ name: string; counts: NextActionCounts; expected: { href: string; label: string } }> = [
		{
			name: 'offers the failed books before any other work',
			counts: { ...COUNTS, errors: 3, pending: 2, matched: 1 },
			expected: { href: '/books?status=error', label: 'Review 3 errors' },
		},
		{
			name: 'keeps the singular label for one failed book',
			counts: { ...COUNTS, errors: 1, pending: 2 },
			expected: { href: '/books?status=error', label: 'Review 1 error' },
		},
		{
			name: 'offers matching over a ready batch and queued work',
			counts: { ...COUNTS, pending: 2, matched: 1, processing: 1, queueRunning: true },
			expected: { href: '/match', label: 'Match 2 books' },
		},
		{
			name: 'keeps the singular label for one book waiting',
			counts: { ...COUNTS, pending: 1 },
			expected: { href: '/match', label: 'Match 1 book' },
		},
		{
			name: 'names the ready batch when nothing is in flight',
			counts: { ...COUNTS, matched: 2 },
			expected: { href: '/process', label: 'Review 2 ready books' },
		},
		{
			name: 'keeps the singular label for one ready book',
			counts: { ...COUNTS, matched: 1 },
			expected: { href: '/process', label: 'Review 1 ready book' },
		},
		{
			name: 'falls back to the queue when a ready batch is in flight',
			counts: { ...COUNTS, matched: 2, processing: 1 },
			expected: { href: '/process', label: 'View queue' },
		},
		{
			name: 'falls back to the queue when a job is running or queued',
			counts: { ...COUNTS, matched: 1, queueRunning: true },
			expected: { href: '/process', label: 'View queue' },
		},
		{
			name: 'offers the queue for processing with nothing ready',
			counts: { ...COUNTS, processing: 3 },
			expected: { href: '/process', label: 'View queue' },
		},
		{
			name: 'offers the queue while idle work is queued',
			counts: { ...COUNTS, queueRunning: true },
			expected: { href: '/process', label: 'View queue' },
		},
		{
			name: 'falls back to import for an empty library',
			counts: COUNTS,
			expected: { href: '/import', label: 'Import books' },
		},
	];

	for (const c of cases) {
		it(c.name, () => {
			expect(nextAction(c.counts)).toEqual(c.expected);
		});
	}
});

describe('libraryEmpty', () => {
	const options = { hasLoaded: true, error: null, totalBooks: 0 };

	it('is not empty before the fetch settles', () => {
		expect(libraryEmpty({ ...options, hasLoaded: false })).toBe(false);
	});

	it('is not empty after a failed load, even at zero totals', () => {
		expect(libraryEmpty({ ...options, error: { message: 'boom', rateLimited: false, raw: 'boom' } })).toBe(
			false
		);
	});

	it('is empty once loaded with no error and no books', () => {
		expect(libraryEmpty(options)).toBe(true);
	});

	it('is not empty for any books', () => {
		expect(libraryEmpty({ ...options, totalBooks: 1 })).toBe(false);
	});
});
