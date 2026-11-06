/**
 * Partition a selection for the removal dialog: books owned by a queued or
 * running processing job are skipped (the server refuses such a delete with
 * 409), everything else is deletable.
 *
 * The in-flight set is derived from the ACTIVE JOB list, not from
 * books.status: the status only becomes 'processing' when a worker claims
 * the job, so a book whose job is still queued keeps its pre-queue status
 * and would slip through a status test.
 */
import type { BookWithPeople, ProcessingJob } from '#lib/types.js';

export interface RemovePartition {
	/** Deletable books, in the input order. */
	resolved: BookWithPeople[];
	/** Books skipped because a queued/running job owns them, in the input order. */
	skipped: BookWithPeople[];
}

/** The book ids owned by an active (queued or running) job; null book_ids are ignored. */
export function activeJobBookIds(jobs: ProcessingJob[] | null | undefined): Set<number> {
	return new Set(
		(jobs ?? [])
			.filter((j) => j.status === 'queued' || j.status === 'running')
			.map((j) => j.book_id)
			.filter((id): id is number => id != null)
	);
}

export function partitionForRemoval(
	books: BookWithPeople[],
	jobs: ProcessingJob[] | null | undefined
): RemovePartition {
	const inFlight = activeJobBookIds(jobs);
	const resolved: BookWithPeople[] = [];
	const skipped: BookWithPeople[] = [];
	for (const book of books) {
		if (inFlight.has(book.id)) skipped.push(book);
		else resolved.push(book);
	}
	return { resolved, skipped };
}
