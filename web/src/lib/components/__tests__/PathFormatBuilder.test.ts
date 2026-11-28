import { flushSync, mount, unmount, type Component } from 'svelte';
import { setEngineTokens } from '#lib/scheme.js';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PFBProbe from './PFBProbe.svelte';

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));
vi.mock('#lib/api.js', () => ({ get: vi.fn(), post: postMock, put: vi.fn(), del: vi.fn(), ApiError: class ApiError extends Error {} }));

type SetOutputDir = (dir: string) => void;

afterEach(() => {
	vi.useRealTimers();
	postMock.mockReset();
});

describe('PathFormatBuilder example root', () => {
	it('re-renders the preview root when outputDir changes after the response lands', async () => {
		// The engine preview resolves a RELATIVE path; the displayed root
		// is derived from the CURRENT outputDir, so a directory change
		// after the preview resolved must repaint immediately (a
		// callback-only read of outputDir would keep the stale root).
		vi.useFakeTimers();
		setEngineTokens([
			{ name: 'title', value: 'title', example: 'T' },
			{ name: 'author', value: 'author', example: 'A' }
		]);
		postMock.mockResolvedValue({ relative_path: 'A/T.m4a' });
		let setOutputDir: SetOutputDir = () => {};
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mount(PFBProbe as Component, {
			target,
			props: {
				value: '{author}/{title}',
				initialOutputDir: '/old/shelf',
				onInit: (set: SetOutputDir) => (setOutputDir = set)
			}
		});
		flushSync();
		// Let the debounced preview resolve.
		await vi.advanceTimersByTimeAsync(300);
		flushSync();
		const code = target.querySelector<HTMLElement>('.pbf-preview');
		expect(code).not.toBeNull();
		expect(code!.textContent).toBe('/old/shelf/A/T.m4a');

		// Change the directory AFTER the response landed: the root must
		// follow with no new preview request.
		const postsBefore = postMock.mock.calls.length;
		setOutputDir('/new/shelf');
		flushSync();
		expect(code!.textContent).toBe('/new/shelf/A/T.m4a');
		expect(postMock.mock.calls.length).toBe(postsBefore); // no refetch

		unmount(comp);
		target.remove();
	});
});
