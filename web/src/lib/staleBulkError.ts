/**
 * Stale bulk-error guard for the books bulk bar: 'select' and 'confirm'
 * operate on the whole matching set, not the current effective selection,
 * so an empty selection does not make their retry a no-op. For the other
 * actions, a retry with an empty effective selection would no-op silently.
 */
export type BulkErrorAction = 'select' | 'requeue' | 'confirm' | 'done' | 'remove';

/**
 * A done/remove/requeue retry with an empty effective selection would no-op
 * silently — clear the stale error instead of invoking the handler.
 */
export function shouldClearStaleBulkError(action: BulkErrorAction, effectiveSelectedCount: number): boolean {
	return action !== 'select' && action !== 'confirm' && effectiveSelectedCount === 0;
}
