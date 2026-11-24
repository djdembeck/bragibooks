/**
 * Batch rollup for the queue page's completion banner.
 *
 * A batch is one queue request: all of its jobs share `batch_id`. The
 * banner's subject is the LATEST request, and the /jobs response carries a
 * bounded AGGREGATE of it (counts, wall clock, row ids, computed in SQL on
 * the server), so the banner stays whole for a batch of any size without
 * the page holding every batch row: `summarizeBatchAggregate` turns that
 * aggregate into the banner's rollup. The batch's full rows are fetched on
 * demand, bounded, via `/jobs?batch_id=ID&limit=&offset=` (the page's
 * "Show all" toggle).
 *
 * Rows without a batch id (pre-batch history, or the degraded probe) fall
 * back to the old rule: a run breaks at a queue-order gap, and
 * interleaved audit rows (imports, metadata saves) break it.
 * `summarizeLegacyRun` applies that walk to whatever rows the page holds.
 *
 * `queue_order` is the durable enqueue counter (SQLite `created_at` is only
 * second-precision, so it cannot order a batch); pre-dispatcher rows carry
 * `rowid`, which is also monotonic. Timestamps are naive-UTC strings, so
 * lexicographic comparison is chronological.
 */

export interface BatchAggregate {
	batch_id: string;
	total: number;
	done: number;
	failed: number;
	cancelled: number;
	in_flight: number;
	all_terminal: boolean;
	started_at?: string | null;
	finished_at?: string | null;
	/** The batch's newest queue order. */
	last_seq: number;
	/** The batch's row ids in queue order. */
	job_ids: string[];
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

function toneFor(total: number, done: number, failed: number, inFlight: number): BatchTone {
	if (inFlight > 0) {
		// Failures are terminal, so a partial failure is already a fact.
		return failed > 0 ? 'warning' : 'info';
	}
	if (total > 0 && done === 0 && failed === total) {
		return 'error';
	}
	if (failed > 0) {
		return 'warning';
	}
	if (done > 0) {
		return 'success';
	}
	// Every job stopped by the operator.
	return 'info';
}

/** The banner rollup from the /jobs response's batch aggregate, or the
 *  legacy queue-order adjacency rollup of the given rows when no
 *  aggregate is present (no batched row, or the probe degraded). The two
 *  paths never mix: a batched run's counts come from SQL, a legacy run's
 *  from the rows the page holds. */
export function summarizeRun(
	agg: BatchAggregate | null | undefined,
	jobs: LegacyJob[]
): BatchSummary | null {
	if (agg) return summarizeBatchAggregate(agg);
	return summarizeLegacyRun(jobs);
}

/** The banner rollup from the server's bounded batch aggregate: counts,
 *  wall clock, and the batch's row ids as-is (the server computed them
 *  over the whole batch, so nothing here truncates a large batch). The
 *  wall clock carries no in-flight optimism: the server's started_at is
 *  the batch's earliest started_at/created_at, and finished_at its latest
 *  completed_at, exactly the values the row walk used to derive. */
export function summarizeBatchAggregate(agg: BatchAggregate): BatchSummary {
	return {
		id: `${agg.last_seq}:${agg.total}`,
		isBatch: agg.total > 1,
		jobIds: agg.job_ids,
		total: agg.total,
		done: agg.done,
		failed: agg.failed,
		cancelled: agg.cancelled,
		inFlight: agg.in_flight,
		allTerminal: agg.all_terminal,
		tone: toneFor(agg.total, agg.done, agg.failed, agg.in_flight),
		startedAt: agg.started_at ?? null,
		finishedAt: agg.finished_at ?? null,
	};
}

/** A job row for the legacy (pre-batch) adjacency walk. */
export interface LegacyJob {
	id: string;
	status: string;
	/** Null actions predate the column; those rows were merges. */
	action: string | null;
	/** The queue request that created the job. A batched row (any id)
	 *  breaks the adjacency walk: the aggregate owns batched runs, so the
	 *  legacy walk only rolls up pre-batch rows. */
	batchId?: string | null;
	queueOrder: number;
	startedAt: string | null;
	completedAt: string | null;
	createdAt: string | null;
	/** Local cancellation in flight: the badge is optimistic 'cancelled'
	 *  until the backend's terminal row lands, so the run is still in
	 *  flight. */
	cancelRequested?: boolean;
}

/** The latest legacy run (queue-order adjacency) among the given rows, or
 *  null when no merge jobs exist. Pre-batch history carries no request
 *  identity, so a run breaks at a queue-order gap, and a batched row (any
 *  id) breaks it, as does an audit row. Used when the /jobs response
 *  carries no aggregate (no batched row, or the probe degraded). */
export function summarizeLegacyRun(jobs: LegacyJob[]): BatchSummary | null {
	if (jobs.length === 0) return null;

	// Audit rows (imports, metadata saves) enqueue between runs; they are
	// excluded from the walk. Same convention as jobActionLabel: a missing
	// action means merge.
	const mergeJobs = [...jobs]
		.filter((job) => job.action === 'merge' || job.action == null)
		.sort((a, b) => a.queueOrder - b.queueOrder);
	if (mergeJobs.length === 0) return null;

	// Walk back from the newest merge row while queue orders stay
	// contiguous. A batched row breaks the walk: its run is owned by the
	// aggregate (or, in the degraded case, is not the legacy run at all).
	const last = mergeJobs[mergeJobs.length - 1];
	const run: LegacyJob[] = [last];
	for (let i = mergeJobs.length - 2; i >= 0; i--) {
		const prev = mergeJobs[i];
		if (prev.batchId == null && prev.queueOrder === run[0].queueOrder - 1) {
			run.unshift(prev);
		} else {
			break;
		}
	}
	return buildLegacySummary(run);
}

function buildLegacySummary(run: LegacyJob[]): BatchSummary {
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
		} else if (status === 'cancelled') {
			if (job.cancelRequested) {
				// Optimistic badge: the worker has not persisted the
				// terminal row yet, so the run is still in flight.
				inFlight += 1;
			} else {
				cancelled += 1;
			}
		} else {
			inFlight += 1;
		}
		const started = job.startedAt ?? job.createdAt;
		if (started && (!startedAt || started < startedAt)) startedAt = started;
		if (job.completedAt && (!finishedAt || job.completedAt > finishedAt)) {
			finishedAt = job.completedAt;
		}
	}
	return {
		id: `${run[run.length - 1].queueOrder}:${run.length}`,
		isBatch: run.length > 1,
		jobIds: run.map((job) => job.id),
		total: run.length,
		done,
		failed,
		cancelled,
		inFlight,
		allTerminal: inFlight === 0,
		tone: toneFor(run.length, done, failed, inFlight),
		startedAt,
		finishedAt,
	};
}
