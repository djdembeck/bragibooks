import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// T16: the engine-preview payload must carry the subtitle with the SAME
// precedence the server capture uses (subtitleFromCatalog): the selected
// release's own subtitle when present, then the book's, then the book's
// disambiguation. Without the value, a {subtitle} scheme resolves a
// different name in the preview than in the queued job.
const { getMock, postMock, putMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: putMock,
	del: vi.fn(),
	ApiError: class ApiError extends Error {}
}));
const { gotoMock } = vi.hoisted(() => ({ gotoMock: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto: gotoMock }));
// The page composes many presentational components; the payload under test
// is built in the script and shipped by the preview effect, so everything
// visual stubs out.
vi.mock('#lib/components/index.js', () => {
	const Null = () => null;
	// Renders a real <button> into Svelte's anchor so the save flow is
	// triggerable; the `class` prop (match__save) is enough to locate it.
	const Button = (anchor: ChildNode, props: {
		class?: string;
		disabled?: boolean;
		loading?: boolean;
		onclick?: (event: MouseEvent) => void;
		[key: string]: unknown;
	}) => {
		const el = document.createElement('button');
		el.className = props.class ?? '';
		// A Svelte function component runs once; `disabled`/`loading` change
		// later, so expose them as live getters reading the props proxy.
		Object.defineProperty(el, 'disabled', {
			get: () => props.disabled || props.loading,
			configurable: true
		});
		el.onclick = props.onclick ?? null;
		anchor.before(el);
	};
	return {
		Button,
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
	byAsin: (asin: string) => Promise<unknown> | unknown = async () => ({ ...BOOK, ...byAsinOverrides }),
	byAsinOverrides: Record<string, unknown> = {},
	evidenceOverrides: Record<string, unknown> = {}
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
				comparisons: {},
				...evidenceOverrides
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
		putMock.mockReset();
		gotoMock.mockReset();
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
		// microtasks; a macrotask boundary (setTimeout) lets them start
		// before the first poll.
		await new Promise<void>((r) => setTimeout(r, 0));
		// vi.waitUntil (not vi.waitFor): a plain boolean return is treated
		// as "not yet complete" and retried, so the settle is asserted on
		// the call list rather than assumed after one timer.
		await vi.waitUntil(
			() => {
				// autoMatch completes when it has fetched the selected
				// release (fast path success) or the title-path book
				// detail (title search ran).
				const paths = getMock.mock.calls.map((c) => c[0] as string);
				return (
					paths.some((p) => p.startsWith('/search/releases/')) ||
					paths.some((p) => p.startsWith('/search/books/'))
				);
			},
			{ timeout: 4000, interval: 5 }
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
		// The VISIBLE badge button carries the attribution (the hidden
		// popover does too); the neutral fallback must not win here.
		const badge = mountTarget?.querySelector('button.match__origin');
		expect(badge?.textContent).toContain('Selected by source ASIN');
		expect(badge?.textContent).not.toContain('Title suggestion');
	});

	it('keeps the ASIN attribution on the badge when the report verifies the book identity', async () => {
		// The canonical detail fetch verifies the book identity for exactly
		// the ASIN-resolved case (origin book_id, verified book-scope
		// identifier). The visible badge must keep the source-ASIN
		// attribution; the identity label must not replace it.
		wireApi(
			{},
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			undefined,
			{ external: [], series: [] },
			{
				origin: 'book_id',
				identifiers: [{ identifier: 'B00B5HZGUG', provenance: 'Source ASIN', state: 'verified', scope: 'book', bookId: 'b1', reason: 'Resolved book matches the source ASIN.' }]
			}
		);
		await mountAndSettle();
		await vi.waitUntil(
			() => {
				const badge = mountTarget?.querySelector('button.match__origin');
				return (badge?.textContent ?? '').includes('Selected by source ASIN');
			},
			{ timeout: 4000, interval: 5 }
		);
		const badge = mountTarget?.querySelector('button.match__origin');
		expect(badge?.textContent).toContain('Selected by source ASIN');
		expect(badge?.textContent).not.toContain('Exact book ID');
		// The popover heading keeps the identity scope.
		expect(mountTarget?.querySelector('button.match__origin + .match__explanation-popover strong')?.textContent).toBe('Exact book ID');
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

	it('saves the resolved ASIN and series even though the by-asin payload lacks them', async () => {
		// The by-asin endpoint returns the upstream payload with no
		// `external` and no `series`; the page must fetch the canonical
		// detail so the save carries both. The mock detail (the /search/books/
		// response) is the source of truth here. The by-asin route gets its
		// own overrides map and stays field-less, so the save assertions can
		// only be satisfied via the detail fetch.
		wireApi(
			{ external: [{ type: 'amazon', id: 'B00B5HZGUG' }], series: [{ name: 'Grim Hollow', id: 's1', position: 2 }] },
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			undefined,
			{ external: [], series: [] }
		);
		await mountAndSettle();
		// The detail fetch (with `external` include) must have run for the
		// resolved book, so the save payload is repaired, not the raw hit.
		expect(callsWith('/search/books/')).toEqual(['/search/books/b1?include=releases,people,series,images,external,genres']);
		const save = mountTarget?.querySelector<HTMLButtonElement>('button.match__save');
		expect(save).not.toBeNull();
		save!.click();
		await vi.waitUntil(
			() => putMock.mock.calls.length > 0,
			{ timeout: 4000, interval: 5 }
		);
		const body = putMock.mock.calls[0][1] as Record<string, unknown>;
		expect(body.asin).toBe('B00B5HZGUG');
		expect(body.series).toBe('Grim Hollow #2');
	});

	it('keeps the by-ASIN book selected when the canonical detail fetch fails', async () => {
		// The detail fetch (include set) repairs the by-asin payload with
		// `external`/`series` for the save. A 500 there must NOT demote the
		// match to title search: the already-resolved by-asin book stays
		// selected and the ASIN attribution survives.
		wireApi(
			{},
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } }
		);
		const base = getMock.getMockImplementation()!;
		getMock.mockImplementation(async (path: string) => {
			if (path.startsWith('/search/books/')) {
				throw new Error('API 500: internal server error');
			}
			return base(path);
		});
		await mountAndSettle();
		// The detail fetch ran (and failed) for the resolved book.
		expect(callsWith('/search/books/')).toEqual(['/search/books/b1?include=releases,people,series,images,external,genres']);
		// No title-search fallback: the by-asin hit is kept, not re-derived.
		expect(callsWith('/search?')).toEqual([]);
		// The rejected detail fetch must not crash the match: the candidate
		// is still rendered with the ASIN attribution on the badge.
		await vi.waitUntil(
			() => {
				const badge = mountTarget?.querySelector('button.match__origin');
				return (badge?.textContent ?? '').includes('Selected by source ASIN');
			},
			{ timeout: 4000, interval: 5 }
		);
		const badge = mountTarget?.querySelector('button.match__origin');
		expect(badge?.textContent).toContain('Selected by source ASIN');
	});

	it('carries the book genre in the preview payload from the canonical detail', async () => {
		// The by-asin payload carries no genres; the canonical detail fetch
		// (include set, now including `genres`) is the only source. The
		// preview's captureMetadata reads book.genres[0].title, so a missing
		// include would drop the value and a {genre} scheme would resolve a
		// different path in the preview than in the queued job.
		wireApi(
			{ genres: [{ id: 'g1', name: 'Mystery', title: 'Mystery' }] },
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			undefined,
			{ genres: [] }
		);
		await mountAndSettle();
		// The preview effect dispatches on a 250ms timer after the book
		// settles; vi.waitUntil (boolean return) retries until the post
		// lands. A stale dispatch from a preceding test's mount can post a
		// genre-less payload first, so select THIS mount's payload: the
		// first post whose metadata carries genres (only this test's
		// canonical detail carries them).
		let genres: unknown;
		await vi.waitUntil(
			() => {
				const body = postMock.mock.calls
					.map((c) => c[1] as { metadata?: Record<string, unknown> })
					.find((b) => b.metadata && b.metadata.genres !== undefined);
				if (!body) return false;
				genres = body.metadata!.genres;
				return true;
			},
			{ timeout: 4000, interval: 5 }
		);
		expect(genres).toEqual(['Mystery']);
	});

	it('trims genre titles with Go unicode.IsSpace whitespace (U+0085) and drops blanks', async () => {
		// Go's strings.TrimSpace (genreTitles) trims U+0085; JS String.trim
		// does not. The preview must match the snapshot: a U+0085-only title
		// is dropped, a padded title is trimmed.
		wireApi(
			{
				genres: [
					{ id: 'g1', name: '', title: '\u0085' },
					{ id: 'g2', name: 'Mystery', title: '\u0085 Mystery \u00A0' }
				]
			},
			{},
			{ source_asin: { status: 'present', value: 'B00B5HZGUG' } },
			undefined,
			{ genres: [] }
		);
		await mountAndSettle();
		let genres: unknown;
		await vi.waitUntil(
			() => {
				const body = postMock.mock.calls
					.map((c) => c[1] as { metadata?: Record<string, unknown> })
					.find((b) => b.metadata && b.metadata.genres !== undefined);
				if (!body) return false;
				genres = body.metadata!.genres;
				return true;
			},
			{ timeout: 4000, interval: 5 }
		);
		expect(genres).toEqual(['Mystery']);
	});
});
