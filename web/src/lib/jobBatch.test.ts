import { describe, expect, it } from 'vitest';
import {
	summarizeBatchAggregate,
	summarizeLegacyRun,
	summarizeRun,
	type BatchAggregate,
	type LegacyJob
} from './jobBatch';

function job(overrides: Partial<LegacyJob> & { id: string; queueOrder: number }): LegacyJob {
	return {
		status: 'done',
		action: 'merge',
		batchId: null,
		startedAt: '2026-07-01 12:00:00',
		completedAt: '2026-07-01 12:04:30',
		createdAt: '2026-07-01 11:59:00',
		...overrides
	};
}

function aggregate(overrides: Partial<BatchAggregate> & { batch_id: string; total: number }): BatchAggregate {
	return {
		done: overrides.total,
		failed: 0,
		cancelled: 0,
		in_flight: 0,
		all_terminal: true,
		started_at: '2026-07-01 12:00:00',
		finished_at: '2026-07-01 12:14:00',
		last_seq: overrides.total,
		job_ids: [],
		...overrides
	};
}

describe('summarizeBatchAggregate', () => {
	it('reports a single-job batch as a one-book run', () => {
		const summary = summarizeBatchAggregate(
			aggregate({ batch_id: 'req-1', total: 1, last_seq: 7, job_ids: ['m1'] })
		);
		expect(summary?.isBatch).toBe(false);
		expect(summary?.total).toBe(1);
		expect(summary?.done).toBe(1);
		expect(summary?.tone).toBe('success');
		expect(summary?.allTerminal).toBe(true);
		expect(summary?.startedAt).toBe('2026-07-01 12:00:00');
		expect(summary?.finishedAt).toBe('2026-07-01 12:14:00');
		expect(summary?.jobIds).toEqual(['m1']);
		// Dismissal identity: the batch's newest queue order + size.
		expect(summary?.id).toBe('7:1');
	});

	it('carries a large batch whole: counts and ids are not truncated', () => {
		const ids = Array.from({ length: 120 }, (_, i) => `b1-${String(i + 1).padStart(3, '0')}`);
		const summary = summarizeBatchAggregate(
			aggregate({ batch_id: 'b1', total: 120, done: 118, failed: 2, last_seq: 120, job_ids: ids })
		);
		expect(summary?.total).toBe(120);
		expect(summary?.done).toBe(118);
		expect(summary?.failed).toBe(2);
		expect(summary?.isBatch).toBe(true);
		expect(summary?.jobIds).toHaveLength(120);
		expect(summary?.id).toBe('120:120');
	});

	it('tints a partial failure warning and a full failure error', () => {
		const partial = summarizeBatchAggregate(aggregate({ batch_id: 'b1', total: 2, done: 1, failed: 1 }));
		expect(partial?.tone).toBe('warning');

		const full = summarizeBatchAggregate(aggregate({ batch_id: 'b1', total: 2, done: 0, failed: 2 }));
		expect(full?.tone).toBe('error');
	});

	it('reports an all-cancelled batch as stopped', () => {
		const summary = summarizeBatchAggregate(
			aggregate({ batch_id: 'b1', total: 2, done: 0, cancelled: 2 })
		);
		expect(summary?.tone).toBe('info');
		expect(summary?.cancelled).toBe(2);
	});

	it('flags in-flight batches and tints them by what has already failed', () => {
		const inFlight = summarizeBatchAggregate(
			aggregate({
				batch_id: 'b1',
				total: 2,
				done: 1,
				in_flight: 1,
				all_terminal: false,
				finished_at: null
			})
		);
		expect(inFlight?.allTerminal).toBe(false);
		expect(inFlight?.inFlight).toBe(1);
		expect(inFlight?.tone).toBe('info');
		// The wall clock is only printed for a fully terminal run.
		expect(inFlight?.finishedAt).toBeNull();

		const failing = summarizeBatchAggregate(
			aggregate({
				batch_id: 'b1',
				total: 2,
				done: 0,
				failed: 1,
				in_flight: 1,
				all_terminal: false
			})
		);
		expect(failing?.tone).toBe('warning');
	});
});

