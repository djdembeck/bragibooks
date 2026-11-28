import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BooksListResponse, BookWithPeople } from '#lib/types.js';
// vi.mock hoists above these, so the factories can name the stubs directly.
import ButtonStub from './ButtonStub.svelte';
import ModalStub from './ModalStub.svelte';

// T18: a filter/search change mid-batch is a stale-criteria signal for
// every bulk operation. The done and remove loops must break on the first
// stale tick (no further PUTs/DELETEs) and must NOT re-fetch the catalog
// (the URL effect already reloaded it); the requeue dispatch must be
// discarded after its per-book fetch when the criteria changed in flight.
const { getMock, postMock, putMock, delMock, gotoMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn(),
	delMock: vi.fn(),
	gotoMock: vi.fn()
}));

// The page syncs filter/page/q from the URL $app/state. The real router
// rewrites the page URL when goto() is called, and the page's URL $effect
// re-runs on the filter churn BEFORE any URL change — so the mock goto
// must apply the URL synchronously (exactly as SvelteKit does), or the
// effect sees the pre-navigation URL and reverts the filter.
const pageFixture = vi.hoisted(() => ({
	url: new URL('http://localhost/books/1'),
	params: {}
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: putMock,
	del: delMock,
	ApiError: class ApiError extends Error {}
}));
vi.mock('$app/navigation', () => ({
	goto: (path: string) => {
		pageFixture.url = new URL('http://localhost' + path);
		return gotoMock(path);
	}
}));
vi.mock('$app/state', () => ({ page: pageFixture }));
// The bulk contracts under test live in the script; the page composes many
// presentational components, so they stub out (the row checkboxes and the
// plain filter/bulk buttons render from the page's own markup).
vi.mock('#lib/components/index.js', () => ({
	PageHeader: () => null,
	StatusBadge: () => null,
	Skeleton: () => null,
	EmptyState: () => null,
	Alert: () => null,
	ErrorDetail: () => null,
	CoverArt: () => null,
	OutputProfileSelector: () => null,
	Button: ButtonStub,
	Modal: ModalStub
}));

import BooksPage from './+page.svelte';

function bookFixture(id: number): BookWithPeople {
	return {
		id,
		title: `Book ${id}`,
		asin: '',
		audiobookdb_book_id: '',
		audiobookdb_release_id: '',
		description: '',
		release_date: '',
		series: '',
		publisher: '',
		language: '',
		runtime_length_minutes: 60,
		format_type: '',
		src_path: `/in/book${id}.mp3`,
		dest_path: '',
		status: 'pending',
		status_message: '',
		cover_image_url: '',
		created_at: '',
		updated_at: '',
		authors: [{ id: 1, book_id: id, name: 'Author', role: 'author', audiobookdb_person_id: null, created_at: '' }],
		narrators: []
	};
}

function listResponse(): BooksListResponse {
	return { books: [bookFixture(1), bookFixture(2), bookFixture(3)], total: 3, page: 0, limit: 20 };
}

function wireApi() {
	getMock.mockImplementation(async (path: string) => {
		if (path.startsWith('/books?')) return listResponse();
		if (path === '/output-profiles') return { profiles: [] };
		if (path === '/jobs?limit=1') return { jobs: [] };
		if (/^\/books\/\d+$/.test(path)) return bookFixture(Number(path.split('/')[2]));
		return {};
	});
}

