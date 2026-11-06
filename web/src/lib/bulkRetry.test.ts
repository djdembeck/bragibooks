import { describe, expect, it, vi } from 'vitest';
import { bulkRetryHandler, shouldClearStaleBulkError, type BulkErrorAction } from '#lib/bulkRetry.js';

function handlers(): Record<BulkErrorAction, () => void> {
	return {
		select: vi.fn(),
		requeue: vi.fn(),
		confirm: vi.fn(),
		done: vi.fn(),
		remove: vi.fn()
	};
}

describe('bulkRetryHandler', () => {
	it.each([
		['select', 'select'],
		['requeue', 'requeue'],
		['confirm', 'confirm'],
		['done', 'done'],
		['remove', 'remove']
	] as const)('maps the %s error to the %s handler', (action, key) => {
		const h = handlers();
		const handler = bulkRetryHandler(action, h);
		handler();
		expect(h[key]).toHaveBeenCalledTimes(1);
		for (const other of Object.keys(h) as BulkErrorAction[]) {
			if (other !== key) expect(h[other], `${other} must not run`).not.toHaveBeenCalled();
		}
	});

	it('never routes requeue to a deletion path', () => {
		const h = handlers();
		bulkRetryHandler('requeue', h)();
		expect(h.remove).not.toHaveBeenCalled();
		expect(h.done).not.toHaveBeenCalled();
		expect(h.confirm).not.toHaveBeenCalled();
	});
});

describe('shouldClearStaleBulkError', () => {
	it.each([
		['select', 0, false],
		['confirm', 0, false],
		['requeue', 0, true],
		['done', 0, true],
		['remove', 0, true],
		['requeue', 3, false],
		['done', 1, false],
		['remove', 2, false]
	] as const)('%s with %d selected → clear=%s', (action, count, want) => {
		expect(shouldClearStaleBulkError(action, count)).toBe(want);
	});
});
