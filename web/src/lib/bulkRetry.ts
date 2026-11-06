/**
 * Alert-Retry dispatch for the books bulk bar: which operation produced
 * `bulkError` determines which operation Retry re-runs. 'confirm' means the
 * pre-confirmation title fetch failed, so Retry rebuilds the confirmation
 * list — the deletion itself never ran. 'select' and 'confirm' operate on
 * the whole matching set, not the current effective selection, so an empty
 * selection does not make their retry a no-op.
 */
export type BulkErrorAction = 'select' | 'requeue' | 'confirm' | 'done' | 'remove';

export interface BulkRetryHandlers {
	select: () => void;
	requeue: () => void;
	confirm: () => void;
	done: () => void;
	remove: () => void;
}

/** The handler Retry invokes for the given error action. */
export function bulkRetryHandler(action: BulkErrorAction, handlers: BulkRetryHandlers): () => void {
	return handlers[action];
}

/**
 * A done/remove/requeue retry with an empty effective selection would no-op
 * silently — clear the stale error instead of invoking the handler.
 */
export function shouldClearStaleBulkError(action: BulkErrorAction, effectiveSelectedCount: number): boolean {
	return action !== 'select' && action !== 'confirm' && effectiveSelectedCount === 0;
}