describe('books bulk operations under a mid-batch criteria change', () => {
	let target: HTMLElement;
	let component: Record<string, unknown>;

	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
		putMock.mockReset();
		delMock.mockReset();
		gotoMock.mockReset();
		pageFixture.url = new URL('http://localhost/books/1');
		wireApi();
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
	});

	async function mountPage() {
		component = mount(BooksPage, { target });
		flushSync();
		// The catalog load chain (list response + profiles) settles over
		// several microtask/macrotask turns.
		await vi.waitFor(
			() => {
				expect(target.querySelector('input[aria-label="Select Book 1"]')).not.toBeNull();
			},
			{ timeout: 4000 }
		);
	}

	function listGetCount(): number {
		return getMock.mock.calls.filter((c) => typeof c[0] === 'string' && c[0].startsWith('/books?')).length;
	}

	async function selectAllRows() {
		for (const id of [1, 2, 3]) {
			const box = target.querySelector<HTMLInputElement>(`input[aria-label="Select Book ${id}"]`);
			if (!box) throw new Error(`row checkbox for book ${id} not rendered`);
			box.checked = true;
			box.dispatchEvent(new Event('change', { bubbles: true }));
		}
		flushSync();
	}

	async function changeFilter() {
		// The filter click runs setFilter (criteria change + selection
		// clear + goto); the mocked router applies the URL synchronously,
		// which the page's URL effect observes as a no-op (URL agrees with
		// the new criteria — no second clear).
		const btn = [...target.querySelectorAll<HTMLButtonElement>('button')].find(
			(b) => b.textContent?.trim() === 'Matched'
		);
		if (!btn) throw new Error('Matched filter button not rendered');
		btn.click();
		flushSync();
	}

	function bulkButton(label: string): HTMLButtonElement {
		const btn = [...target.querySelectorAll<HTMLButtonElement>('button')].find(
			(b) => b.textContent?.trim() === label
		);
		if (!btn) throw new Error(`bulk button "${label}" not rendered`);
		return btn;
	}

	it('mark-done: a mid-batch criteria change stops the loop and skips the re-fetch', async () => {
		await mountPage();
		await selectAllRows();
		const gate = Promise.withResolvers<void>();
		putMock.mockImplementation((path: string) =>
			path === '/books/1' ? gate.promise : Promise.resolve()
		);

		bulkButton('Mark as done').click();
		await Promise.resolve();
		await Promise.resolve();
		expect(putMock.mock.calls.map((c) => c[0])).toEqual(['/books/1']); // loop parked on book 1

		await changeFilter();

		gate.resolve();
		await Promise.resolve();
		await Promise.resolve();
		await Promise.resolve();

		// The stale break: books 2 and 3 were never PUT.
		expect(putMock.mock.calls.map((c) => c[0])).toEqual(['/books/1']);
		// The stale guard: the filter change's own URL-effect re-fetch is
		// the ONLY post-batch catalog read (a regression re-fetches again).
		expect(listGetCount()).toBe(2);
		expect(target.textContent).not.toContain('marked as done');
	});

	it('remove: a mid-batch criteria change stops the delete loop and skips the re-fetch', async () => {
		await mountPage();
		await selectAllRows();
		const gate = Promise.withResolvers<void>();
		delMock.mockImplementation((path: string) =>
			path === '/books/1' ? gate.promise : Promise.resolve()
		);
		// Open the confirmation (all books are on-page: no per-book fetch).
		bulkButton('Remove').click();
		await vi.waitFor(
			() => {
				expect(bulkButton('Remove 3 books')).toBeDefined();
			},
			{ timeout: 4000 }
		);
		bulkButton('Remove 3 books').click();
		await vi.waitFor(
			() => {
				expect(delMock.mock.calls.map((c) => c[0])).toEqual(['/books/1']);
			},
			{ timeout: 4000 }
		); // loop parked on book 1

		await changeFilter();

		gate.resolve();
		await vi.waitFor(() => expect(delMock.mock.calls.length).toBe(1), { timeout: 4000 });
		await Promise.resolve();
		await Promise.resolve();

		expect(delMock.mock.calls.map((c) => c[0])).toEqual(['/books/1']);
		expect(listGetCount()).toBe(2);
		expect(target.textContent).not.toContain('removed');
	});

	it('requeue: a criteria change during the per-book fetch discards the dispatch', async () => {
		await mountPage();
		await selectAllRows();
		const gates = new Map<number, PromiseWithResolvers<BookWithPeople>>();
		getMock.mockImplementation((path: string) => {
			if (/^\/books\/\d+$/.test(path)) {
				const id = Number(path.split('/')[2]);
				const gate = Promise.withResolvers<BookWithPeople>();
				gates.set(id, gate);
				return gate.promise;
			}
			if (path.startsWith('/books?')) return listResponse();
			if (path === '/output-profiles') return { profiles: [] };
			return {};
		});

		bulkButton('Requeue & view progress').click();
		await Promise.resolve();
		await Promise.resolve();
		expect([...gates.keys()].sort()).toEqual([1, 2, 3]); // fetches in flight

		await changeFilter();

		gates.forEach((gate, id) => gate.resolve(bookFixture(id)));
		await Promise.resolve();
		await Promise.resolve();
		await Promise.resolve();

		// The stale guard: the captured ids no longer represent what the
		// user sees, so the batch is never dispatched.
		expect(postMock).not.toHaveBeenCalled();
		// goto fires for the filter URL itself (expected); it must NOT fire
		// for the /process target (the stale post-fetch guard).
		expect(gotoMock.mock.calls.map((c) => c[0])).not.toContain('/process');
	});
});
