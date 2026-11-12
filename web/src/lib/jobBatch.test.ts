import { describe, expect, it } from 'vitest';
import { summarizeLatestBatch, type BatchJob } from './jobBatch';

function job(overrides: Partial<BatchJob> & { id: string; queueOrder: number }): BatchJob {
	return {
		status: 'done',
		action: 'merge',
		startedAt: '2026-07-01 12:00:00',
		completedAt: '2026-07-01 12:04:30',
		createdAt: '2026-07-01 11:59:00',
		...overrides,
	};
}

describe('summarizeLatestBatch', () => {
	it('returns null for an empty list and for audit-only activity', () => {
		expect(summarizeLatestBatch([])).toBeNull();
		expect(
			summarizeLatestBatch([
				job({ id: 'a1', queueOrder: 1, action: 'import' }),
				job({ id: 'a2', queueOrder: 2, action: 'metadata_update' }),
			])
		).toBeNull();
	});

	it('reports a single finished merge job as success, not a batch', () => {
		const summary = summarizeLatestBatch([job({ id: 'm1', queueOrder: 1 })]);
		expect(summary).not.toBeNull();
		expect(summary?.isBatch).toBe(false);
		expect(summary?.total).toBe(1);
		expect(summary?.done).toBe(1);
		expect(summary?.tone).toBe('success');
		expect(summary?.allTerminal).toBe(true);
		expect(summary?.startedAt).toBe('2026-07-01 12:00:00');
		expect(summary?.finishedAt).toBe('2026-07-01 12:04:30');
	});

	it('rolls up a consecutive done batch', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, startedAt: '2026-07-01 12:00:00', completedAt: '2026-07-01 12:04:00' }),
			job({ id: 'm2', queueOrder: 2, startedAt: '2026-07-01 12:04:30', completedAt: '2026-07-01 12:09:00' }),
			job({ id: 'm3', queueOrder: 3, startedAt: '2026-07-01 12:09:30', completedAt: '2026-07-01 12:14:00' }),
		]);
		expect(summary?.isBatch).toBe(true);
		expect(summary?.total).toBe(3);
		expect(summary?.done).toBe(3);
		expect(summary?.tone).toBe('success');
		// Earliest start, latest finish: the batch wall clock.
		expect(summary?.startedAt).toBe('2026-07-01 12:00:00');
		expect(summary?.finishedAt).toBe('2026-07-01 12:14:00');
		expect(summary?.id).toBe('3:3');
	});

	it('tints a partial failure warning and a full failure error', () => {
		const partial = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2, status: 'error', completedAt: '2026-07-01 12:08:00' }),
		]);
		expect(partial?.tone).toBe('warning');
		expect(partial?.failed).toBe(1);
		expect(partial?.done).toBe(1);

		const full = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'error' }),
			job({ id: 'm2', queueOrder: 2, status: 'failed' }),
		]);
		expect(full?.tone).toBe('error');
		expect(full?.failed).toBe(2);
	});

	it('reports an all-cancelled batch as stopped', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'cancelled' }),
			job({ id: 'm2', queueOrder: 2, status: 'cancelled' }),
		]);
		expect(summary?.tone).toBe('info');
		expect(summary?.cancelled).toBe(2);
	});

	it('keeps an in-flight cancel out of the settled outcome until the backend row lands', () => {
		const single = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'cancelled', cancelRequested: true }),
		]);
		expect(single?.allTerminal).toBe(false);
		expect(single?.inFlight).toBe(1);
		expect(single?.cancelled).toBe(0);

		const mixed = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'done' }),
			job({ id: 'm2', queueOrder: 2, status: 'cancelled', cancelRequested: true }),
		]);
		expect(mixed?.allTerminal).toBe(false);
		expect(mixed?.cancelled).toBe(0);
		expect(mixed?.tone).toBe('info');
	});

	it('counts a cancelled job once the backend terminal row confirms it', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'cancelled' }),
			job({ id: 'm2', queueOrder: 2, status: 'cancelled', cancelRequested: false }),
		]);
		expect(summary?.allTerminal).toBe(true);
		expect(summary?.cancelled).toBe(2);
		expect(summary?.tone).toBe('info');
	});

	it('flags in-flight batches and tints them by what has already failed', () => {
		const inFlight = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2, status: 'running', startedAt: '2026-07-01 12:05:00', completedAt: null }),
		]);
		expect(inFlight?.allTerminal).toBe(false);
		expect(inFlight?.inFlight).toBe(1);
		expect(inFlight?.tone).toBe('info');
		// Only the finished job contributes; the wall clock stays hidden
		// until allTerminal.
		expect(inFlight?.finishedAt).toBe('2026-07-01 12:04:30');

		const failing = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, status: 'error' }),
			job({ id: 'm2', queueOrder: 2, status: 'running', completedAt: null }),
		]);
		expect(failing?.tone).toBe('warning');
	});

	it('treats interleaved audit rows as a run break: the newer run wins', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 }),
			job({ id: 'a1', queueOrder: 3, action: 'import' }),
			job({ id: 'm3', queueOrder: 4, status: 'running', completedAt: null }),
		]);
		expect(summary?.jobIds).toEqual(['m3']);
		expect(summary?.allTerminal).toBe(false);
	});

	it('ignores audit rows appended after a finished batch', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 }),
			job({ id: 'a1', queueOrder: 3, action: 'metadata_update' }),
		]);
		expect(summary?.jobIds).toEqual(['m1', 'm2']);
		expect(summary?.tone).toBe('success');
	});

	it('treats legacy null-action rows as merges', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, action: null }),
			job({ id: 'm2', queueOrder: 2, action: null }),
		]);
		expect(summary?.total).toBe(2);
		expect(summary?.tone).toBe('success');
	});

	it('sorts unsorted input by queue order before walking runs', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm3', queueOrder: 3 }),
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 }),
		]);
		expect(summary?.jobIds).toEqual(['m1', 'm2', 'm3']);
	});

	it('falls back to created_at when a job never started', () => {
		const summary = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1, startedAt: null, completedAt: '2026-07-01 12:00:10' }),
		]);
		expect(summary?.startedAt).toBe('2026-07-01 11:59:00');
	});

	it('derives a stable dismissal identity from the run window', () => {
		const first = summarizeLatestBatch([job({ id: 'm1', queueOrder: 1 }), job({ id: 'm2', queueOrder: 2 })]);
		const sameRun = summarizeLatestBatch([job({ id: 'm1', queueOrder: 1 }), job({ id: 'm2', queueOrder: 2 })]);
		const next = summarizeLatestBatch([
			job({ id: 'm1', queueOrder: 1 }),
			job({ id: 'm2', queueOrder: 2 }),
			job({ id: 'm3', queueOrder: 3 }),
		]);
		expect(first?.id).toBe(sameRun?.id);
		expect(next?.id).not.toBe(first?.id);
	});
});
