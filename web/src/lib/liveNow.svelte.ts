/**
 * A once-per-second clock that only runs while `active()` is true.
 *
 * Pages with no live work schedule no interval, so an idle queue costs
 * nothing. Call during component initialization; the returned getter is
 * reactive, and `active()` may read derived state.
 */
export function liveNow(active: () => boolean): { readonly now: number } {
	let now = $state(Date.now());

	$effect(() => {
		if (!active()) return;
		now = Date.now();
		const timer = window.setInterval(() => (now = Date.now()), 1000);
		return () => window.clearInterval(timer);
	});

	return {
		get now() {
			return now;
		}
	};
}