describe('summarizeLegacyRun (queue-order adjacency)', () => {
	it('returns null for an empty list and for audit-only activity', () => {
		expect(summarizeLegacyRun([])).toBeNull();
		expect(
			summarizeLegacyRun([
				job({ id: 'a1', queueOrder: 1, action: 'import' }),
				job({ id: 'a2', queueOrder: 2, action: 'metadata_update' })
			])
		).toBeNull();
	});

	it('reports a single finished merge job as success, not a batch', () => {
		const summary = summarizeLegacyRun([job({ id: 'm1', queueOrder: 1 })]);
		expect(summary?.isBatch).toBe(false);
		expect(summary?.total).toBe(1);
		expect(summary?.done).toBe(1);
		expect(summary?.tone).toBe('success');
		expect(summary?.allTerminal).toBe(true);
		expect(summary?.startedAt).toBe('2026-07-01 12:00:00');
		expect(summary?.finishedAt).toBe('2026-07-01 12:04:30');
	});

	it('rolls up a consecutive done run', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1, startedAt: '2026-07-01 12:00:00', completedAt: '2026-07-01 12:04:00' }),
			job({ id: 'm2', queueOrder: 2, startedAt: '2026-07-01 12:04:30', completedAt: '2026-07-01 12:09:00' }),
			job({ id: 'm3', queueOrder: 3, startedAt: '2026-07-01 12:09:30', completedAt: '2026-07-01 12:14:00' })
		]);
		expect(summary?.isBatch).toBe(true);
		expect(summary?.total).toBe(3);
		expect(summary?.done).toBe(3);
		expect(summary?.tone).toBe('success');
		// Earliest start, latest finish: the run's wall clock.
		expect(summary?.startedAt).toBe('2026-07-01 12:00:00');
		expect(summary?.finishedAt).toBe('2026-07-01 12:14:00');
		expect(summary?.id).toBe('3:3');
	});

	it('ignores audit rows appended after a finished run', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 }),
			job({ id: 'a1', queueOrder: 3, action: 'metadata_update' })
		]);
		expect(summary?.jobIds).toEqual(['m1', 'm2']);
		expect(summary?.tone).toBe('success');
	});

	it('treats legacy null-action rows as merges', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1, action: null }),
			job({ id: 'm2', queueOrder: 2, action: null })
		]);
		expect(summary?.total).toBe(2);
		expect(summary?.tone).toBe('success');
	});

	it('sorts unsorted input by queue order before walking runs', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'm3', queueOrder: 3 }),
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 })
		]);
		expect(summary?.jobIds).toEqual(['m1', 'm2', 'm3']);
	});

	it('falls back to created_at when a job never started', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1, startedAt: null, completedAt: '2026-07-01 12:00:10' })
		]);
		expect(summary?.startedAt).toBe('2026-07-01 11:59:00');
	});

	it('breaks a run at a queue-order gap', () => {
		const gapped = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 3 })
		]);
		expect(gapped?.jobIds).toEqual(['m2']);
	});

	it('breaks a run when a batched row sits in the walk', () => {
		// A batched row (any id) breaks the adjacency walk: its run is
		// owned by the aggregate.
		const mixed = summarizeLegacyRun([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'b1', queueOrder: 2, batchId: 'req-1' }),
			job({ id: 'm3', queueOrder: 3 })
		]);
		expect(mixed?.jobIds).toEqual(['m3']);
	});

	it('keeps a batched run out of the legacy rollup when it is the newest', () => {
		const summary = summarizeLegacyRun([
			job({ id: 'b1-a', queueOrder: 1, batchId: 'req-1' }),
			job({ id: 'b1-b', queueOrder: 2, batchId: 'req-1' })
		]);
		// The walk starts at the newest merge row (batched) and cannot
		// reach back across batched rows: the legacy summary is that
		// single row, not the whole request.
		expect(summary?.jobIds).toEqual(['b1-b']);
		expect(summary?.total).toBe(1);
	});
});

describe('summarizeRun (aggregate first, legacy fallback)', () => {
	it('prefers the aggregate when present, even with rows in hand', () => {
		const agg = aggregate({ batch_id: 'b1', total: 3, last_seq: 3, job_ids: ['m1', 'm2', 'm3'] });
		const rows = [
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'a1', queueOrder: 2, action: 'import' })
		];
		const summary = summarizeRun(agg, rows);
		expect(summary?.total).toBe(3);
		expect(summary?.jobIds).toEqual(['m1', 'm2', 'm3']);
		expect(summary?.id).toBe('3:3');
	});

	it('falls back to the adjacency walk when no aggregate is present', () => {
		const rows = [job({ id: 'm1', queueOrder: 1 }), job({ id: 'm2', queueOrder: 2 })];
		const summary = summarizeRun(null, rows);
		expect(summary?.jobIds).toEqual(['m1', 'm2']);
		expect(summary?.total).toBe(2);
	});

	it('returns null when there is neither an aggregate nor merge rows', () => {
		expect(summarizeRun(null, [])).toBeNull();
		expect(summarizeRun(null, [job({ id: 'a1', queueOrder: 1, action: 'import' })])).toBeNull();
	});
});
