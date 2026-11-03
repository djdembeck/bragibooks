import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { delayedLoad } from '../delayedLoad.svelte';
import FirstLoadProbe from './FirstLoadProbe.svelte';

/** Snapshot the controller's public state; the getters are plain reads outside a component. */
function makeProbe() {
	const dl = delayedLoad({ delay: 200 });
	return {
		dl,
		state: () => ({ hasLoaded: dl.hasLoaded, showSkeleton: dl.showSkeleton, error: dl.error })
	};
}

describe('delayedLoad hasLoaded', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('starts false before any load runs', () => {
		expect(makeProbe().state().hasLoaded).toBe(false);
	});

	it('becomes true after a successful settlement', async () => {
		const { dl, state } = makeProbe();
		await dl.run(async () => {});
		expect(state().hasLoaded).toBe(true);
		expect(state().error).toBeNull();
	});

	it('becomes true after a rejected settlement and records the error', async () => {
		const { dl, state } = makeProbe();
		await dl.run(async () => {
			throw new Error('boom');
		});
		expect(state().hasLoaded).toBe(true);
		expect(state().error?.message).toContain('boom');
	});

	it('stays true across subsequent loads', async () => {
		const { dl, state } = makeProbe();
		await dl.run(async () => {});
		const pending = dl.run(async () => {
			await new Promise((resolve) => setTimeout(resolve, 50));
		});
		expect(state().hasLoaded).toBe(true);
		await vi.advanceTimersByTimeAsync(60);
		await pending;
		expect(state().hasLoaded).toBe(true);
	});

	it('is not set by a superseded run that settles first', async () => {
		const { dl, state } = makeProbe();
		let releaseStale: () => void = () => {};
		const stale = dl.run(
			() =>
				new Promise<void>((resolve) => {
					releaseStale = resolve;
				})
		);
		// Second run supersedes the first; the stale completion must be ignored.
		const fresh = dl.run(async () => {
			await new Promise((resolve) => setTimeout(resolve, 100));
		});
		releaseStale();
		await stale;
		expect(state().hasLoaded).toBe(false);
		await vi.advanceTimersByTimeAsync(120);
		await fresh;
		expect(state().hasLoaded).toBe(true);
	});
});

describe('delayedLoad skeleton timing', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('does not show the skeleton before the delay elapses', async () => {
		const { dl, state } = makeProbe();
		const run = dl.run(() => new Promise<void>((resolve) => setTimeout(resolve, 500)));
		await vi.advanceTimersByTimeAsync(150);
		expect(state().showSkeleton).toBe(false);
		await vi.advanceTimersByTimeAsync(400);
		await run;
		expect(state().showSkeleton).toBe(false);
	});
});

describe('first render with a fast response', () => {
	it('never renders the empty state before the first load settles', async () => {
		const target = document.createElement('div');
		document.body.appendChild(target);
		const component = mount(FirstLoadProbe, { target, props: { responseDelay: 0 } });
		flushSync();
		// Pre-fix this read "empty": showSkeleton is false until the 200ms timer.
		expect(target.textContent?.trim()).toBe('loading');
		await vi.waitFor(() => expect(target.textContent?.trim()).toBe('Book 1'));
		unmount(component);
		target.remove();
	});
});
