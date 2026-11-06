/**
 * Run task factories with at most `concurrency` promises in flight; results
 * come back in input order (the caller pairs them positionally, e.g. the
 * src_dirs pairing). A rejection sets `failed`, so no worker dequeues
 * further ids — only the requests already issued may still complete, and a
 * retry cannot double-issue against this pool.
 */
export async function fetchInChunks<T>(fns: Array<() => Promise<T>>, concurrency: number): Promise<T[]> {
	const results: T[] = new Array(fns.length);
	let next = 0;
	let failed = false;
	const workers = Array.from({ length: Math.min(concurrency, fns.length) }, async () => {
		for (;;) {
			if (failed) return;
			const i = next++;
			if (i >= fns.length) return;
			try {
				results[i] = await fns[i]();
			} catch (e) {
				failed = true;
				throw e;
			}
		}
	});
	await Promise.all(workers);
	return results;
}
