/**
 * Bulk-error Retry routing for the books bulk bar.
 *
 * Which operation a bulk error was produced by determines what Retry re-runs.
 * 'select' operates on the whole matching set, not the current effective
 * selection, so an empty selection does not make its retry a no-op. For every
 * other action (requeue/confirm/done/remove) the handler posts for the
 * current selection, so a retry with an empty effective selection would
 * no-op silently and leave the alert stuck — clear the stale error instead
 * of invoking the handler.
 */
export type BulkErrorAction = 'select' | 'requeue' | 'confirm' | 'done' | 'remove';

/**
 * A single Alert Retry click, given the action that produced the error and the
 * size of the current effective selection:
 *  - 'clear': a stale requeue/confirm/done/remove error with nothing left
 *    selected — drop it instead of invoking a no-op handler;
 *  - 'select': re-run the select-all that produced the error — it requests the
 *    matching IDs only and never posts processing jobs, which is the takeover
 *    contract when a select-all fails after a previous requeue failure;
 *  - 'requeue'|'confirm'|'done'|'remove' with a non-empty selection:
 *    re-dispatch the action that produced the error (e.g. a requeue retry
 *    posts /process for its selection).
 */
export function bulkErrorRetryDispatch(
	action: BulkErrorAction,
	effectiveSelectedCount: number
): 'clear' | BulkErrorAction {
	if (action !== 'select' && effectiveSelectedCount === 0) return 'clear';
	return action;
}
