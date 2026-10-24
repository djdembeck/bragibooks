import { cleanAdbError, toCleanedError, type CleanedAdbError } from './adbError';

export interface DelayedLoadOptions {
	/** Milliseconds to wait before showing a skeleton. Defaults to 200. */
	delay?: number;
}

export interface DelayedLoadState {
	/** True only after `delay` has elapsed while a load is in flight. */
	readonly showSkeleton: boolean;

	/** Cleaned error from the most recent failed load, or null. */
	readonly error: CleanedAdbError | null;

	/** Manually set the error (for form submissions, etc.). */
	setError(value: string | CleanedAdbError | null): void;

	/**
	 * Run `fn` with delayed skeleton and error handling. If another call to
	 * `run` starts before `fn` resolves, stale results are ignored.
	 */
	run(fn: () => Promise<void>): Promise<void>;
}

/**
 * Returns a reactive load controller that delays showing a skeleton so fast
 * loads (cached data, quick filter changes) do not flash placeholder UI.
 */
export function delayedLoad(options: DelayedLoadOptions = {}): DelayedLoadState {
	let showSkeleton = $state(false);
	let error = $state<CleanedAdbError | null>(null);
	let generation = 0;
	const delay = options.delay ?? 200;

	async function run(fn: () => Promise<void>) {
		const g = ++generation;
		// Per-call timer id: a stale load only clears its own timer, so an
		// earlier in-flight run can't cancel the skeleton of a newer one.
		const timer = window.setTimeout(() => {
			if (g === generation) {
				showSkeleton = true;
			}
		}, delay);

		try {
			await fn();
			if (g === generation) {
				error = null;
			}
		} catch (e) {
			if (g === generation) {
				error = toCleanedError(e);
			}
		} finally {
			clearTimeout(timer);
			if (g === generation) {
				showSkeleton = false;
			}
		}
	}

	return {
		get showSkeleton() {
			return showSkeleton;
		},
		get error() {
			return error;
		},
		setError(value: string | CleanedAdbError | null) {
			error = value === null ? null : typeof value === 'string' ? cleanAdbError(value) : value;
		},
		run
	};
}
