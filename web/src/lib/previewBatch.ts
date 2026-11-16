/**
 * Decision logic for the Match page's engine-backed output-name previews.
 * Pure state machine, no Svelte, so the concurrency cap, dispatch queue,
 * payload-ownership staleness, and failure re-queue are unit-testable
 * without mounting the component. The page keeps the fetch side effects
 * (it owns the `post` call and the debounce); this module owns ONLY the
 * bookkeeping that decides what may run and what a response is allowed to
 * change.
 */

export interface PreviewBatchState {
	/** Settled (or pending) name per book id. '' = a fetch is in flight. */
	previews: Record<number, string>;
	/** The payload each settled value / in-flight request was made for. */
	payloadFor: Map<number, string>;
	/** Book ids with a fetch currently in flight. */
	inFlight: Set<number>;
	/** Book ids awaiting a free slot (dispatch order). */
	queue: Set<number>;
}

export interface Dispatch {
	id: number;
	payload: string;
}

export function newPreviewBatchState(): PreviewBatchState {
	return { previews: {}, payloadFor: new Map(), inFlight: new Set(), queue: new Set() };
}

/**
 * Re-plan the batch for the currently wanted book ids (each with its
 * capture payload). Prunes state for ids that are no longer wanted, keeps
 * a settled value or an in-flight slot standing while its payload is
 * unchanged, requeues an in-flight id whose payload was superseded (its old
 * response fails its ownership check on settle), and fills free slots up
 * to `concurrency`. Returns the ids the caller must start fetching.
 */
export function plan(
	state: PreviewBatchState,
	wanted: Map<number, string>,
	concurrency: number
): Dispatch[] {
	const wantedIds = new Set(wanted.keys());
	for (const key of Object.keys(state.previews)) {
		const id = Number(key);
		if (!wantedIds.has(id)) {
			state.payloadFor.delete(id);
			delete state.previews[id];
		}
	}
	for (const id of [...state.inFlight]) {
		if (!wantedIds.has(id)) {
			state.payloadFor.delete(id);
			state.inFlight.delete(id);
		}
	}
	state.queue.clear();
	for (const [id, payload] of wanted) {
		// Already settled for THIS payload: the value stands.
		if (state.payloadFor.get(id) === payload && state.previews[id] !== '') continue;
		// A fetch for THIS payload is in flight: the pending slot stands. A
		// fetch for a SUPERSEDED payload falls through and is requeued below.
		if (state.inFlight.has(id) && state.payloadFor.get(id) === payload) continue;
		state.queue.add(id);
	}
	const dispatched: Dispatch[] = [];
	for (const id of [...state.queue]) {
		if (state.inFlight.size >= concurrency) break;
		const payload = wanted.get(id);
		if (payload == null) continue;
		state.queue.delete(id);
		state.inFlight.add(id);
		state.payloadFor.set(id, payload);
		state.previews[id] = '';
		dispatched.push({ id, payload });
	}
	return dispatched;
}

/**
 * Settle one completed fetch. `payload` is the payload the request was
 * dispatched with. The response lands only when the id is still wanted and
 * its recorded payload is the one this request was made for (a superseded
 * request must not clear the newer generation's slot); a failure drops the
 * slot so the id re-enters the queue on the next plan. Returns the id that
 * left the in-flight set (always, so the caller re-plans to advance).
 */
export function settle(
	state: PreviewBatchState,
	id: number,
	payload: string,
	result: { ok: boolean; value?: string }
): number {
	if (result.ok && state.payloadFor.get(id) === payload) {
		state.previews[id] = result.value ?? '';
	} else {
		// Failure, or superseded: release the slot for this payload so the
		// next plan requeues the id (retry, never a permanent pending row).
		if (state.payloadFor.get(id) === payload) {
			state.payloadFor.delete(id);
			delete state.previews[id];
		}
	}
	state.inFlight.delete(id);
	return id;
}
