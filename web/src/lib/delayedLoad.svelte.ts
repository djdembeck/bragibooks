export interface DelayedLoadOptions {
	/** Milliseconds to wait before showing a skeleton. Defaults to 200. */
	delay?: number;
}

export interface DelayedLoadState {
	/** True only after `delay` has elapsed while a load is in flight. */
	readonly showSkeleton: boolean;

	/** Error message from the most recent failed load, or null. */
	readonly error: string | null;

	/** Manually set the error message (for form submissions, etc.). */
	setError(value: string | null): void;

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
	let error = $state<string | null>(null);
	let timer: number | null = null;
	let generation = 0;
	const delay = options.delay ?? 200;

	async function run(fn: () => Promise<void>) {
		const g = ++generation;
		if (timer !== null) {
			clearTimeout(timer);
			timer = null;
		}
		timer = window.setTimeout(() => {
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
				error = e instanceof Error ? e.message : 'Failed to load';
			}
		} finally {
			if (timer !== null) {
				clearTimeout(timer);
				timer = null;
			}
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
		setError(value: string | null) {
			error = value;
		},
		run
	};
}
