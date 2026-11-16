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
		settle(s, 1, 'payload-1', { ok: true, value: 'a.m4b' });
		const d = plan(s, wanted(1, 2, 3, 4), 3);
		// id 1 is settled: its value stands, and id 4 takes the freed slot.
		expect(d.map((x) => x.id)).toEqual([4]);
		expect(s.previews[1]).toBe('a.m4b');
		expect(s.inFlight).toEqual(new Set([2, 3, 4]));
	});

	it('keeps a settled value standing while its payload is unchanged', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', { ok: true, value: 'a.m4b' });
		const d = plan(s, wanted(1), 3);
		expect(d).toEqual([]);
		expect(s.previews[1]).toBe('a.m4b');
	});

	it('re-requests an id whose payload changed while its value stood', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', { ok: true, value: 'a.m4b' });
		const d = plan(s, new Map([[1, 'payload-1-v2']]), 3); // new payload
		expect(d.map((x) => x.payload)).toEqual(['payload-1-v2']);
		// The stale value is replaced by a pending slot until the new fetch lands.
		expect(s.previews[1]).toBe('');
	});

	it('does not let a stale response clear the newer generation slot', () => {
		const s = state();
		plan(s, wanted(1), 3); // gen 1 in flight for payload-1
		// The payload changes while gen 1 is in flight: the id requeues and
		// gen 2 takes a slot for the fresh payload.
		const d = plan(s, new Map([[1, 'payload-1-v2']]), 3);
		expect(d.map((x) => x.payload)).toEqual(['payload-1-v2']);
		expect(s.inFlight).toEqual(new Set([1]));
		// Gen 1 finally resolves: it must NOT touch the slot gen 2 owns.
		settle(s, 1, 'payload-1', { ok: true, value: 'STALE.m4b' });
		expect(s.previews[1]).toBe(''); // gen 2 slot untouched, not STALE
		expect(s.inFlight).toEqual(new Set());
		// Next plan re-dispatches gen 3 for the current payload.
		const d2 = plan(s, new Map([[1, 'payload-1-v2']]), 3);
		expect(d2.map((x) => x.payload)).toEqual(['payload-1-v2']);
	});

	it('requeues a failed fetch so it retries on the next plan', () => {
		const s = state();
		plan(s, wanted(1), 3);
		settle(s, 1, 'payload-1', { ok: false });
		expect(s.previews).toEqual({});
		expect(s.inFlight).toEqual(new Set());
		const d = plan(s, wanted(1), 3);
		expect(d.map((x) => x.id)).toEqual([1]);
	});

	it('prunes ids that are no longer wanted before deferring', () => {
		const s = state();
		plan(s, wanted(1, 2, 3, 4, 5), 3); // in flight 1,2,3; queued 4,5
		// id 2 (in flight) and id 4 (queued) disappear; id 2's response lands.
		settle(s, 2, 'payload-2', { ok: true, value: 'b.m4b' });
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
		settle(s, 1, 'payload-1', { ok: true, value: 'a.m4b' });
		expect(s.previews[1]).toBe('a.m4b');
	});

	it('releases the slot when the id was wanted at dispatch but is not anymore', () => {
		const s = state();
		plan(s, wanted(1), 3);
		plan(s, wanted(2), 3); // id 1 no longer wanted; its slot is freed
		expect(s.inFlight).toEqual(new Set([2]));
		// The in-flight gen-1 response for id 1 must not resurrect it.
		settle(s, 1, 'payload-1', { ok: true, value: 'a.m4b' });
		expect(s.previews[1]).toBeUndefined();
		expect(s.inFlight).toEqual(new Set([2]));
	});
});

describe('plan + settle together', () => {
	it('serves a full batch past the cap in waves', () => {
		const s = state();
		const ids = [1, 2, 3, 4, 5];
		plan(s, wanted(...ids), 3);
		for (const id of [1, 2, 3]) settle(s, id, `payload-${id}`, { ok: true, value: `${id}.m4b` });
		plan(s, wanted(...ids), 3);
		for (const id of [4, 5]) settle(s, id, `payload-${id}`, { ok: true, value: `${id}.m4b` });
		const d = plan(s, wanted(...ids), 3);
		expect(d).toEqual([]);
		for (const id of ids) expect(s.previews[id]).toBe(`${id}.m4b`);
	});
});
