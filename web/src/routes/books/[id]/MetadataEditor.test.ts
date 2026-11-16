import { flushSync, mount, unmount, type Component } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BookMetadataResponse } from '#lib/types.js';
import MetadataEditor from './MetadataEditor.svelte';
import NavHarness from './MetadataEditorNavHarness.svelte';

type SetBookId = (id: number) => void;

/* ── Mockable api.js ────────────────────────────────────────────
 * The mock records every call (url + body) and defers resolution
 * through stored resolvers so tests can hold an in-flight fetch
 * across a book-ID navigation and prove the stale response is
 * discarded. pathFormat: null disables the preview effect (no
 * POSTs) in the navigation test; a real format enables it in the
 * preview test.
 */

let getImpl: ((url: string) => Promise<unknown>) | null = null;
let postImpl: ((url: string, body: unknown) => Promise<unknown>) | null = null;
let putImpl: ((url: string, body: unknown) => Promise<unknown>) | null = null;
const calls: { method: 'get' | 'post' | 'put'; url: string; body?: unknown }[] = [];

vi.mock('#lib/api.js', () => ({
	get: (url: string) => {
		calls.push({ method: 'get', url });
		if (!getImpl) throw new Error('test did not provide a get implementation');
		return getImpl(url);
	},
	post: (url: string, body: unknown) => {
		calls.push({ method: 'post', url, body });
		if (!postImpl) throw new Error('test did not provide a post implementation');
		return postImpl(url, body);
	},
	put: (url: string, body: unknown) => {
		calls.push({ method: 'put', url, body });
		if (!putImpl) throw new Error('test did not provide a put implementation');
		return putImpl(url, body);
	},
	ApiError: class ApiError extends Error {}
}));

function snap(over: Partial<BookMetadataResponse['snapshot']> = {}): NonNullable<BookMetadataResponse['snapshot']> {
	return {
		book_id: 1,
		audiobookdb_book_id: '',
		audiobookdb_release_id: '',
		title: 'Book A',
		subtitle: '',
		authors: ['Auth A'],
		narrators: ['Narr A'],
		series_name: '',
		series_position: '',
		description: '',
		genres: [],
		year: null,
		cover_file: '',
		chapters: null,
		captured_at: '2026-01-01T00:00:00Z',
		...over
	};
}

const BASE = { overrides: null, effective: null, effective_chapters: null };
const resA = { ...BASE, snapshot: snap({ book_id: 1, title: 'Book A' }) } satisfies BookMetadataResponse;
const resB = { ...BASE, snapshot: snap({ book_id: 2, title: 'Book B' }) } satisfies BookMetadataResponse;
const resNoSnap = { ...BASE, snapshot: null } satisfies BookMetadataResponse;

function titleInput(el: HTMLElement): HTMLInputElement {
	const input = el.querySelector<HTMLInputElement>('#meditor-title');
	if (!input) throw new Error('title input not rendered');
	return input;
}

function mountEditor(target: HTMLElement, bookId: number, pathFormat: string | null) {
	return mount(MetadataEditor as Component, { target, props: { bookId, pathFormat } });
}

/** Flush pending microtasks so resolved fetches reach their .then. */
async function settle() {
	await vi.advanceTimersByTimeAsync(0);
	flushSync();
}

function previewBodies(): Record<string, unknown>[] {
	return calls
		.filter((c) => c.url === '/engine/preview')
		.map((c) => c.body as { metadata: Record<string, unknown> })
		.map((b) => b.metadata);
}

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	calls.length = 0;
	getImpl = null;
	postImpl = null;
	putImpl = null;
	vi.useRealTimers();
});

