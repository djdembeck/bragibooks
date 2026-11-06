import { describe, expect, it } from 'vitest';
import { bulkErrorRetryDispatch, type BulkErrorAction } from '#lib/staleBulkError.js';

describe('bulkErrorRetryDispatch', () => {
	it.each([
		['select', 0, 'select'],
		['confirm', 0, 'confirm'],
		['requeue', 0, 'clear'],
		['done', 0, 'clear'],
		['remove', 0, 'clear'],
		['requeue', 3, 'requeue'],
		['done', 1, 'done'],
		['remove', 2, 'remove']
	] as const)('%s with %d selected → %s', (action, count, want) => {
		expect(bulkErrorRetryDispatch(action, count)).toBe(want);
	});

	// Regression: a requeue failure sets the bulk error's action to 'requeue';
	// a subsequent failed select-all must take over the error so Retry re-runs
	// the select-all (requests the matching IDs only) — not re-dispatch the
	// requeue, which would post processing jobs.
	it('a failed select-all takes over a prior requeue error; Retry re-runs the select-all only', () => {
		// 1. Requeue of the matching set fails → the error is owned by requeue.
		let action: BulkErrorAction = 'requeue';
		let selected = 2;
		expect(bulkErrorRetryDispatch(action, selected)).toBe('requeue');

		// 2. A fresh select-all then fails: it re-arms the error as 'select'
		//    and drops the baseline, so the effective selection is empty.
		action = 'select';
		selected = 0;

		// 3. Retry dispatches the select-all — which requests the matching
		//    IDs only and never posts processing jobs.
		expect(bulkErrorRetryDispatch(action, selected)).toBe('select');
	});
});
