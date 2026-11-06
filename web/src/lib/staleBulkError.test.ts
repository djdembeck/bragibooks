import { describe, expect, it } from 'vitest';
import { shouldClearStaleBulkError } from '#lib/staleBulkError.js';

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
