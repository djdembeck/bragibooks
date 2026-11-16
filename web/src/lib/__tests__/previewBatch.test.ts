import { describe, expect, it } from 'vitest';
import { newPreviewBatchState, plan, settle, type PreviewBatchState } from '../previewBatch';

function state(): PreviewBatchState {
	return newPreviewBatchState();
}

function wanted(...ids: number[]): Map<number, string> {
	return new Map(ids.map((id) => [id, `payload-${id}`]));
}

describe('plan', () => {
	it('dispatches up to the concurrency cap and queues the rest', () => {
		const s = state();
		const d = plan(s, wanted(1, 2, 3, 4, 5), 3);
		expect(d.map((x) => x.id).sort((a, b) => a - b)).toEqual([1, 2, 3]);
		expect(s.inFlight).toEqual(new Set([1, 2, 3]));
		expect(s.queue).toEqual(new Set([4, 5]));
		expect(s.previews).toEqual({ 1: '', 2: '', 3: '' });
	});

	it('advances the queue by one slot as requests settle', () => {
		const s = state();
		plan(s, wanted(1, 2, 3, 4), 3);
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'a.m4b' });
		const d = plan(s, wanted(1, 2, 3, 4), 3);
		// id 1 is settled: its value stands, and id 4 takes the freed slot.
		expect(d.map((x) => x.id)).toEqual([4]);
		expect(s.previews[1]).toBe('a.m4b');
		expect(s.inFlight).toEqual(new Set([2, 3, 4]));
	});

	it('keeps a settled value standing while its payload is unchanged', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'a.m4b' });
		const d = plan(s, wanted(1), 3);
		expect(d).toEqual([]);
		expect(s.previews[1]).toBe('a.m4b');
	});

	it('re-requests an id whose payload changed while its value stood', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'a.m4b' });
		const d = plan(s, new Map([[1, 'payload-1-v2']]), 3); // new payload
		expect(d.map((x) => x.payload)).toEqual(['payload-1-v2']);
		// The stale value is replaced by a pending slot until the new fetch lands.
		expect(s.previews[1]).toBe('');
	});

	it('keeps the newer generation slot when an older generation settles', () => {
		const s = state();
		plan(s, wanted(1), 3); // gen 1 in flight for payload-1
		// The payload changes while gen 1 is in flight: the id requeues and
		// gen 2 takes a slot for the fresh payload. The id now has TWO
		// requests outstanding under ONE slot.
		const d = plan(s, new Map([[1, 'payload-1-v2']]), 3);
		expect(d.map((x) => x.payload)).toEqual(['payload-1-v2']);
		expect(s.inFlight).toEqual(new Set([1]));
		// Gen 1 finally resolves: it must NOT paint and must NOT release
		// the slot the newer dispatch owns — its request is still
		// outstanding, so the id's one slot stays counted.
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'STALE.m4b' });
		expect(s.previews[1]).toBe(''); // newer slot untouched, not STALE
		expect(s.inFlight).toEqual(new Set([1])); // slot RETAINED
		// The slot stands for the in-flight replacement: no re-dispatch.
		const d2 = plan(s, new Map([[1, 'payload-1-v2']]), 3);
		expect(d2).toEqual([]);
		// The replacement (dispatch gen 3) settles: the value lands and
		// the slot is released.
		settle(s, 1, 'payload-1-v2', 3, { ok: true, value: 'NEW.m4b' });
		expect(s.previews[1]).toBe('NEW.m4b');
		expect(s.inFlight).toEqual(new Set());
	});

	it('requeues a failed fetch so it retries on the next plan', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', 1, { ok: false });
		expect(s.previews).toEqual({});
		expect(s.inFlight).toEqual(new Set());
		const d = plan(s, wanted(1), 3);
		expect(d.map((x) => x.id)).toEqual([1]);
	});

	it('prunes ids that are no longer wanted before deferring', () => {
		const s = state();
		plan(s, wanted(1, 2, 3, 4, 5), 3); // in flight 1,2,3; queued 4,5
		// id 2 (in flight) and id 4 (queued) disappear; id 2's response lands.
		settle(s, 2, 'payload-2', 1, { ok: true, value: 'b.m4b' });
		const d = plan(s, wanted(1, 3, 5), 3);
		// id 4 is gone (pruned, not dispatched); the freed slot goes to id 5.
		expect(d.map((x) => x.id)).toEqual([5]);
		expect(s.inFlight).toEqual(new Set([1, 3, 5]));
		expect(s.queue).toEqual(new Set());
		expect(s.previews[2]).toBeUndefined();
	});
});