describe('MetadataEditor book-ID navigation isolation', () => {
	it('reloads on bookId change and discards the stale book\'s response', async () => {
		const { promise: nav, resolve: releaseNav } = Promise.withResolvers<BookMetadataResponse>();
		const { promise: b2, resolve: resolveB2 } = Promise.withResolvers<BookMetadataResponse>();
		getImpl = (url) => {
			if (url === '/books/1/metadata') return nav;
			if (url === '/books/2/metadata') return b2;
			throw new Error(`unexpected GET ${url}`);
		};
		let setBookId: SetBookId = () => {};
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mount(NavHarness as Component, {
			target,
			props: { onInit: (set: SetBookId) => (setBookId = set) }
		});
		flushSync();
		expect(calls.filter((c) => c.url === '/books/1/metadata').length).toBe(1);

		// Navigate to book 2 while book 1's response is STILL in flight:
		// the navigation must wipe book 1's state and start book 2's own
		// fetch.
		setBookId(2);
		flushSync();
		expect(calls.filter((c) => c.url === '/books/2/metadata').length).toBe(1);

		// The LATE book-1 response arrives after the navigation: it must
		// be discarded, not written into book 2's state.
		releaseNav(resA);
		await settle();
		expect(target.textContent).not.toContain('Book A');
		expect(calls.filter((c) => c.url === '/books/2/metadata').length).toBe(1); // no refetch

		// Book 2's own response populates the state.
		resolveB2(resB);
		await settle();
		expect(target.textContent).toContain('Book B');
		expect(target.textContent).not.toContain('Book A');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor save/reset races', () => {
	it('discards a save that outlives the book-ID navigation', async () => {
		const { promise: nav, resolve: releaseNav } = Promise.withResolvers<BookMetadataResponse>();
		const { promise: b2, resolve: resolveB2 } = Promise.withResolvers<BookMetadataResponse>();
		const { promise: saveA, resolve: releaseSaveA } = Promise.withResolvers<BookMetadataResponse>();
		getImpl = (url) => {
			if (url === '/books/1/metadata') return nav;
			if (url === '/books/2/metadata') return b2;
			throw new Error(`unexpected GET ${url}`);
		};
		putImpl = (url) => {
			if (url === '/books/1/metadata') return saveA;
			throw new Error(`unexpected PUT ${url}`);
		};
		let setBookId: SetBookId = () => {};
		let onSavedCalls = 0;
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mount(NavHarness as Component, {
			target,
			props: {
				onInit: (set: SetBookId) => (setBookId = set),
				onSaved: () => onSavedCalls++
			}
		});
		flushSync();
		// Book 1 loaded; start a save and navigate away BEFORE it resolves.
		releaseNav(resA);
		await settle();
		const saveBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Save manual metadata')
		);
		expect(saveBtn, 'save button not found').toBeDefined();
		saveBtn!.click();
		flushSync();
		setBookId(2);
		flushSync();
		resolveB2(resB);
		await settle();
		// The LATE book-1 save resolves: it must not paint book A's
		// response into book B's state, must not fire book A's onSaved on
		// B, and must not leave a stuck saving flag behind.
		releaseSaveA(resA);
		await settle();
		expect(target.textContent).toContain('Book B');
		expect(onSavedCalls).toBe(0);
		const stillSaving = Array.from(target.querySelectorAll<HTMLButtonElement>('button'))
			.find((b) => b.textContent?.includes('Save manual metadata'));
		expect(stillSaving?.disabled).not.toBe(true);

		unmount(comp);
		target.remove();
	});

	it('blocks reset while a save is pending', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`unexpected GET ${url}`));
		const { promise: save, resolve: releaseSave } = Promise.withResolvers<BookMetadataResponse>();
		putImpl = (url) => {
			if (url === '/books/1/metadata') return save;
			throw new Error(`unexpected PUT ${url}`);
		};
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, null);
		flushSync();
		await settle();

		const saveBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Save manual metadata')
		);
		saveBtn!.click();
		flushSync();
		// While the save is in flight, the reset entry point is disabled:
		// committing a reset under a pending save would resurrect the
		// save's edits on its resolution.
		const resetBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Reset to catalog')
		);
		expect(resetBtn, 'reset button not found').toBeDefined();
		expect(resetBtn!.disabled).toBe(true);

		// Resolving the save re-enables it.
		releaseSave(resA);
		await settle();
		expect(resetBtn!.disabled).toBe(false);

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor naming preview payload', () => {
	it('sends the effective payload on nested draft edits and applies people clears', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata'
				? Promise.resolve(resA)
				: Promise.reject(new Error(`unexpected GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview'
				? Promise.resolve({ relative_path: 'A/B.m4a' })
				: Promise.reject(new Error(`unexpected POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();
		expect(titleInput(target).value).toBe(''); // catalog title is the snapshot default

		// Catalog-state preview: the first debounced POST carries the
		// effective (catalog) payload.
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const first = previewBodies()[0];
		expect(first, 'no preview posted').toBeDefined();
		expect(first.title).toBe('Book A');
		expect(first.authors).toEqual(['Auth A']);

		// Edit the title: the NEXT POST must carry the edited value —
		// this only works because the payload is computed synchronously
		// from the current draft fields (the effect re-runs on the
		// nested draft edit, and the timer captures that payload).
		titleInput(target).value = 'Edited A';
		titleInput(target).dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const bodies = previewBodies();
		expect(bodies[bodies.length - 1].title).toBe('Edited A');

		// Clear the authors tag: the preview must blank the people list,
		// matching the server's applyMetadataOverride effective state.
		const fieldsets = Array.from(target.querySelectorAll<HTMLFieldSetElement>('fieldset'));
		const authorsBlock = fieldsets.find((f) => f.querySelector('.meditor__block-title')?.textContent?.trim() === 'Authors');
		expect(authorsBlock, 'authors section not found').toBeDefined();
		// An unedited list shows only "Edit list": reveal the list (which
		// seeds it from the catalog) to expose the Clear tag button.
		const editBtn = Array.from(authorsBlock!.querySelectorAll<HTMLButtonElement>('button')).find((b) =>
			b.textContent?.includes('Edit list')
		);
		expect(editBtn, 'authors "Edit list" button not found').toBeDefined();
		editBtn!.click();
		flushSync();
		const clearBtn = authorsBlock!.querySelector<HTMLButtonElement>('.meditor__clearbtn');
		expect(clearBtn, 'authors clear button not found').toBeDefined();
		clearBtn!.click();
		flushSync();
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const clearedBodies = previewBodies();
		const clearedMeta = clearedBodies[clearedBodies.length - 1];
		// The cleared tag is honored on the SERVER: the server's
		// applyMetadataOverride pins the clear policy while keeping the
		// effective (catalog) value for naming, so the preview payload
		// must carry the kept value — not the catalog list. (An empty
		// EDITED list, i.e. a list the operator deliberately emptied,
		// would preview as [].)
		expect(clearedMeta.authors).toEqual(['Auth A']);
		// Clears do not blank the effective scalar values in the payload:
		// the output is still named from the kept value.
		expect(clearedMeta.title).toBe('Edited A');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor clear-tag on unedited scalars', () => {
	it('lets the operator clear a catalog scalar with no edited value', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview' ? Promise.resolve({ relative_path: 'x' }) : Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();

		// The title is unedited (blank). The Clear tag button must still be
		// usable: clearing the catalog value is a distinct operator intent
		// from editing, so the button is never gated on an edited value.
		const titleBlock = Array.from(target.querySelectorAll<HTMLFieldSetElement>('fieldset')).find(
			(f) => f.querySelector('.meditor__block-title')?.textContent?.trim() === 'Title'
		) as HTMLFieldSetElement;
		const btn = titleBlock.querySelector<HTMLButtonElement>('.meditor__clearbtn');
		expect(btn, 'title clear button not found').toBeDefined();
		expect(btn!.disabled).toBe(false);

		// Toggling on marks the clear intent (is-on); the payload keeps the
		// effective catalog value for naming (the server clears the atom).
		btn!.click();
		flushSync();
		expect(btn!.className).toContain('is-on');
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const last = previewBodies()[previewBodies().length - 1];
		expect(last.title).toBe('Book A');
		// Toggling off removes the intent.
		btn!.click();
		flushSync();
		expect(btn!.className).not.toContain('is-on');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor genreless snapshot', () => {
	// The server used to serve snapshot.genres as JSON null for a catalog
	// book with NO genres; the editor's "Catalog: …" line called
	// snap.genres.join(', ') and the whole book page died with a SvelteKit
	// 500. The genre block must render its empty state instead.
	it('renders the catalog-genres empty state when the snapshot has no genres', async () => {
		const resNoGenres = {
			...BASE,
			snapshot: snap({ genres: null as unknown as string[] })
		} as BookMetadataResponse;
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resNoGenres) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview' ? Promise.resolve({ relative_path: 'x' }) : Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();

		const genresBlock = Array.from(target.querySelectorAll<HTMLFieldSetElement>('fieldset')).find(
			(f) => f.querySelector('.meditor__block-title')?.textContent?.trim() === 'Genres'
		);
		expect(genresBlock, 'genres section not found').toBeDefined();
		expect(genresBlock!.textContent).toContain('Catalog:');
		expect(genresBlock!.textContent).toContain('—');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor empty-title preview guard', () => {
	// The engine rejects a blank effective title ("effective title is
	// empty"), so previewing it can only produce a 502. A book whose
	// catalog title is blank must not POST a guaranteed-failing preview.
	it('posts no engine preview while the effective title is blank', async () => {
		const resNoTitle = {
			...BASE,
			snapshot: snap({ title: '' })
		} as BookMetadataResponse;
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resNoTitle) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview' ? Promise.resolve({ relative_path: 'x' }) : Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		expect(calls.filter((c) => c.url === '/engine/preview').length).toBe(0);

		// A typed title unblocks the preview: the POST must arrive with it.
		const input = titleInput(target);
		input.value = 'Typed Title';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const bodies = previewBodies();
		expect(bodies.length).toBe(1);
		expect(bodies[0].title).toBe('Typed Title');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor no-capture (snapshot null) state', () => {
	it('renders the unavailable-catalog state without crashing and keeps the editor usable', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resNoSnap) : Promise.reject(new Error(`GET ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, null);
		flushSync();
		await settle();
		expect(target.textContent).toContain('no captured catalog record');
		expect(target.textContent).toContain('—');
		// The editor is still reachable: typing works with no snapshot.
		const input = titleInput(target);
		expect(input.value).toBe('');
		input.value = 'Typed Title';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		expect(titleInput(target).value).toBe('Typed Title');

		unmount(comp);
		target.remove();
	});
});
