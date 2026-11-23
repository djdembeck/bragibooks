/**
 * Batch rollup for the queue page's completion banner.
 *
 * A batch is one queue request: all of its jobs share `batch_id`. The
 * banner's subject is the LATEST request — the one owning the newest merge
 * row — and its run is every job carrying that batch id, wherever those
 * rows sit: two concurrent requests interleave in `queue_order` (A,B,A),
 * so adjacency alone would split a request into fragments. Requests
 * enqueued back to back are contiguous in `queue_order` (queue_seq is
 * max+1 per insert, completed rows are never pruned), which is why the id
 * is the only separator. Rows without a batch id (pre-batch history) fall
 * back to the old rule: a run breaks at a queue-order gap, and interleaved
 * audit rows (imports, metadata saves) break it. An older finished batch
 * is superseded once a newer run exists. A run with no terminal job yet
 * reports an in-flight rollup, not completion.
 *
 * `queue_order` is the durable enqueue counter (SQLite `created_at` is only
 * second-precision, so it cannot order a batch); pre-dispatcher rows carry
 * `rowid`, which is also monotonic. Timestamps are naive-UTC strings, so
 * lexicographic comparison is chronological.
 */

export interface BatchJob {
	id: string;
	status: string;
	/** True while a local cancel is in flight and the backend row is not
	 *  terminal yet: the status is optimistic and must not count as a
	 *  settled outcome. */
	cancelRequested?: boolean;
	/** Null actions predate the column; those rows were merges. */
	action: string | null;
	/** The queue request that created the job; every job of one request
	 *  shares it. Null for pre-batch rows, which group by queue-order
	 *  adjacency instead. */
	batchId?: string | null;
	queueOrder: number;
	startedAt: string | null;
	completedAt: string | null;
	createdAt: string | null;
}

export type BatchTone = 'success' | 'warning' | 'error' | 'info';

export interface BatchSummary {
	/** Stable identity for dismissal: the run's last queue order + size. */
	id: string;
	/** True when the run holds two or more jobs (a batch). */
	isBatch: boolean;
	jobIds: string[];
	total: number;
	done: number;
	failed: number;
	cancelled: number;
	inFlight: number;
	allTerminal: boolean;
	tone: BatchTone;
	/** Earliest started_at (created_at fallback) across the run. */
	startedAt: string | null;
	/** Latest completed_at among finished jobs; null until one finishes.
	 *  The wall clock is only printed for a fully terminal run. */
	finishedAt: string | null;
}

function buildSummary(run: BatchJob[]): BatchSummary {
	let done = 0;
	let failed = 0;
	let cancelled = 0;
	let inFlight = 0;
	let startedAt: string | null = null;
	let finishedAt: string | null = null;
	for (const job of run) {
		const status = job.status.toLowerCase();
		if (status === 'done') {
			done += 1;
		} else if (status === 'error' || status === 'failed') {
			failed += 1;
		} else if (status === 'cancelled' && !job.cancelRequested) {
			cancelled += 1;
		} else {
			inFlight += 1;
		}
		const started = job.startedAt ?? job.createdAt;
		if (started && (!startedAt || started < startedAt)) startedAt = started;
		if (job.completedAt && (!finishedAt || job.completedAt > finishedAt)) {
			finishedAt = job.completedAt;
		}
	}
	const allTerminal = inFlight === 0;
	let tone: BatchTone;
	if (inFlight > 0) {
		// Failures are terminal, so a partial failure is already a fact.
		tone = failed > 0 ? 'warning' : 'info';
	} else if (failed === run.length) {
		tone = 'error';
	} else if (failed > 0) {
		tone = 'warning';
	} else if (done > 0) {
		tone = 'success';
	} else {
		// Every job stopped by the operator.
		tone = 'info';
	}
	const last = run[run.length - 1];
	return {
		id: `${last.queueOrder}:${run.length}`,
		isBatch: run.length > 1,
		jobIds: run.map((job) => job.id),
		total: run.length,
		done,
		failed,
		cancelled,
		inFlight,
		allTerminal,
		tone,
		startedAt,
		finishedAt,
	};
}

/** The latest merge run and its rollup, or null when no merge jobs exist. */
export function summarizeLatestBatch(jobs: BatchJob[]): BatchSummary | null {
	if (jobs.length === 0) return null;

	// Audit rows (imports, metadata saves) enqueue between batches; they are
	// excluded from the walk. Same convention as jobActionLabel: a missing
	// action means merge.
	const mergeJobs = [...jobs]
		.filter((job) => job.action === 'merge' || job.action == null)
		.sort((a, b) => a.queueOrder - b.queueOrder);
	if (mergeJobs.length === 0) return null;

	// The LATEST request is the one whose newest row sits at the end of the
	// sorted list (queue_seq is the durable enqueue counter). Every job of
	// that request shares its batchId, so the run is ALL of that request's
	// rows, wherever they sit: two concurrent requests interleave in
	// queue order (A,B,A), and adjacency alone would report the last A job
	// as a one-book run while the first A job is still running.
	const last = mergeJobs[mergeJobs.length - 1];
	let run: BatchJob[];
	if (last.batchId != null) {
		run = mergeJobs.filter((job) => job.batchId === last.batchId);
	} else {
		// Pre-batch history has no request identity: fall back to the
		// adjacency rule, walking back from the newest legacy row while
		// queue orders stay contiguous. A batched row (any id) breaks the
		// walk, as a gap does.
		run = [last];
		for (let i = mergeJobs.length - 2; i >= 0; i--) {
			const prev = mergeJobs[i];
			if (prev.batchId == null && prev.queueOrder === run[0].queueOrder - 1) {
				run.unshift(prev);
			} else {
				break;
			}
		}
	}
	return run.length > 0 ? buildSummary(run) : null;
}
