import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// T16: the engine-preview payload must carry the subtitle with the SAME
// precedence the server capture uses (subtitleFromCatalog): the selected
// release's own subtitle when present, then the book's, then the book's
// disambiguation. Without the value, a {subtitle} scheme resolves a
// different name in the preview than in the queued job.
const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: vi.fn(),
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
// The page composes many presentational components; the payload under test
// is built in the script and shipped by the preview effect, so everything
// visual stubs out.
vi.mock('#lib/components/index.js', () => {
	const Null = () => null;
	return {
		Button: Null,
		Alert: Null,
		ErrorDetail: Null,
		Skeleton: Null,
		EmptyState: Null,
		Modal: Null
	};
});
vi.mock('./SourceEvidence.svelte', () => ({ default: () => null }));
vi.mock('./RuntimeComparison.svelte', () => ({ default: () => null }));
vi.mock('./MissingReleaseNudge.svelte', () => ({ default: () => null }));
vi.mock('#lib/missingRelease.js', () => ({
	showBlockNudge: () => false,
	showEditorNudge: () => false,
	showPanelNudge: () => false
}));

import MatchPage from './+page.svelte';

const BOOK = {
	id: 'b1',
	title: 'Winter’s Lament',
	description: null,
	subtitle: 'Book Sub',
	disambiguation: 'Disambig',
	originallyPublishedAt: '2020-01-01',
	images: [],
	people: [],
	releases: [{ id: 'r1', title: 'Winter’s Lament (2021)' }],
	series: [],
	external: [],
	genres: [],
	tags: []
};
const RELEASE = {
	id: 'r1',
	title: 'Winter’s Lament (2021)',
	duration: '00:00:00',
	runtimeLengthMs: 3600000,
	runtimeLengthSec: 3600,
	isbn: null,
	releaseDate: '2021-05-01',
	subtitle: 'Rel Sub',
	people: [],
	publisher: { id: 'p1', name: 'Pub' },
	language: { name: 'English' },
	images: []
};

// The stock fixture: release subtitle present. The precedence inputs are
// all set to DISTINCT values so any precedence regression changes the
// payload's subtitle.
function wireApi(
	bookOverrides: Record<string, unknown> = {},
	releaseOverrides: Record<string, unknown> = {},
	pendingOverrides: Record<string, unknown> = {},
	byAsin: (asin: string) => Promise<unknown> | unknown = async () => ({ ...BOOK, ...bookOverrides, id: 'asin-book' })
) {
	const book = { ...BOOK, ...bookOverrides };
	const release = { ...RELEASE, ...releaseOverrides };
	getMock.mockImplementation(async (path: string) => {
		if (path === '/settings') {
			return { output_scheme: '{title}', output_dir: '/out' };
		}
		if (path.startsWith('/books?')) {
			return {
				books: [
					{
						id: 7,
						title: 'Winter’s Lament',
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
						src_path: '/in/Winter’s Lament.mp3',
						dest_path: '',
						status: 'pending',
						status_message: '',
						cover_image_url: '',
						created_at: '',
						updated_at: '',
						...pendingOverrides
					}
				],
				total: 1,
				page: 0,
				limit: 100
			};
		}
		if (path.startsWith('/search/by-asin/')) {
			return byAsin(path.slice('/search/by-asin/'.length));
		}
		if (path.startsWith('/search?')) {
			return { results: [{ type: 'books', data: book }] };
		}
		if (path.startsWith('/search/books/')) {
			return book;
		}
		if (path.startsWith('/search/releases/')) {
			return release;
		}
		if (path.includes('/match-evidence')) {
			return {
				sourceRead: { status: 'read', detail: '' },
				selection: { bookId: 'b1', releaseId: 'r1' },
				origin: 'title',
				identifiers: [],
				comparisons: {}
			};
		}
		return {};
	});
	postMock.mockResolvedValue({ relative_path: 'Winter’s Lament.m4b' });
}

