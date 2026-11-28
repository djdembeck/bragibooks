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
	/**
	 * The LATEST dispatch generation per id, monotonically increasing.
	 * plan bumps it when it requeues an in-flight id whose payload was
	 * superseded, so a response whose recorded generation is older is
	 * stale the moment plan sees the new payload — even when the
	 * replacement could not dispatch yet under a saturated cap.
	 */
	genFor: Map<number, number>;
	/**
	 * Outstanding (dispatched, unsettled) requests per id. A payload
	 * change while in flight re-dispatches a second request for the same
	 * id under ONE slot: the superseded request stays counted here until
	 * it finishes, so an older settle can never free the slot its newer
	 * replacement owns. The slot is released when the count reaches 0.
	 */
	outstanding: Map<number, number>;
	/**
	 * Consecutive failure count per id (reset on a success). plan
	 * dispatches lower-failure ids first, so a persistently failing row
	 * yields its freed slots to healthy rows instead of re-claiming the
	 * head of the order every 250 ms and starving the tail.
	 */
	failCount: Map<number, number>;
}

export interface Dispatch {
	id: number;
	payload: string;
	/** The generation this dispatch owns; pass it back to `settle`. */
	gen: number;
}

export function newPreviewBatchState(): PreviewBatchState {
	return {
		previews: {},
		payloadFor: new Map(),
		inFlight: new Set(),
		queue: new Set(),
		genFor: new Map(),
		outstanding: new Map(),
		failCount: new Map()
	};
}

/**
 * Re-plan the batch for the currently wanted book ids (each with its
 * capture payload). Prunes state for ids that are no longer wanted, keeps
 * a settled value or an in-flight slot standing while its payload is
 * unchanged, requeues an in-flight id whose payload was superseded (its
 * old response fails its ownership check on settle — even when the
 * replacement cannot dispatch yet under a saturated cap), and fills free
 * slots up to `concurrency`. Each dispatch owns a generation: one id
 * holds ONE slot no matter how many of its requests are outstanding, and
 * the slot is released only by the owning (latest) generation. Returns
 * the ids the caller must start fetching (with the generation to pass
 * back to `settle`).
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
	// genFor is NOT pruned here: its counter stays monotonic for the
	// lifetime of the batch state, so a response that outlives a prune (
	// and a re-add of the id) can never be mistaken for the newest
	// dispatch (settle treats a missing record as "not owner").
	state.queue.clear();
	for (const [id, payload] of wanted) {
		if (state.inFlight.has(id)) {
			// A fetch for THIS payload is in flight: the pending slot
			// stands.
			if (state.payloadFor.get(id) === payload) continue;
			// The payload was superseded while a fetch is in flight:
			// requeue the id and invalidate the in-flight response's
			// ownership NOW (bump the latest generation), even when the
			// replacement cannot dispatch yet under a saturated cap — a
			// stale-payload paint must be impossible the moment the new
			// payload is wanted. The in-flight slot is RETAINED: the
			// superseded request stays in the concurrency accounting
			// until it settles, so the id holds one slot while two of its
			// requests are outstanding. Clear the settled value too: while
			// the replacement waits for a free slot (the render cache is
			// cleared only for DISPATCHED rows), a settled path for the
			// PREVIOUS edition must not stay visible — the row reads as
			// pending until the new payload settles.
			state.genFor.set(id, (state.genFor.get(id) ?? 0) + 1);
			state.queue.add(id);
			state.previews[id] = '';
			continue;
		}
		// No request outstanding. Already settled for THIS payload: the
		// value stands.
		if (state.payloadFor.get(id) === payload && state.previews[id] !== '') continue;
		state.queue.add(id);
	}
	const dispatched: Dispatch[] = [];
	// Dispatch order is the queue's insertion order EXCEPT rows with a
	// higher consecutive-failure count go last (their relative order is
	// kept): a persistently failing row would otherwise re-claim the head
	// of the order on every re-plan and starve the tail.
	const order = new Map([...state.queue].map((id, i) => [id, i]));
	const ordered = [...state.queue].sort((a, b) => {
		const fa = state.failCount.get(a) ?? 0;
		const fb = state.failCount.get(b) ?? 0;
		if (fa !== fb) return fa - fb;
		return (order.get(a) ?? 0) - (order.get(b) ?? 0);
	});
	for (const id of ordered) {
		if (state.inFlight.size >= concurrency) break;
		const payload = wanted.get(id);
		if (payload == null) continue;
		state.queue.delete(id);
		const gen = (state.genFor.get(id) ?? 0) + 1;
		state.genFor.set(id, gen);
		state.outstanding.set(id, (state.outstanding.get(id) ?? 0) + 1);
		state.inFlight.add(id);
		state.payloadFor.set(id, payload);
		state.previews[id] = '';
		dispatched.push({ id, payload, gen });
	}
	return dispatched;
}

/**
 * Settle one completed fetch. `gen` is the generation `plan` returned for
 * this dispatch (see `Dispatch.gen`); `payload` is the payload the request
 * was made with. The response lands only when the id is still wanted (its
 * recorded payload is the one this request was made for) AND this
 * generation is the latest: a superseded request (its payload changed and
 * plan bumped the latest generation) must not paint over the newer
 * generation's slot, and it stays painted-blocked even when the
 * replacement was still waiting for a free slot. The slot is held while
 * the id's requests are outstanding — a payload change while in flight
 * re-dispatches a second request for the same id under ONE slot — and is
 * released when the OWNING (newest dispatched) generation settles: an
 * older superseded settle can never free the slot its replacement owns.
 * An owning failure drops the slot so the id re-enters the queue on the
 * next plan. Returns the id (always, so the caller re-plans to advance).
 */
