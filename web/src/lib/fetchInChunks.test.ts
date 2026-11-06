import { describe, expect, it } from 'vitest';
import { fetchInChunks } from '#lib/fetchInChunks.js';

/** A promise resolved only when its task is explicitly released. */
interface Gate<T> {
	promise: Promise<T>;
	resolve: (value: T) => void;
}
function gate<T>(): Gate<T> {
	const { promise, resolve } = Promise.withResolvers<T>();
	return { promise, resolve };
}

/** Let queued microtasks run so already-issued tasks have started. */
function tick(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

describe('fetchInChunks', () => {
	it('preserves input indices when completion is out of order (src_dirs pairing)', async () => {
		const gates = [gate<number>(), gate<number>(), gate<number>()];
		const call = fetchInChunks(gates.map((g) => () => g.promise), 3);
		await tick();
		// Completion order is the reverse of input order.
		gates[2].resolve(2);
		gates[1].resolve(1);
		gates[0].resolve(0);
		expect(await call).toEqual([0, 1, 2]);
	});

	it('caps simultaneous starts at the concurrency', async () => {
		const started: number[] = [];
		const gates: Array<Gate<number>> = Array.from({ length: 8 }, () => gate<number>());
		const call = fetchInChunks(
			gates.map((g, i) => () => {
				started.push(i);
				return g.promise;
			}),
			3
		);
		await tick();
		// Exactly the first `concurrency` tasks are in flight; the rest wait
		// on the worker pool.
		expect([...started].sort((a, b) => a - b)).toEqual([0, 1, 2]);
		// A slot opens only as an issued task finishes.
		gates[0].resolve(0);
		await tick();
		expect(started).toContain(3);
		expect(started).toHaveLength(4);
		gates.slice(1).forEach((g, i) => g.resolve(i + 1));
		expect(await call).toEqual([0, 1, 2, 3, 4, 5, 6, 7]);
	});

	it('a rejection rejects the whole call and stops dequeuing', async () => {
		const started: number[] = [];
		const gates: Array<Gate<number>> = Array.from({ length: 6 }, () => gate<number>());
		const call = fetchInChunks(
			gates.map((g, i) => () => {
				started.push(i);
				if (i === 1) return Promise.reject(new Error('boom'));
				return g.promise;
			}),
			2
		);
		await expect(call).rejects.toThrow('boom');
		await tick();
		// Only the two tasks already issued ran; indices 2-5 must never be
		// dequeued after the rejection.
		expect([...started].sort((a, b) => a - b)).toEqual([0, 1]);
	});

	it('resolves an empty task list', async () => {
		expect(await fetchInChunks([], 4)).toEqual([]);
	});

	it('tolerates a concurrency above the task count', async () => {
		expect(await fetchInChunks([() => Promise.resolve('a'), () => Promise.resolve('b')], 100)).toEqual([
			'a',
			'b'
		]);
	});
});
