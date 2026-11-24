import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BatchAggregate, BookWithPeople, ProcessingJob } from '#lib/types.js';

// The queue page hydrates the rows it renders: the render cap for
// non-batch history, and the latest batch's rows as a bounded paged fetch
// (the /jobs response carries the batch's aggregate, not its rows). The
// contracts under test live in the script (restoreJobs hydration,
// hydrateRenderedHistory, loadBatchRows, renderedHistory/
// hiddenHistoryCount), so the presentational components stub out.
const { getMock, postMock, streamEventsMock, sseHandlers } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	// connectSSE stores the returned stop in cleanupMap (the unmount
	// teardown calls every entry) and drives the terminal transition via
	// the onMessage callback (a running job's pollJob returns early
	// without arming a timer), so the mock records onMessage per url and
	// hands back a stop function.
	sseHandlers: new Map<string, (raw: string) => void>(),
	streamEventsMock: vi.fn(
		(url: string, onMessage: (raw: string) => void) => {
			sseHandlers.set(url, onMessage);
			return vi.fn();
		}
	)
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: vi.fn(),
	del: vi.fn(),
	streamEvents: streamEventsMock,
	ApiError: class ApiError extends Error {}
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('#lib/components/index.js', async () => {
	// Dynamic import is load-bearing: vi.mock hoists this factory above
	// the file's static imports, so a static ButtonStub import would hit
	// a not-yet-initialized binding.
	const { default: Button } = await import('./ButtonStub.svelte');
	const Null = () => null;
	// Button is a real stub component (the page's controls are clicked in
	// tests); every other stub is inert.
	return {
		Button,
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
const BATCH_PAGE_SIZE = 16; // mirrors the page's batch page size

/** i seconds after 2026-01-01 00:00:00, formatted as the API's
 *  second-precision timestamp. Realistic (minutes/seconds stay in range),
 *  so lexicographic == chronological — the API orders by created_at DESC. */
function createdAt(i: number): string {
	const m = Math.floor(i / 60);
	const s = i % 60;
	return `2026-01-01 00:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

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
		created_at: createdAt(seq),
		queue_order: seq,
		batch_id: batchId
	};
}

// n terminal merge rows, one per book (books 1..n), queue_order 1..n, no
// batch id: pre-batch history that sits entirely past the render bound.
function jobList(n: number): ProcessingJob[] {
	return Array.from({ length: n }, (_, i) => jobFixture(`job-${String(i + 1).padStart(2, '0')}`, i + 1, i + 1));
}

function batchJobList(n: number, batchId: string): ProcessingJob[] {
	return Array.from({ length: n }, (_, i) => jobFixture(`job-${String(i + 1).padStart(2, '0')}`, i + 1, i + 1, batchId));
}

function batchAggregate(batchId: string, n: number, lastSeq: number): BatchAggregate {
	return {
		batch_id: batchId,
		total: n,
		done: n,
		failed: 0,
		cancelled: 0,
		in_flight: 0,
		all_terminal: true,
		started_at: '2026-01-01 00:00:00',
		finished_at: '2026-01-01 00:01:00',
		last_seq: lastSeq,
		job_ids: Array.from({ length: n }, (_, i) => `job-${String(i + 1).padStart(2, '0')}`)
	};
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

function wireApi(jobs: ProcessingJob[], batch: BatchAggregate | null = null) {
	const batched = batch ? jobs.filter((job) => job.batch_id === batch.batch_id) : [];
	getMock.mockImplementation(async (path: string) => {
		if (path.startsWith('/books?')) return { books: [], total: 0, page: 0, limit: 100 };
		if (path.startsWith('/output-profiles')) return { profiles: [] };
		if (path.startsWith('/jobs')) {
			// The paged batch fetch: ?batch_id=ID&limit=&offset= slices the
			// batch's rows in queue order (the page's on-demand fetch).
			if (path.includes('batch_id=')) {
				const params = new URLSearchParams(path.split('?')[1]);
				const offset = Number(params.get('offset') ?? 0);
				const limit = Number(params.get('limit') ?? 100);
				return { jobs: batched.slice(offset, offset + limit), batch: null };
			}
			// Newest-first, like the real API (ORDER BY created_at DESC),
			// sliced to the limit window (the page asks for limit=100).
			const params = new URLSearchParams(path.split('?')[1]);
			const limit = Number(params.get('limit') ?? 100);
			const offset = Number(params.get('offset') ?? 0);
			const sorted = [...jobs].sort((a, b) => b.created_at.localeCompare(a.created_at));
			return { jobs: sorted.slice(offset, offset + limit), batch };
		}
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
		streamEventsMock.mockClear();
		sseHandlers.clear();
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
	});

	function bookFetches(): string[] {
		return getMock.mock.calls
			.map((call) => call[0] as string)
			.filter((path) => path.startsWith('/books/'));
	}

	async function mountWith(jobs: ProcessingJob[], batch: BatchAggregate | null = null) {
		wireApi(jobs, batch);
		component = mount(ProcessPage, { target });
		flushSync();
		await vi.waitFor(
			() => {
				expect(getMock).toHaveBeenCalledWith('/jobs?limit=100');
			},
			{ timeout: 4000 }
		);
		// The delayed load (200ms) gates the page body; wait for the
		// history disclosure to mount before asserting on it.
		await vi.waitFor(
			() => {
				expect(target.querySelector('.job-history')).not.toBeNull();
			},
			{ timeout: 4000 }
		);
		flushSync();
	}

	it('pre-batch history hydrates the render cap, not the whole past set', async () => {
		// 30 legacy rows, created seconds apart and spaced by gaps of 4 in
		// queue order: the adjacency walk breaks at every gap, so each row
		// is its own one-row run. The banner's run is the newest row alone
		// (already in the first-20 slice), so the bound is the render cap:
		// 20 rendered, 10 hidden. A whole-past-set hydrator would fetch 30
		// books at load; the bounded view fetches exactly the 20 it shows.
		const n = HISTORY_RENDER_LIMIT + 10;
		const jobs = jobList(n).map((job, i) => ({
			...job,
			created_at: createdAt((i + 1) * 4), // seconds apart: 4, 8, ..., 120
			queue_order: (i + 1) * 4
		}));
		await mountWith(jobs);

		const rendered = (target.querySelector('.job-history') as HTMLElement).querySelectorAll('.job-record').length;
		expect(rendered).toBe(HISTORY_RENDER_LIMIT);
		// Hydration follows the rendered set exactly: 20 fetches, none for
		// the 10 hidden rows.
		expect(bookFetches()).toHaveLength(HISTORY_RENDER_LIMIT);
		// No batch, so no paged batch fetch.
		expect(getMock.mock.calls.some((call) => String(call[0]).includes('batch_id='))).toBe(false);
	});

	it('a large batch pages in bounded and the banner reads the aggregate', async () => {
		// A 120-job batch: bigger than the 100-row recent window, so the
		// /jobs response carries the newest 100 batch rows PLUS the
		// aggregate (counts + all 120 row ids), not all 120 full rows.
		// The page pages the batch in BATCH_PAGE_SIZE-at-a-time and reads
		// the banner from the aggregate, not the rows it holds.
		const jobs = batchJobList(120, 'req-1'); // books 1..120, queue orders 1..120
		const batch = batchAggregate('req-1', 120, 120);
		await mountWith(jobs, batch);

		// The paged fetch runs, bounded to BATCH_PAGE_SIZE per page:
		// ceil(120/16) = 8 pages, the last a partial of 8.
		await vi.waitFor(
			() => {
				const pages = getMock.mock.calls.filter((call) => String(call[0]).includes('batch_id=req-1'));
				expect(pages).toHaveLength(8);
			},
			{ timeout: 4000 }
		);
		const pageCalls = getMock.mock.calls
			.filter((call) => String(call[0]).includes('batch_id=req-1'))
			.map((call) => String(call[0]));
		expect(pageCalls).toContain('/jobs?batch_id=req-1&limit=16&offset=0');
		expect(pageCalls).toContain('/jobs?batch_id=req-1&limit=8&offset=112');
		// Every page is bounded to BATCH_PAGE_SIZE.
		for (const call of pageCalls) {
			expect(call).not.toMatch(/limit=(1[7-9]|[2-9]\d|1\d{2,})&/);
		}

		// The banner is visible: batchVisible (allTerminal + not dismissed)
		// and batchSummary are both driven by the aggregate. The Alert is
		// stubbed out (its text would be erased), so assert the banner
		// element the page renders when the aggregate's run is terminal.
		await vi.waitFor(
			() => {
				expect(target.querySelector('.queue-banner-wrap')).not.toBeNull();
			},
			{ timeout: 4000 }
		);
	});

	it('Show all hydrates the rows it reveals, and the toggle flips', async () => {
		// 25 legacy rows with SPACED queue orders (gap 4): each row is its
		// own run (the adjacency walk breaks at every gap), so the banner's
		// run is the newest row alone (order 100, in the first-20 slice).
		// The bound is the render cap: 20 rendered, 5 hidden.
		const n = HISTORY_RENDER_LIMIT + 5;
		const bounded = HISTORY_RENDER_LIMIT;
		const hidden = n - bounded;
		const jobs = jobList(n).map((job, i) => ({
			...job,
			created_at: createdAt((i + 1) * 4),
			queue_order: (i + 1) * 4
		}));
		await mountWith(jobs);

		const history = () => target.querySelector('.job-history') as HTMLElement;
		const records = () => history().querySelectorAll('.job-record').length;
		const moreButtons = () => Array.from(history().querySelectorAll<HTMLButtonElement>('button.history-more'));

		// Bounded view: exactly the render cap; the hidden rows' books are
		// NOT fetched at load.
		expect(records()).toBe(bounded);
		expect(bookFetches()).toHaveLength(bounded);
		expect(moreButtons()).toHaveLength(1);
		expect(moreButtons()[0].textContent).toContain(`Show ${hidden} older runs`);

		// Show all: every past row renders, and the newly revealed rows
		// hydrate (the hidden rows' books are fetched now, not at load).
		moreButtons()[0].click();
		flushSync();
		expect(records()).toBe(n);
		await vi.waitFor(
			() => {
				expect(bookFetches()).toHaveLength(n);
			},
			{ timeout: 4000 }
		);
		expect(moreButtons()[0].textContent).toContain('Show fewer');

		// Show fewer: back to the bound and the "Show N older" wording.
		moreButtons()[0].click();
		flushSync();
		expect(records()).toBe(bounded);
		expect(moreButtons()[0].textContent).toContain(`Show ${hidden} older runs`);
	});

	it('shows no toggle at all when the past set fits the render bound', async () => {
		const n = HISTORY_RENDER_LIMIT; // exactly at the bound: nothing hidden
		await mountWith(jobList(n));

		const history = () => target.querySelector('.job-history') as HTMLElement;
		expect(history().querySelectorAll('.job-record')).toHaveLength(n);
		expect(history().querySelectorAll('button.history-more')).toHaveLength(0);
	});

	it('clears the finished batch banner on start and re-announces the new batch live', async () => {
		// A finished 2-job batch: its aggregate is the banner's source of
		// truth, so the completion banner is up. Queuing a NEW batch must
		// drop that stale terminal aggregate (the new batch owns the
		// banner), and when the new batch's last row goes terminal the
		// aggregate must be re-fetched WITHOUT a full-page reload — the
		// banner reappears for the new batch, not the old one.
		const readyBooks = [
			{ ...bookFixture(1), status: 'matched' },
			{ ...bookFixture(2), status: 'matched' }
		];
		// One mock for the whole flow: matched books, the finished
		// batch's rows+aggregate, the NEW batch's single-job rows (the
		// page polls each new job from the start — a list-shape response
		// here would leave req-2-1 with a stale non-terminal status), and
		// the limit=1 aggregate refetch.
		const newBatchJobs: ProcessingJob[] = [
			{ ...jobFixture('req-2-1', 1, 3, 'req-2'), status: 'done' },
			{ ...jobFixture('req-2-2', 2, 4, 'req-2'), status: 'running' }
		];
		getMock.mockImplementation(async (path: string) => {
			if (path.startsWith('/books?')) {
				if (path.includes('status=matched')) {
					return { books: readyBooks, total: 2, page: 0, limit: 100 };
				}
				return { books: [], total: 0, page: 0, limit: 100 };
			}
			if (path.startsWith('/output-profiles')) return { profiles: [] };
			// Single-job poll: the canonical state of a new-batch job.
			if (path.startsWith('/jobs/')) {
				const id = path.split('/').pop();
				return newBatchJobs.find((j) => j.id === id) ?? {};
			}
			if (path.startsWith('/jobs?')) {
				const params = new URLSearchParams(path.split('?')[1] ?? '');
				if (params.get('batch_id') === 'req-1') {
					return { jobs: batchJobList(2, 'req-1'), batch: null };
				}
				if (params.get('batch_id') === 'req-2') {
					return { jobs: newBatchJobs, batch: null };
				}
				// The limit=1 aggregate refetch: the new batch, fresh counts.
				if (params.get('limit') === '1') {
					const allDone = newBatchJobs.every((j) => j.status === 'done');
					return {
						jobs: newBatchJobs.slice(0, 1),
						batch: allDone
							? batchAggregate('req-2', 2, 4)
							: { ...batchAggregate('req-2', 2, 4), done: 1, in_flight: 1, all_terminal: false }
					};
				}
				// The full page load (limit=100): the finished batch's rows + aggregate.
				return { jobs: batchJobList(2, 'req-1'), batch: batchAggregate('req-1', 2, 2) };
			}
			if (path.startsWith('/books/')) {
				return bookFixture(Number(path.split('/').pop()));
			}
			return {};
		});
		component = mount(ProcessPage, { target });
		flushSync();
		await vi.waitFor(
			() => {
				expect(target.querySelector('.job-history')).not.toBeNull();
			},
			{ timeout: 4000 }
		);
		await vi.waitFor(
			() => {
				expect(target.querySelector('.queue-banner-wrap')).not.toBeNull();
			},
			{ timeout: 4000 }
		);

		// Start a 2-book batch: post returns the new request id and its
		// jobs; the page then polls each job for its canonical state.
		// Select both ready books first — start() is a no-op with nothing
		// selected. The per-row checkboxes follow the select-all control
		// in DOM order, so click the last two.
		const checkboxes = target.querySelectorAll('input[type="checkbox"]');
		expect(checkboxes.length).toBe(3);
		(checkboxes[1] as HTMLInputElement).click();
		(checkboxes[2] as HTMLInputElement).click();
		flushSync();
		postMock.mockResolvedValue({
			jobs: [
				{ id: 'req-2-1', book_id: 1 },
				{ id: 'req-2-2', book_id: 2 }
			],
			batch_id: 'req-2'
		});
		const startButton = Array.from(target.querySelectorAll('button')).find((b) =>
			String(b.textContent).includes('Start processing')
		);
		expect(startButton).not.toBeNull();
		(startButton as HTMLButtonElement).click();
		flushSync();

		// The POST landed and the page refreshed the book lists around it.
		await vi.waitFor(
			() => {
				expect(postMock).toHaveBeenCalledWith('/process', expect.objectContaining({ book_ids: [1, 2] }));
			},
			{ timeout: 4000 }
		);
		// The old batch's terminal aggregate is gone: no banner (the new
		// batch is in flight, and the legacy fallback walk is empty — no
		// un-batched rows are in the list).
		await vi.waitFor(
			() => {
				expect(target.querySelector('.queue-banner-wrap')).toBeNull();
			},
			{ timeout: 4000 }
		);

		// The page polls the new jobs (start() kicks a poll per job); wait
		// for the running job to have been polled at least once, then the
		// banner must still be down — the batch is in flight.
		await vi.waitFor(
			() => {
				expect(getMock.mock.calls.some((call) => String(call[0]) === '/jobs/req-2-2')).toBe(true);
			},
			{ timeout: 4000 }
		);
		expect(target.querySelector('.queue-banner-wrap')).toBeNull();

		// Last row goes terminal, exactly as the page observes it: the SSE
		// stream reports 'done', the page pulls the canonical row (the
		// poll now returns terminal), and refreshAfterTerminal re-fetches
		// the aggregate (limit=1 — not a full-page load). The banner
		// reappears for the NEW batch.
		newBatchJobs[1] = { ...newBatchJobs[1], status: 'done' };
		sseHandlers.get('/api/jobs/req-2-2/stream')?.('{"status":"done"}');
		flushSync();
		// The terminal refresh re-fetches the aggregate via the bounded
		// limit=1 call — wait for that specific call, not the banner (the
		// legacy walk can surface a banner before the fetch lands).
		await vi.waitFor(
			() => {
				expect(getMock.mock.calls.some((call) => String(call[0]) === '/jobs?limit=1')).toBe(true);
			},
			{ timeout: 4000 }
		);
		// The banner is up for the NEW batch (its terminal aggregate), and
		// the refetch was the bounded limit=1 call — not the full
		// /jobs?limit=100 page load (which would flash the queue).
		expect(target.querySelector('.queue-banner-wrap')).not.toBeNull();
		expect(getMock.mock.calls.filter((call) => String(call[0]) === '/jobs?limit=100').length).toBe(1);
	});
});
