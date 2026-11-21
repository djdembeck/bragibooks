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

/** A book with a persisted manual edit, so Reset has something to delete. */
const resAWithOverrides = {
	...resA,
	overrides: {
		title: 'Saved title',
		subtitle: '',
		authors: null,
		narrators: null,
		series_name: '',
		series_position: '',
		description: '',
		genres: null,
		year: null,
		cleared: [],
		cover_source: '' as const,
		cover_file: '',
		cover_url: '',
		chapters: null,
		updated_at: '2026-01-01T00:00:00Z'
	}
} satisfies BookMetadataResponse;

function titleInput(el: HTMLElement): HTMLInputElement {
	const input = el.querySelector<HTMLInputElement>('#meditor-title');
	if (!input) throw new Error('title input not rendered');
	return input;
}

function tagCheckbox(el: HTMLElement, label: string): HTMLInputElement {
	const input = el.querySelector<HTMLInputElement>(
		`.meditor__tagpolicy-row input[aria-label="${label}"]`
	);
	if (!input) throw new Error('tag-policy checkbox not rendered');
	return input;
}

/** Mount the editor; `open` defaults to true (the parent shell is open)
 *  so the existing Ctrl+S tests keep their shortcut available. */
function mountEditor(target: HTMLElement, bookId: number, pathFormat: string | null, open = true) {
	return mount(MetadataEditor as Component, { target, props: { bookId, pathFormat, open } });
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
		expect(titleInput(target).value).toBe('Book B');
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
		// Book 1 loaded; dirty the draft, start a save, and navigate away
		// BEFORE the save resolves. Save is intentionally disabled while clean.
		releaseNav(resA);
		await settle();
		const input = titleInput(target);
		input.value = 'Dirty A';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
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
		expect(titleInput(target).value).toBe('Book B');
		expect(onSavedCalls).toBe(0);
		const stillSaving = Array.from(target.querySelectorAll<HTMLButtonElement>('button'))
			.find((b) => b.textContent?.includes('Save manual metadata'));
		expect(stillSaving?.getAttribute('aria-busy')).not.toBe('true');
		expect(target.textContent).toContain('No saved edits yet');

		unmount(comp);
		target.remove();
	});

	it('blocks reset while a save is pending', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata'
				? Promise.resolve(resAWithOverrides)
				: Promise.reject(new Error(`unexpected GET ${url}`));
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

		const resetBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Reset to catalog')
		);
		expect(resetBtn, 'reset button not found').toBeDefined();
		expect(resetBtn!.disabled).toBe(false);

		// Save is intentionally disabled while clean; dirty the draft first.
		const input = titleInput(target);
		input.value = 'Dirty A';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		const saveBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Save manual metadata')
		);
		saveBtn!.click();
		flushSync();
		// While the save is in flight, the reset entry point is disabled:
		// committing a reset under a pending save would resurrect the
		// save's edits on its resolution.
		expect(resetBtn!.disabled).toBe(true);

		// Resolving the save re-enables it.
		releaseSave(resAWithOverrides);
		await settle();
		expect(resetBtn!.disabled).toBe(false);

		unmount(comp);
		target.remove();
	});

	it('saves with Ctrl+S while the draft is dirty', async () => {
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
		const input = titleInput(target);
		input.value = 'Dirty via shortcut';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		window.dispatchEvent(
			new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true, cancelable: true })
		);
		flushSync();
		expect(calls.filter((c) => c.method === 'put' && c.url === '/books/1/metadata').length).toBe(1);
		releaseSave(resA);
		await settle();
		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor prefill and partial save', () => {
	// A stored override row whose scalar is EMPTY: the "no override" zero
	// value (Go marshals it as "" with no omitempty). The editor must
	// prefill the CATALOG value from the server-computed effective state,
	// not a blank (finding: prefill falls through to blank).
	const resEmptyScalarOverride = {
		...resA,
		overrides: {
			title: '',
			subtitle: '',
			authors: null,
			narrators: null,
			series_name: '',
			series_position: '',
			description: '',
			genres: null,
			year: null,
			cleared: [],
			cover_source: '' as const,
			cover_file: '',
			cover_url: '',
			chapters: null,
			updated_at: '2026-01-01T00:00:00Z'
		},
		effective: snap({ book_id: 1, title: 'Book A', authors: ['Auth A'] })
	} satisfies BookMetadataResponse;

	it('prefills catalog values over an override row with empty scalars', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata'
				? Promise.resolve(resEmptyScalarOverride)
				: Promise.reject(new Error(`unexpected GET ${url}`));
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
		// The empty-scalar override row must not pin the field to blank:
		// the catalog title is shown, and the authors list carries the
		// catalog's one author (not the "No authors yet." empty state).
		expect(titleInput(target).value).toBe('Book A');
		const authorInput = target.querySelector<HTMLInputElement>('.meditor__input--person');
		expect(authorInput, 'author input not rendered').not.toBeNull();
		expect(authorInput!.value).toBe('Auth A');
		expect(target.textContent).toContain('All changes saved');

		// The save payload keeps the STORED row's values for every field
		// the operator did not touch — the effective (catalog) value must
		// not be pinned as an override, or a re-capture would no longer
		// update that field (finding: first save pins the whole capture).
		const input = titleInput(target);
		input.value = 'New Title';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		const saveBtn = Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find(
			(b) => b.textContent?.includes('Save manual metadata')
		);
		saveBtn!.click();
		flushSync();
		const puts = calls.filter((c) => c.method === 'put' && c.url === '/books/1/metadata');
		expect(puts.length).toBe(1);
		const body = puts[0].body as Record<string, unknown>;
		expect(body.title).toBe('New Title'); // the edited field
		expect(body.authors).toBeNull(); // unchanged list: stored value (no override)
		expect(body.year).toBeNull(); // unchanged year: no override
		expect(body.series_name).toBe(''); // unchanged scalar: stored value
		expect(body.cover_source).toBe(''); // unchanged cover: stored value
		releaseSave(resEmptyScalarOverride);
		await settle();

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor Ctrl+S shell gating', () => {
	it('ignores Ctrl+S while the parent shell is closed', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`unexpected GET ${url}`));
		const { promise: save, resolve: releaseSave } = Promise.withResolvers<BookMetadataResponse>();
		putImpl = (url) => {
			if (url === '/books/1/metadata') return save;
			throw new Error(`unexpected PUT ${url}`);
		};
		const target = document.createElement('div');
		document.body.appendChild(target);
		// open=false: the parent <details> shell is closed, so the
		// page-wide listener must not fire (and must not swallow the
		// keystroke) even though the draft is dirty.
		const comp = mountEditor(target, 1, null, false);
		flushSync();
		await settle();
		const input = titleInput(target);
		input.value = 'Dirty while closed';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		window.dispatchEvent(
			new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true, cancelable: true })
		);
		flushSync();
		expect(calls.filter((c) => c.method === 'put' && c.url === '/books/1/metadata').length).toBe(0);
		releaseSave(resA);
		await settle();

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
		expect(titleInput(target).value).toBe('Book A'); // catalog title is the snapshot default

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
		// The tag policy lives in one disclosure; the checkbox does not
		// require entering list edit mode.
		const clearBox = tagCheckbox(target, 'Remove author tags from output');
		expect(clearBox.checked).toBe(false);
		clearBox.click();
		flushSync();
		expect(clearBox.checked).toBe(true);
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

