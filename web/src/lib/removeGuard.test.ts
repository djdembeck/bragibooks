import { describe, expect, it } from 'vitest';
import { activeJobBookIds, partitionForRemoval } from '#lib/removeGuard.js';
import type { BookWithPeople, ProcessingJob } from '#lib/types.js';

function book(id: number): BookWithPeople {
	return { id, title: `Book ${id}` } as BookWithPeople;
}

function job(bookId: number | null, status: ProcessingJob['status']): ProcessingJob {
	return { id: `job-${bookId}-${status}`, book_id: bookId, status, action: 'import' } as ProcessingJob;
}

describe('partitionForRemoval', () => {
	const books = [book(1), book(2), book(3), book(4), book(5)];

	it('skips a book whose job is still queued (the status-based filter would miss it)', () => {
		const { resolved, skipped } = partitionForRemoval(books, [job(3, 'queued')]);
		expect(skipped.map((b) => b.id)).toEqual([3]);
		expect(resolved.map((b) => b.id)).toEqual([1, 2, 4, 5]);
	});

	it('skips a book whose job is running', () => {
		const { resolved, skipped } = partitionForRemoval(books, [job(2, 'running')]);
		expect(skipped.map((b) => b.id)).toEqual([2]);
		expect(resolved.map((b) => b.id)).toEqual([1, 3, 4, 5]);
	});

	it('keeps a book whose job is terminal (done)', () => {
		const { resolved, skipped } = partitionForRemoval(books, [job(1, 'done')]);
		expect(skipped).toEqual([]);
		expect(resolved.map((b) => b.id)).toEqual([1, 2, 3, 4, 5]);
	});

	it('keeps a book whose job is terminal (error and cancelled)', () => {
		for (const status of ['error', 'cancelled'] as const) {
			const { resolved, skipped } = partitionForRemoval(books, [job(4, status)]);
			expect(skipped, `status ${status}`).toEqual([]);
			expect(resolved.map((b) => b.id), `status ${status}`).toEqual([1, 2, 3, 4, 5]);
		}
	});

	it('ignores a job with a null book_id', () => {
		const { resolved, skipped } = partitionForRemoval(books, [job(null, 'running')]);
		expect(skipped).toEqual([]);
		expect(resolved.map((b) => b.id)).toEqual([1, 2, 3, 4, 5]);
	});

	it('keeps every book when the job list is empty', () => {
		const { resolved, skipped } = partitionForRemoval(books, []);
		expect(skipped).toEqual([]);
		expect(resolved.map((b) => b.id)).toEqual([1, 2, 3, 4, 5]);
	});

	it('keeps every book when the job list is null', () => {
		const { resolved, skipped } = partitionForRemoval(books, null);
		expect(skipped).toEqual([]);
		expect(resolved.map((b) => b.id)).toEqual([1, 2, 3, 4, 5]);
	});

	it('skips every book an active job owns and keeps the rest', () => {
		const { resolved, skipped } = partitionForRemoval(books, [job(1, 'queued'), job(5, 'running'), job(3, 'done')]);
		expect(skipped.map((b) => b.id)).toEqual([1, 5]);
		expect(resolved.map((b) => b.id)).toEqual([2, 3, 4]);
	});
});

describe('activeJobBookIds', () => {
	it('collects only queued/running book ids', () => {
		expect(
			activeJobBookIds([job(1, 'queued'), job(2, 'running'), job(3, 'done'), job(null, 'running')])
		).toEqual(new Set([1, 2]));
	});

	it('tolerates a missing list', () => {
		expect(activeJobBookIds(undefined)).toEqual(new Set());
	});
});
