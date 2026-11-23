import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BookWithPeople, ProcessingJob } from '#lib/types.js';

// The queue page hydrates its whole past job set from the 100-row jobs
// fetch and bounds what Recent activity renders. The contracts under test
// live in the script (restoreJobs hydration, renderedHistory/
// hiddenHistoryCount), so the presentational components stub out.
const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: vi.fn(),
	del: vi.fn(),
	streamEvents: vi.fn(),
	ApiError: class ApiError extends Error {}
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('#lib/components/index.js', () => {
	const Null = () => null;
	return {
		Button: Null,
		Alert: Null,
		ErrorDetail: Null,
		Skeleton: Null,
		EmptyState: Null,
		StatusBadge: Null,
		CoverArt: Null,
		OutputProfileSelector: Null,
		JobActionLabel: Null
	};
});

import ProcessPage from './+page.svelte';

const HISTORY_RENDER_LIMIT = 20; // mirrors the page's bound

function jobFixture(id: string, bookId: number, seq: number, batchId?: string): ProcessingJob {
	return {
		id,
		book_id: bookId,
		status: 'done',
		action: 'merge',
		detail: null,
		output: '',
		error: null,
		output_file: null,
		started_at: '2026-01-01 00:00:00',
		completed_at: '2026-01-01 00:01:00',
		created_at: '2026-01-01 00:00:00',
		queue_order: seq,
		batch_id: batchId
	};
}

// n terminal merge rows, one per book (books 1..n), queue_order 1..n, no
// batch id: pre-batch history that sits entirely past the render bound.
function jobList(n: number): ProcessingJob[] {
	return Array.from({ length: n }, (_, i) => jobFixture(`job-${String(i + 1).padStart(2, '0')}`, i + 1, i + 1));
}

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
		src_path: `/in/${id}.m4b`,
		dest_path: '',
		status: 'done',
		status_message: '',
		cover_image_url: '',
		created_at: '',
		updated_at: '',
		authors: [],
		narrators: []
	};
}

function wireApi(jobs: ProcessingJob[]) {
	getMock.mockImplementation(async (path: string) => {
		if (path.startsWith('/books?')) return { books: [], total: 0, page: 0, limit: 100 };
		if (path.startsWith('/jobs')) return { jobs };
		if (path.startsWith('/output-profiles')) return { profiles: [] };
		if (path.startsWith('/books/')) {
			const id = Number(path.split('/').pop());
			return bookFixture(id);
		}
		return {};
	});
}

describe('queue page history hydration and render bound', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
	});

	async function mountWithJobs(jobs: ProcessingJob[]) {
		wireApi(jobs);
		component = mount(ProcessPage, { target });
		flushSync();
		await vi.waitFor(
			() => {
				expect(getMock).toHaveBeenCalledWith('/jobs?limit=100');
			},
			{ timeout: 4000 }
		);
		await vi.waitFor(
			() => {
				expect(target.querySelectorAll('.job-record').length).toBeGreaterThan(0);
			},
			{ timeout: 4000 }
		);
		flushSync();
	}

	it('hydrates the ENTIRE past set: one book fetch per id, beyond the render bound', async () => {
		const n = HISTORY_RENDER_LIMIT + 10; // 30 rows; 10 sit past the bound
		await mountWithJobs(jobList(n));

		// Exactly one /books/<id> fetch per past row, no duplicates.
		const bookFetches = getMock.mock.calls
			.map((call) => call[0] as string)
			.filter((path) => path.startsWith('/books/'));
		expect(bookFetches).toHaveLength(n);
		expect(new Set(bookFetches).size).toBe(n);

		// The rows past the bound (21..30) were fetched too: a bound-gated
		// hydrator (renderedHistory only) would fetch 20.
		for (let id = HISTORY_RENDER_LIMIT + 1; id <= n; id++) {
			expect(bookFetches).toContain(`/books/${id}`);
		}

		// Hydration landed on the rows: the hidden rows render their book
		// title, not the "Book #<id>" fallback.
		await vi.waitFor(
			() => {
				expect(target.querySelector('.job-record[data-job-id="job-30"] a')?.textContent).toBe('Book 30');
			},
			{ timeout: 4000 }
		);
	});

	it('Show all / Show fewer flips the rendered rows and the button pair', async () => {
		// 25 rows with SPACED queue orders (gap 4): the pre-batch adjacency
		// walk breaks at every gap, so the banner's run is the newest job
		// alone. It sits PAST the first-20 slice, so the bound renders the
		// 20 plus that one always-visible run row: 21 rendered, 4 hidden.
		const n = HISTORY_RENDER_LIMIT + 5;
		const bounded = HISTORY_RENDER_LIMIT + 1;
		const hidden = n - bounded;
		const jobs = jobList(n).map((job, i) => ({ ...job, queue_order: (i + 1) * 4 }));
		await mountWithJobs(jobs);

		const history = () => target.querySelector('.job-history') as HTMLElement;
		const records = () => history().querySelectorAll('.job-record').length;
		const moreButtons = () => Array.from(history().querySelectorAll<HTMLButtonElement>('button.history-more'));

		// Bounded view: the render bound plus the banner's run row.
		expect(records()).toBe(bounded);
		expect(moreButtons()).toHaveLength(1);
		expect(moreButtons()[0].textContent).toContain(`Show ${hidden} older runs`);
		expect(moreButtons()[0].textContent).not.toContain('Show fewer');

		// Show all: every past row renders, button flips to "Show fewer".
		moreButtons()[0].click();
		flushSync();
		expect(records()).toBe(n);
		expect(moreButtons()).toHaveLength(1);
		expect(moreButtons()[0].textContent).toContain('Show fewer');

		// Show fewer: back to the bound and the "Show N older" wording.
		moreButtons()[0].click();
		flushSync();
		expect(records()).toBe(bounded);
		expect(moreButtons()[0].textContent).toContain(`Show ${hidden} older runs`);
	});

	it('shows no toggle at all when the past set fits the render bound', async () => {
		const n = HISTORY_RENDER_LIMIT; // exactly at the bound: nothing hidden
		await mountWithJobs(jobList(n));

		const history = () => target.querySelector('.job-history') as HTMLElement;
		expect(history().querySelectorAll('.job-record')).toHaveLength(n);
		expect(history().querySelectorAll('button.history-more')).toHaveLength(0);
	});
});
