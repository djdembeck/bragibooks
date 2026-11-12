/**
 * Batch rollup for the queue page's completion banner.
 *
 * A batch is a maximal run of merge jobs with consecutive `queue_order`
 * values: the queue endpoint inserts one row per book in a single request,
 * so a batch is contiguous, and interleaved audit rows (imports, metadata
 * saves) break the run. The LATEST merge run is always the banner's subject
 * — an older finished batch is superseded once a newer run exists. A run
 * with no terminal job yet reports an in-flight rollup, not completion.
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
	// excluded from the walk, and adjacency is checked on merge queue orders
	// so an interleaved row still breaks the run.
	// Same convention as jobActionLabel: a missing action means merge.
	const mergeJobs = [...jobs]
		.filter((job) => job.action === 'merge' || job.action == null)
		.sort((a, b) => a.queueOrder - b.queueOrder);
	let run: BatchJob[] = [];
	for (const job of mergeJobs) {
		if (run.length > 0 && job.queueOrder === run[run.length - 1].queueOrder + 1) {
			run.push(job);
		} else {
			run = [job];
		}
	}
	return run.length > 0 ? buildSummary(run) : null;
}