describe('MetadataEditor tag-removal toggle on unedited scalars', () => {
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

		// The title is unedited (blank). The tag-policy checkbox must still
		// be usable: clearing the catalog value is a distinct operator intent
		// from editing, so it is never gated on an edited value.
		const toggle = tagCheckbox(target, 'Remove title tag from output');
		const policyCount = () =>
			target.querySelector('.meditor__tagpolicy-count')?.textContent?.trim();
		expect(toggle.disabled).toBe(false);
		expect(toggle.checked).toBe(false);
		expect(toggle.getAttribute('aria-label')).toBe('Remove title tag from output');
		expect(toggle.closest('label')?.textContent).toContain('Title');
		expect(policyCount()).toBe('None omitted');

		// Ticking marks the clear intent; the payload keeps the effective
		// catalog value for naming (the server clears the atom).
		toggle.click();
		flushSync();
		expect(toggle.checked).toBe(true);
		expect(policyCount()).toBe('Title');
		await vi.advanceTimersByTimeAsync(250);
		flushSync();
		const last = previewBodies()[previewBodies().length - 1];
		expect(last.title).toBe('Book A');
		// Unticking removes the intent.
		toggle.click();
		flushSync();
		expect(toggle.checked).toBe(false);
		expect(policyCount()).toBe('None omitted');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor saved status and discard', () => {
	it('marks the draft dirty and restores the saved values without a save', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview' ? Promise.resolve({ relative_path: 'x' }) : Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();

		const findButton = (text: string) =>
			Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find((b) =>
				b.textContent?.includes(text)
			);
		const saveBtn = findButton('Save manual metadata');
		const resetBtn = findButton('Reset to catalog');
		expect(saveBtn, 'save button not found').toBeDefined();
		expect(resetBtn, 'reset button not found').toBeDefined();
		expect(saveBtn!.disabled).toBe(true);
		expect(resetBtn!.disabled).toBe(true); // no saved overrides to reset
		expect(target.textContent).toContain('No saved edits yet');

		const input = titleInput(target);
		input.value = 'Dirty A';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		flushSync();
		expect(target.textContent).toContain('Unsaved changes');
		expect(saveBtn!.disabled).toBe(false);

		findButton('Discard changes')!.click();
		flushSync();
		expect(target.textContent).toContain('Discard unsaved changes');
		findButton('Yes, discard')!.click();
		flushSync();
		expect(titleInput(target).value).toBe('Book A');
		expect(target.textContent).toContain('No saved edits yet');
		const saveAfterDiscard = findButton('Save manual metadata');
		expect(saveAfterDiscard!.disabled).toBe(true);

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor genreless snapshot', () => {
	// The server used to serve snapshot.genres as JSON null for a catalog
	// book with NO genres; the editor's catalog line called
	// snap.genres.join(', ') and the whole book page died with a SvelteKit
	// 500. The genre block must render its empty state instead.
	it('renders an editable empty state when the snapshot has no genres', async () => {
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

		const genresBlock = target.querySelector('#meditor-genres-label')?.closest('fieldset');
		expect(genresBlock, 'genres section not found').toBeDefined();
		expect(genresBlock!.textContent).toContain('No genres yet.');
		expect(genresBlock!.textContent).toContain('Add genre');

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

describe('MetadataEditor chapter time inputs', () => {
	it('accepts h:mm:ss and shows the parsed time', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview'
				? Promise.resolve({ relative_path: 'x' })
				: Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();
		const findButton = (text: string) =>
			Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find((b) =>
				b.textContent?.includes(text)
			);
		findButton('Add chapter')!.click();
		flushSync();
		const start = target.querySelector<HTMLInputElement>('.meditor__chapter-start');
		expect(start, 'chapter start input not rendered').not.toBeNull();
		expect(start!.value).toBe('0:00');
		start!.value = '0:04:12';
		start!.dispatchEvent(new Event('change', { bubbles: true }));
		flushSync();
		const startAfter = target.querySelector<HTMLInputElement>('.meditor__chapter-start');
		expect(startAfter!.value).toBe('4:12');
		const offset = target.querySelector<HTMLInputElement>('#meditor-chapter-offset');
		expect(offset, 'chapter offset input not rendered').not.toBeNull();
		offset!.value = '-0:01:00';
		offset!.dispatchEvent(new Event('input', { bubbles: true }));
		findButton('Apply')!.click();
		flushSync();
		expect(target.querySelector<HTMLInputElement>('.meditor__chapter-start')!.value).toBe('3:12');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor chapter keyboard reorder', () => {
	it('moves a chapter with Alt+Arrow on its drag handle', async () => {
		const resChapters = {
			...BASE,
			snapshot: snap({
				chapters: [
					{ title: 'Chapter One', start_millis: 0, duration_millis: 1000 },
					{ title: 'Chapter Two', start_millis: 1000, duration_millis: 1000 },
					{ title: 'Chapter Three', start_millis: 2000, duration_millis: 1000 }
				]
			})
		} as BookMetadataResponse;
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resChapters) : Promise.reject(new Error(`GET ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, null);
		flushSync();
		await settle();

		const grip = target.querySelector<HTMLButtonElement>('[data-chapter-grip="0"]');
		expect(grip, 'chapter drag handle not rendered').not.toBeNull();
		grip!.dispatchEvent(
			new KeyboardEvent('keydown', { key: 'ArrowDown', altKey: true, bubbles: true, cancelable: true })
		);
		await settle();
		const titles = Array.from(target.querySelectorAll<HTMLInputElement>('.meditor__chapter-title')).map(
			(input) => input.value
		);
		expect(titles.slice(0, 3)).toEqual(['Chapter Two', 'Chapter One', 'Chapter Three']);
		expect(document.activeElement).toBe(target.querySelector('[data-chapter-grip="1"]'));

		unmount(comp);
		target.remove();
	});

	it('expands the window when a chapter moves past the fifth row', async () => {
		const resSix = {
			...BASE,
			snapshot: snap({
				chapters: [
					{ title: 'Chapter One', start_millis: 0, duration_millis: 1000 },
					{ title: 'Chapter Two', start_millis: 1000, duration_millis: 1000 },
					{ title: 'Chapter Three', start_millis: 2000, duration_millis: 1000 },
					{ title: 'Chapter Four', start_millis: 3000, duration_millis: 1000 },
					{ title: 'Chapter Five', start_millis: 4000, duration_millis: 1000 },
					{ title: 'Chapter Six', start_millis: 5000, duration_millis: 1000 }
				]
			})
		} as BookMetadataResponse;
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resSix) : Promise.reject(new Error(`GET ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, null);
		flushSync();
		await settle();

		const grip = target.querySelector<HTMLButtonElement>('[data-chapter-grip="4"]');
		expect(grip, 'fifth chapter drag handle not rendered').not.toBeNull();
		grip!.dispatchEvent(
			new KeyboardEvent('keydown', { key: 'ArrowDown', altKey: true, bubbles: true, cancelable: true })
		);
		await settle();
		const titles = Array.from(target.querySelectorAll<HTMLInputElement>('.meditor__chapter-title')).map(
			(input) => input.value
		);
		expect(titles).toHaveLength(6);
		expect(titles[5]).toBe('Chapter Five');
		expect(target.textContent).toContain('Show first 5 chapters');
		expect(document.activeElement).toBe(target.querySelector('[data-chapter-grip="5"]'));

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor genre add control', () => {
	it('adds a genre from the visible button', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview'
				? Promise.resolve({ relative_path: 'x' })
				: Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();
		const findButton = (text: string) =>
			Array.from(target.querySelectorAll<HTMLButtonElement>('button')).find((b) =>
				b.textContent?.includes(text)
			);
		const add = target.querySelector<HTMLInputElement>('.meditor__input--add');
		expect(add, 'genre add input not rendered').not.toBeNull();
		add!.value = 'Space Opera';
		findButton('Add genre')!.click();
		flushSync();
		expect(target.textContent).toContain('Space Opera');
		expect(add!.value).toBe('');

		unmount(comp);
		target.remove();
	});
});

describe('MetadataEditor omission summary', () => {
	it('names every omitted field past three', async () => {
		getImpl = (url) =>
			url === '/books/1/metadata' ? Promise.resolve(resA) : Promise.reject(new Error(`GET ${url}`));
		postImpl = (url) =>
			url === '/engine/preview'
				? Promise.resolve({ relative_path: 'x' })
				: Promise.reject(new Error(`POST ${url}`));
		const target = document.createElement('div');
		document.body.appendChild(target);
		const comp = mountEditor(target, 1, '{title}');
		flushSync();
		await settle();
		for (const label of [
			'Remove title tag from output',
			'Remove subtitle tag from output',
			'Remove author tags from output',
			'Remove narrator tags from output'
		]) {
			tagCheckbox(target, label).click();
			flushSync();
		}
		expect(target.querySelector('.meditor__tagpolicy-count')?.textContent?.trim()).toBe(
			'Title, Subtitle, Authors, Narrators'
		);
		expect(target.querySelector('label[for="meditor-title"]')?.textContent).toContain('Tag omitted');
		expect(target.querySelector('label[for="meditor-subtitle"]')?.textContent).toContain('Tag omitted');

		unmount(comp);
		target.remove();
	});
});