describe('match engine-preview payload', () => {
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

	async function expectPreviewSubtitle(want: string) {
		component = mount(MatchPage, { target });
		flushSync();
		await vi.waitFor(
			() => {
				expect(postMock).toHaveBeenCalled();
			},
			{ timeout: 4000 }
		);
		// The page posts the PARSED payload object (JSON.parse of the
		// recorded dispatch payload), so the mock's argument is the body.
		const body = postMock.mock.calls[0][1] as Record<string, unknown>;
		expect((body.metadata as Record<string, unknown>).subtitle).toBe(want);
	}

	it('carries the RELEASE subtitle when the release has its own (precedence 1)', async () => {
		wireApi();
		await expectPreviewSubtitle('Rel Sub');
	});

	it('falls back to the BOOK subtitle when the release carries none (precedence 2)', async () => {
		wireApi({}, { subtitle: '' });
		await expectPreviewSubtitle('Book Sub');
	});

	it('falls back to the DISAMBIGUATION when neither release nor book has one (precedence 3)', async () => {
		wireApi({ subtitle: null }, { subtitle: '' });
		await expectPreviewSubtitle('Disambig');
	});
});

describe('match ASIN fast path', () => {
	let target: HTMLElement;
	let mountTarget: HTMLElement | null = null;
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

	async function mountAndSettle() {
		mountTarget = target;
		component = mount(MatchPage, { target });
		flushSync();
		// loadPending's async body schedules the autoMatch chain as
		// microtasks; a macrotask boundary (setTimeout) lets them all
		// complete before the first vi.waitFor poll, so the assertions
		// see a settled call list rather than a mid-flight snapshot.
		await new Promise<void>((r) => setTimeout(r, 0));
		await vi.waitFor(
			() => {
				// autoMatch completes when it has either fetched the
				// release (fast path success) or run the title search.
				const paths = getMock.mock.calls.map((c) => c[0] as string);
				return (
					paths.some((p) => p.startsWith('/search/releases/')) ||
					paths.some((p) => p.startsWith('/search?'))
				);
			},
			{ timeout: 4000 }
		);
	}

	function callsWith(prefix: string): string[] {
		return getMock.mock.calls
			.map((c) => c[0] as string)
			.filter((p) => p.startsWith(prefix));
	}

	it('resolves a present source ASIN via the by-asin endpoint (no title search)', async () => {
		wireApi({}, {}, { source_asin: { status: 'present', value: 'B00B5HZGUG' } });
		await mountAndSettle();
		expect(callsWith('/search/by-asin/')).toEqual(['/search/by-asin/B00B5HZGUG']);
		expect(callsWith('/search?')).toEqual([]);
		// The auto-selection must explain itself as ASIN-based, not
		// title-based: the operator sees which evidence produced the pick.
		expect(mountTarget?.textContent).toContain('Matched by the source file\'s ASIN');
	});

	it('falls back to title search when the ASIN lookup 404s', async () => {
		wireApi(
			{},
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			() => {
				throw new Error('API 404: no catalog record for this ASIN');
			}
		);
		await mountAndSettle();
		expect(callsWith('/search/by-asin/')).toHaveLength(1);
		expect(callsWith('/search?')).toHaveLength(1);
	});

	it('falls back to title search when the ASIN lookup 502s', async () => {
		wireApi(
			{},
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			() => {
				throw new Error('API 502: audiobookdb ASIN lookup: boom');
			}
		);
		await mountAndSettle();
		expect(callsWith('/search/by-asin/')).toHaveLength(1);
		expect(callsWith('/search?')).toHaveLength(1);
	});

	it('skips the ASIN fast path when source_asin is not present', async () => {
		// 'ambiguous' carries no value; 'missing'/'unknown' likewise.
		wireApi({}, {}, { source_asin: { status: 'ambiguous', count: 2 } });
		await mountAndSettle();
		expect(callsWith('/search/by-asin/')).toEqual([]);
		expect(callsWith('/search?')).toHaveLength(1);
	});
});