export function settle(
	state: PreviewBatchState,
	id: number,
	payload: string,
	gen: number,
	result: { ok: boolean; value?: string }
): number {
	const count = (state.outstanding.get(id) ?? 0) - 1;
	if (count > 0) {
		state.outstanding.set(id, count);
	} else {
		state.outstanding.delete(id);
	}
	// The response may change state only for the LATEST generation with
	// the id's recorded payload intact (wanted). A pruned id, a superseded
	// payload, or a generation outlived by plan's latest-gen bump (the
	// saturated-cap case) all fail this check and settle as state no-ops.
	const latest = state.genFor.get(id) === gen && state.payloadFor.get(id) === payload;
	if (latest) {
		if (result.ok) {
			state.previews[id] = result.value ?? '';
			state.failCount.delete(id);
		} else {
			// Failure by the latest dispatch: drop the settled slot so the
			// next plan requeues the id (retry, never a permanent pending
			// row), and count the failure so plan defers it behind
			// healthy rows.
			state.payloadFor.delete(id);
			delete state.previews[id];
			state.failCount.set(id, (state.failCount.get(id) ?? 0) + 1);
		}
	} else if (
		count === 0 &&
		state.payloadFor.get(id) === payload &&
		state.previews[id] === ''
	) {
		// The last outstanding request for the id was superseded and
		// settled without painting (a stale response, or a failure of a
		// non-latest dispatch): the id is still wanted, so drop BOTH the
		// pending preview slot and the payload-cache entry to make the
		// next plan requeue it — a surviving payload record would match
		// the (re-wanted) payload against a missing preview and make the
		// row read as already settled, stranding it with no dispatch.
		// The previews[id] === '' guard keeps this branch from deleting a
		// NEWER settled value that stands for the SAME payload (a
		// supersede/re-want of the identical payload): only a stale settle
		// on a PENDING slot may clear it; a standing value survives.
		state.payloadFor.delete(id);
		delete state.previews[id];
	}
	// The slot is released when the LAST outstanding request for the id
	// settles: a payload change while in flight keeps the id's ONE slot
	// counted while its superseded request is unfinished, so the real
	// fetch count never exceeds the cap.
	if (count === 0) {
		state.inFlight.delete(id);
	}
	return id;
}