describe('settle', () => {
	it('lands a response only for the payload the request was made for', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'a.m4b' });
		expect(s.previews[1]).toBe('a.m4b');
	});

	it('re-dispatches a superseded id once its original payload is wanted again', () => {
		const s = state();
		plan(s, new Map([[1, 'p-1']]), 1); // id 1 dispatched, gen 1
		// The payload changes while in flight: gen bumped to 2, the
		// replacement cannot dispatch (cap saturated by the in-flight
		// request).
		const d2 = plan(s, new Map([[1, 'p-1-v2'], [2, 'p-2']]), 1);
		expect(d2).toEqual([]);
		// Re-plan with the same new payload: gen bumped to 3, still no
		// dispatch.
		const d3 = plan(s, new Map([[1, 'p-1-v2'], [2, 'p-2']]), 1);
		expect(d3).toEqual([]);
		// The superseded gen-1 request settles: it must not paint, and the
		// pending slot is dropped.
		settle(s, 1, 'p-1', 1, { ok: true, value: 'STALE.m4b' });
		expect(s.previews[1]).not.toBe('STALE.m4b');
		expect(s.inFlight).toEqual(new Set());
		// The payload reverts: the id must be re-dispatched, not treated
		// as already settled by a leftover payload record.
		const d4 = plan(s, new Map([[1, 'p-1']]), 1);
		expect(d4.map((x) => x.id)).toEqual([1]);
	});

	it('releases the slot when the id was wanted at dispatch but is not anymore', () => {
		const s = state();
		plan(s, wanted(1), 3);
		plan(s, wanted(2), 3); // id 1 no longer wanted; its slot is freed
		expect(s.inFlight).toEqual(new Set([2]));
		// The in-flight gen-1 response for id 1 must not resurrect it.
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'a.m4b' });
		expect(s.previews[1]).toBeUndefined();
		expect(s.inFlight).toEqual(new Set([2]));
	});
});

describe('generation accounting', () => {
	it('retains the slot for a superseded request under a saturated cap', () => {
		const s = state();
		plan(s, wanted(1, 2, 3), 3); // all three slots in flight, gen 1
		// id 1's payload changes while the cap is SATURATED: the
		// replacement cannot dispatch, so its request never goes out.
		// The stale in-flight response must be invalidated NOW.
		plan(s, new Map([[1, 'p1-v2'], [2, 'p2'], [3, 'p3']]), 3);
		expect(s.inFlight).toEqual(new Set([1, 2, 3])); // slot retained
		// Gen 1 for id 1 settles: it must not paint (payload changed) and,
		// being the last outstanding request, it releases the slot and
		// clears the pending slot so the next plan requeues the id.
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'STALE.m4b' });
		expect(s.previews[1]).toBeUndefined();
		expect(s.inFlight).toEqual(new Set([2, 3]));
		const d = plan(s, new Map([[1, 'p1-v2'], [2, 'p2'], [3, 'p3']]), 3);
		expect(d.map((x) => x.id)).toEqual([1]);
		expect(d.map((x) => x.payload)).toEqual(['p1-v2']);
	});

	it('releases the slot only when the owning generation settles', () => {
		const s = state();
		plan(s, wanted(1), 3); // dispatch gen 1
		// Payload change: the supersede bumps the latest generation and the
		// replacement dispatches under gen 3; gen 1 stays outstanding.
		plan(s, new Map([[1, 'payload-1-v2']]), 3);
		// Both requests outstanding: the id holds its ONE slot.
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'STALE.m4b' });
		expect(s.inFlight).toEqual(new Set([1])); // older settle: retained
		// The owning (newer) generation fails: the slot is released so the
		// id retries on the next plan.
		settle(s, 1, 'payload-1-v2', 3, { ok: false });
		expect(s.inFlight).toEqual(new Set());
		expect(s.previews[1]).toBeUndefined();
		const d = plan(s, new Map([[1, 'payload-1-v2']]), 3);
		expect(d.map((x) => x.id)).toEqual([1]);
	});

	it('lets a late older settle release the slot after the newer one already settled', () => {
		const s = state();
		plan(s, wanted(1), 3); // dispatch gen 1
		// The supersede bump moves the latest generation past gen 1, and
		// the replacement dispatches under gen 3.
		plan(s, new Map([[1, 'payload-1-v2']]), 3);
		// The NEWER request settles first: the value lands, but the slot
		// is retained while the superseded gen 1 is still outstanding.
		settle(s, 1, 'payload-1-v2', 3, { ok: true, value: 'NEW.m4b' });
		expect(s.previews[1]).toBe('NEW.m4b');
		expect(s.inFlight).toEqual(new Set([1]));
		// Gen 1 settles late: it must not repaint and it releases the slot.
		settle(s, 1, 'payload-1', 1, { ok: true, value: 'STALE.m4b' });
		expect(s.previews[1]).toBe('NEW.m4b');
		expect(s.inFlight).toEqual(new Set());
	});
});

describe('plan + settle together', () => {
	it('serves a full batch past the cap in waves', () => {
		const s = state();
		const ids = [1, 2, 3, 4, 5];
		plan(s, wanted(...ids), 3);
		for (const id of [1, 2, 3]) settle(s, id, `payload-${id}`, 1, { ok: true, value: `${id}.m4b` });
		plan(s, wanted(...ids), 3);
		for (const id of [4, 5]) settle(s, id, `payload-${id}`, 1, { ok: true, value: `${id}.m4b` });
		const d = plan(s, wanted(...ids), 3);
		expect(d).toEqual([]);
		for (const id of ids) expect(s.previews[id]).toBe(`${id}.m4b`);
	});
});
