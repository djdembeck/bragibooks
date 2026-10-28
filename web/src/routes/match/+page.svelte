<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { get, put, del } from '#lib/api.js';
	import { goto } from '$app/navigation';
	import type {
		BookWithPeople,
		BooksListResponse,
		SearchHit,
		SearchResponse,
		AudiobookDBBook,
		AudiobookDBRelease,
		Settings,
		UpdateBookRequest
	} from '#lib/types.js';
	import {
		extractAsin,
		pickCover,
		hitCoverUrl,
		bookCoverUrl,
		decodeHtmlEntities,
		AUDIO_FILE_EXTS
	} from '#lib/types.js';
	import { Button, Alert, ErrorDetail, Skeleton, EmptyState, AbdTag, Modal } from '#lib/components/index.js';
	import { toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { resolveScheme } from '#lib/scheme.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';

	type ReviewMode = 'batch' | 'focused';

	interface CandidateDetails {
		book: AudiobookDBBook | null;
		release: AudiobookDBRelease | null;
	}

	interface MatchCandidate {
		bookId: number;
		srcPath: string;
		title: string;
		/** Local cover assigned at intake — never confused with catalog art. */
		coverUrl: string;
		searchResults: AudiobookDBBook[];
		selectedBookId: string | null;
		selectedReleaseId: string | null;
		details: CandidateDetails;
		loading: boolean;
		error: CleanedAdbError | null;
		/** Edition detail request in flight / failed. */
		releaseLoading: boolean;
		releaseError: CleanedAdbError | null;
		/** Explicit batch inclusion. Persists across modes and sources. */
		included: boolean;
		/** Every book/release request increments this; only the newest may apply. */
		seq: number;
		manualPick: boolean;
		removing: boolean;
		removingConfirming: boolean;
		descriptionOpen: boolean;
		descriptionOverflow: boolean;
		_removeSnapshot?: { bookId: number; srcPath: string; title: string };
	}

	interface HydratedEntry {
		book: AudiobookDBBook | null;
		release: AudiobookDBRelease | null;
		loading: boolean;
		error: CleanedAdbError | null;
	}

	/** Real metadata for the releases listed in the edition picker. Only the
	 *  active focused book is populated; every request reuses the shared
	 *  release cache. */
	interface EditionEntry {
		release: AudiobookDBRelease | null;
		loading: boolean;
		error: CleanedAdbError | null;
	}

	let candidates = $state<MatchCandidate[]>([]);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let saving = $state(false);
	let pageError = $state<CleanedAdbError | null>(null);
	// Prevents the empty state from flashing before the first request starts.
	let initialLoadComplete = $state(false);

	// Batch is the default review surface; focused review is the exception path.
	let mode = $state<ReviewMode>('batch');

	// Focused review — one source is active at a time; its inspector is shown
	// right. Switching sources never discards any candidate's state.
	let activeId = $state<number | null>(null);

	// Bulk removal — the dialog carries its own checked set. The page's
	// include boxes choose the save set and never authorize a delete.
	let removeDialogOpen = $state(false);
	let removePick = $state<Set<number>>(new Set());
	let removingMany = $state(false);
	let removingNow = $state<number | null>(null);
	let removeFailure = $state<{
		failed: number;
		attempted: number;
		error: CleanedAdbError;
		ids: number[];
	} | null>(null);
	/** Transient completion sentence for the polite live region. */
	let removalNotice = $state('');

	// The removal toolbar docks directly under the sticky page head, whose
	// height changes with wrapping and breakpoints.
	let headEl = $state<HTMLElement>();
	let headHeight = $state(0);

	$effect(() => {
		const node = headEl;
		if (!node) return;
		const measure = () => (headHeight = node.getBoundingClientRect().height);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(node);
		return () => observer.disconnect();
	});

	// Candidate picker — which source's listbox is open + keyboard cursor
	let pickerCandidateId = $state<number | null>(null);
	let pickerIndex = $state(-1);

	// Inline catalog search for the active source
	let searchQuery = $state('');
	let searchFormQuery = $state('');
	let searchedQuery = $state('');
	let searching = $state(false);
	let searchResults = $state<AudiobookDBBook[]>([]);
	let searchError = $state<CleanedAdbError | null>(null);
	let searched = $state(false);
	// Bumped whenever the search context resets (source switch) and for every
	// new search: a request may only publish its outcome while still current.
	let searchGeneration = 0;

	// Settings for the output naming preview (output dir + scheme)
	let outputDir = $state('');
	let outputScheme = $state('');
	let settingsLoaded = $state(false);

	// Hydrated catalog options — only populated for choices the operator can
	// actually see (open picker / rendered search results). Cached per page.
	let hydration = $state<Record<string, HydratedEntry>>({});

	// Edition-picker metadata for the one focused multi-release book.
	let editionDetails = $state<Record<string, EditionEntry>>({});

	// Shared request caches; reactive UI state lives in `hydration`.
	const bookRequests = new Map<string, Promise<AudiobookDBBook>>();
	const releaseRequests = new Map<string, Promise<AudiobookDBRelease>>();

	const active = $derived(candidates.find((c) => c.bookId === activeId) ?? null);
	const activeIndex = $derived(active ? candidates.findIndex((c) => c.bookId === active.bookId) : -1);
	const pickerCandidate = $derived(candidates.find((c) => c.bookId === pickerCandidateId) ?? null);
	const readyCandidates = $derived(candidates.filter(isReady));
	const includedReadyCount = $derived(candidates.filter(isSavable).length);
	const allReadyIncluded = $derived(
		readyCandidates.length > 0 && readyCandidates.every((c) => c.included)
	);
	const someReadyIncluded = $derived(readyCandidates.some((c) => c.included));

	const removeCount = $derived(candidates.filter((c) => removePick.has(c.bookId)).length);
	const allRemovePicked = $derived(
		candidates.length > 0 && candidates.every((c) => removePick.has(c.bookId))
	);
	const someRemovePicked = $derived(removeCount > 0);
	/** A per-row delete is already committing; a bulk removal waits for it. */
	const removalInFlight = $derived(candidates.some((c) => c.removingConfirming));

	const announcements = $derived.by(() => {
		if (mode === 'batch') {
			return `${includedReadyCount} of ${candidates.length} included in this save.`;
		}
		if (!active) return '';
		return active.details.book
			? `Record for ${active.title} selected.`
			: `${active.title} has no selected record.`;
	});

	function setMode(next: ReviewMode) {
		if (mode === next || removingMany) return;
		mode = next;
		closePicker();
		removalNotice = '';
		if (next === 'focused' && activeId == null && candidates.length > 0) {
			activeId = candidates[0].bookId;
		}
	}

	function openFocused(id: number) {
		setActive(id);
		setMode('focused');
	}

	function setActive(id: number) {
		if (activeId === id) return;
		activeId = id;
		closePicker();
	}

	/** Clamp the active source to a real candidate — after the initial load
	 *  and after a confirmed removal — so the inspector can never target
	 *  stale data. */
	$effect(() => {
		if (!active && candidates.length > 0) {
			activeId = candidates[0].bookId;
		}
	});

	async function loadPending() {
		initialLoadComplete = false;
		resetRemovalState();
		await dl.run(async () => {
			pageError = null;
			try {
				// Naming preview needs the root + scheme.
				try {
					const settings = await get<Settings>('/settings');
					outputDir = settings.output_dir || '';
					outputScheme = settings.output_scheme || '';
					settingsLoaded = true;
				} catch {
					// Settings unavailable — previews stay visibly unresolved
				}
				const response = await get<BooksListResponse>('/books?status=pending&limit=100');
				candidates = (response.books || []).map((book) => ({
					bookId: book.id,
					srcPath: book.src_path,
					title: book.title,
					coverUrl: book.cover_image_url || '',
					searchResults: [],
					selectedBookId: null,
					selectedReleaseId: null,
					details: { book: null, release: null },
					loading: true,
					error: null,
					releaseLoading: false,
					releaseError: null,
					included: true,
					seq: 0,
					manualPick: false,
					removing: false,
					removingConfirming: false,
					descriptionOpen: false,
					descriptionOverflow: false
				}));
				if (activeId == null || !candidates.some((c) => c.bookId === activeId)) {
					activeId = candidates[0]?.bookId ?? null;
				}
				for (const candidate of candidates) {
					autoMatch(candidate);
				}
			} catch (e) {
				pageError = toCleanedError(e);
			}
		});
		initialLoadComplete = true;
	}

	async function autoMatch(candidate: MatchCandidate) {
		const seq = ++candidate.seq;
		try {
			const hits = await searchBooks(stripExtension(candidate.title));
			if (seq !== candidate.seq) return;
			candidate.searchResults = hits;
			// Only auto-select a result that clearly IS the source: the
			// shorter title's tokens must be fully contained in the other
			// (containment >= 0.9). Blindly taking hits[0] attaches unrelated
			// books (e.g. a "Ghost Stories" hit on a "Forbidden Grove" folder).
			const best = bestContainedHit(candidate.title, hits);
			if (best) {
				candidate.selectedBookId = best.id;
				candidate.details = { book: null, release: null };
				const book = await getBookDetail(best.id);
				if (seq !== candidate.seq) return;
				const releaseId = book.releases?.[0]?.id;
				const release = releaseId ? await getReleaseDetail(releaseId) : null;
				if (seq !== candidate.seq) return;
				candidate.details = { book, release };
				candidate.selectedReleaseId = release?.id ?? null;
				candidate.manualPick = false;
			}
		} catch (e) {
			if (seq === candidate.seq) candidate.error = toCleanedError(e);
		} finally {
			if (seq === candidate.seq) candidate.loading = false;
		}
	}

	// Returns the best hit whose title is near-fully token-contained by (or
	// contains) the source title, or null when no hit is a confident match.
	function bestContainedHit(
		sourceTitle: string,
		hits: AudiobookDBBook[]
	): AudiobookDBBook | null {
		const srcTokens = titleTokens(sourceTitle);
		if (srcTokens.size === 0) return null;
		let best: AudiobookDBBook | null = null;
		let bestScore = 0;
		for (const hit of hits) {
			const hitTokens = titleTokens(hit.title);
			if (hitTokens.size === 0) continue;
			const inter = setIntersection(srcTokens, hitTokens);
			const smaller = Math.min(srcTokens.size, hitTokens.size);
			const score = inter / smaller;
			if (score >= 0.9 && score > bestScore) {
				bestScore = score;
				best = hit;
			}
		}
		return best;
	}

	function titleTokens(s: string): Set<string> {
		return new Set(
			s
				.toLowerCase()
				.replace(/[^a-z0-9]+/g, ' ')
				.split(' ')
				.filter(Boolean)
		);
	}

	function setIntersection(a: Set<string>, b: Set<string>): number {
		let n = 0;
		for (const t of a) if (b.has(t)) n++;
		return n;
	}

	async function searchBooks(query: string): Promise<AudiobookDBBook[]> {
		if (!query.trim()) return [];
		const response = await get<SearchResponse>(
			`/search?query=${encodeURIComponent(query.trim())}&types=books&take=10`
		);
		return (response.results || [])
			.filter((hit: SearchHit) => hit.type === 'books' || hit.type === 'book')
			.map((hit: SearchHit) => hit.data as AudiobookDBBook);
	}

	// ── Detail loading + per-page caches ───────────────────────

	function getBookDetail(bookId: string): Promise<AudiobookDBBook> {
		const cached = bookRequests.get(bookId);
		if (cached) return cached;
		const request = get<AudiobookDBBook>(
			`/search/books/${bookId}?include=releases,people,series,images,external`
		);
		bookRequests.set(bookId, request);
		void request.catch(() => {
			if (bookRequests.get(bookId) === request) bookRequests.delete(bookId);
		});
		return request;
	}

	function getReleaseDetail(releaseId: string): Promise<AudiobookDBRelease> {
		const cached = releaseRequests.get(releaseId);
		if (cached) return cached;
		const request = get<AudiobookDBRelease>(
			`/search/releases/${releaseId}?include=book,chapterDetail,external,images,language,people,publisher`
		);
		releaseRequests.set(releaseId, request);
		void request.catch(() => {
			if (releaseRequests.get(releaseId) === request) releaseRequests.delete(releaseId);
		});
		return request;
	}

	/** Load an option's real metadata once per page. Only first-release
	 *  details are fetched here; other editions wait for focused review.
	 *  A failed entry stays cached until the option is explicitly retried
	 *  (reopening the picker or its retry control). */
	function hydrateBook(bookId: string, allowRetry = false) {
		const existing = hydration[bookId];
		if (existing?.loading) return;
		if (existing && !(allowRetry && existing.error)) return;
		hydration[bookId] = { book: null, release: null, loading: true, error: null };
		void (async () => {
			try {
				const book = await getBookDetail(bookId);
				const releaseId = book.releases?.[0]?.id;
				const release = releaseId ? await getReleaseDetail(releaseId) : null;
				// A selection or edition change may have published newer,
				// authoritative metadata while this request was in flight.
				if (!hydration[bookId]?.loading) return;
				hydration[bookId] = { book, release, loading: false, error: null };
			} catch (e) {
				if (!hydration[bookId]?.loading) return;
				hydration[bookId] = {
					book: null,
					release: null,
					loading: false,
					error: toCleanedError(e)
				};
			}
		})();
	}

	function hydrateOptions(books: AudiobookDBBook[], allowRetry = false) {
		for (const book of books) hydrateBook(book.id, allowRetry);
	}

	function retryHydration(bookId: string) {
		hydrateBook(bookId, true);
	}

	/** Edition-picker metadata is scoped to the active book and fetched only
	 *  when that book actually has multiple releases. All successful requests
	 *  reuse the shared release promise cache. */
	function hydrateEditions(releases: { id: string }[]) {
		for (const option of releases) {
			const existing = editionDetails[option.id];
			if (existing?.loading || (existing && !existing.error)) continue;
			editionDetails[option.id] = { release: null, loading: true, error: null };
			void (async () => {
				try {
					const release = await getReleaseDetail(option.id);
					editionDetails[option.id] = { release, loading: false, error: null };
				} catch (e) {
					editionDetails[option.id] = {
						release: null,
						loading: false,
						error: toCleanedError(e)
					};
				}
			})();
		}
	}

	// Load edition labels for the focused book only — never for every source
	// in the batch. Reads no edition state reactively, so updates cannot
	// retrigger this effect.
	$effect(() => {
		if (mode !== 'focused') return;
		const book = active?.details.book;
		if (!book || (book.releases?.length ?? 0) < 2) {
			return;
		}
		const releases = book.releases.map((option) => ({ id: option.id }));
		untrack(() => hydrateEditions(releases));
	});

	// ── Selection + edition changes (race-guarded) ─────────────

	async function selectResult(
		candidate: MatchCandidate,
		bookId: string,
		manual: boolean
	) {
		// Re-picking the already-loaded record keeps its edition: resetting to
		// the default release would silently discard a deliberate choice.
		// Errors still fall through to a real retry below.
		const sameLoaded =
			candidate.details.book?.id === bookId &&
			candidate.selectedBookId === bookId &&
			!candidate.error &&
			!candidate.releaseError;
		if (sameLoaded) {
			const book = candidate.details.book;
			if (book) {
				const release = candidate.details.release;
				candidate.manualPick = manual || candidate.manualPick;
				hydration[bookId] = { book, release, loading: false, error: null };
			}
			closePicker();
			return;
		}

		const seq = ++candidate.seq;
		// A retry of the same book keeps the chosen edition if the book's
		// releases list still contains it.
		const keepReleaseId =
			candidate.details.book?.id === bookId ? candidate.selectedReleaseId : null;
		candidate.loading = true;
		candidate.error = null;
		candidate.releaseError = null;
		candidate.releaseLoading = false;
		candidate.selectedBookId = bookId;
		candidate.selectedReleaseId = null;
		candidate.details = { book: null, release: null };
		candidate.manualPick = manual;
		closePicker();
		try {
			const hydrated = hydration[bookId];
			const book = hydrated?.book ?? (await getBookDetail(bookId));
			if (seq !== candidate.seq) return;
			let releaseId = book.releases?.[0]?.id ?? null;
			if (keepReleaseId && book.releases?.some((option) => option.id === keepReleaseId)) {
				releaseId = keepReleaseId;
			}
			const release = releaseId
				? hydrated?.release?.id === releaseId
					? hydrated.release
					: await getReleaseDetail(releaseId)
				: null;
			if (seq !== candidate.seq) return;
			candidate.details = { book, release };
			candidate.selectedReleaseId = release?.id ?? null;
			// Publish the record actually selected so picker metadata and the
			// inspector cannot disagree after a recovered or alternate choice.
			hydration[bookId] = { book, release, loading: false, error: null };
		} catch (e) {
			if (seq === candidate.seq) {
				candidate.error = toCleanedError(e);
				candidate.details = { book: null, release: null };
			}
		} finally {
			if (seq === candidate.seq) candidate.loading = false;
		}
	}

	async function chooseRelease(candidate: MatchCandidate, releaseId: string | null) {
		if (!releaseId) return;
		if (
			candidate.selectedReleaseId === releaseId &&
			candidate.details.release?.id === releaseId &&
			!candidate.releaseError
		) {
			return;
		}
		const seq = ++candidate.seq;
		candidate.selectedReleaseId = releaseId;
		candidate.releaseError = null;
		candidate.releaseLoading = true;
		try {
			const release = await getReleaseDetail(releaseId);
			if (seq !== candidate.seq) return;
			const book = candidate.details.book;
			candidate.details = { book, release };
			editionDetails[releaseId] = { release, loading: false, error: null };
			// The selected book's option metadata follows the chosen edition.
			if (book) {
				hydration[book.id] = { book, release, loading: false, error: null };
			}
		} catch (e) {
			if (seq === candidate.seq) candidate.releaseError = toCleanedError(e);
		} finally {
			if (seq === candidate.seq) candidate.releaseLoading = false;
		}
	}

	function retryCandidate(candidate: MatchCandidate) {
		if (candidate.selectedBookId) {
			void selectResult(candidate, candidate.selectedBookId, candidate.manualPick);
		} else {
			candidate.error = null;
			candidate.loading = true;
			void autoMatch(candidate);
		}
	}

	// ── Inline catalog search (active source) ──────────────────

	// Re-seed the query when the operator switches sources. Reads the
	// candidate list untracked so background metadata updates never wipe a
	// search the operator is running.
	$effect(() => {
		const id = activeId;
		untrack(() => {
			searchGeneration += 1;
			searching = false;
			searched = false;
			searchedQuery = '';
			searchResults = [];
			searchError = null;
			const current = candidates.find((c) => c.bookId === id) ?? null;
			searchQuery = current ? stripExtension(current.title) : '';
			searchFormQuery = searchQuery;
		});
	});

	async function runSearch() {
		const query = searchFormQuery.trim();
		if (!active || !query || searching) return;
		const generation = ++searchGeneration;
		searchedQuery = query;
		searching = true;
		searchError = null;
		searchResults = [];
		searched = false;
		try {
			const hits = await searchBooks(query);
			if (generation !== searchGeneration) return;
			searchResults = hits;
			searched = true;
			hydrateOptions(hits);
		} catch (e) {
			if (generation !== searchGeneration) return;
			searchError = toCleanedError(e);
			searched = true;
		} finally {
			if (generation === searchGeneration) searching = false;
		}
	}

	function pickSearchResult(book: AudiobookDBBook) {
		if (!active) return;
		void selectResult(active, book.id, true);
	}

	// ── Remove flow (two-step confirm/cancel) ──────────────────

	function removeCandidate(candidate: MatchCandidate) {
		// Keep the candidate in place until the operator explicitly confirms.
		candidate.removing = true;
		candidate._removeSnapshot = {
			bookId: candidate.bookId,
			srcPath: candidate.srcPath,
			title: candidate.title
		};
	}

	async function confirmRemoveCandidate(candidate: MatchCandidate) {
		if (!candidate._removeSnapshot) return;
		const id = candidate._removeSnapshot.bookId;
		candidate.removingConfirming = true;
		if (mode === 'batch') {
			// Keep focus on the confirmation during the request, in case the
			// disabled state drop it.
			await tick();
			document
				.querySelector<HTMLButtonElement>(
					`.batch__removal[data-book-id='${id}'] [data-yes]`
				)
				?.focus();
		}
		try {
			await del(`/books/${id}`);
			const wasActive = activeId === id;
			const removedIndex = candidates.findIndex((c) => c.bookId === id);
			candidate.removing = false;
			candidate.removingConfirming = false;
			candidates = candidates.filter((c) => c.bookId !== id);
			// The row is gone from every set — a failure report about it is
			// stale, and per-row deletes never run while a bulk delete is
			// in flight, so clearing cannot lose a fresh report.
			removeFailure = null;
			if (wasActive) {
				activeId =
					candidates[Math.min(Math.max(removedIndex, 0), candidates.length - 1)]?.bookId ??
					null;
			}
			// The confirmation button is gone with the row; hand focus to a
			// surviving control so the operator stays in the flow.
			await tick();
			if (mode === 'batch') {
				const next = candidates[removedIndex] ?? candidates[candidates.length - 1];
				if (next) document.getElementById(`batch-remove-${next.bookId}`)?.focus();
			} else {
				document.getElementById('focused-remove')?.focus();
			}
		} catch (e) {
			pageError = toCleanedError(e);
			candidate.removing = false;
			candidate.removingConfirming = false;
			candidate._removeSnapshot = undefined;
			if (mode === 'batch') void returnFocusToBatchRemove(id);
		}
	}

	function cancelRemoveCandidate(candidate: MatchCandidate) {
		candidate.removing = false;
		candidate._removeSnapshot = undefined;
	}

	/** Shared confirmation copy: the record goes, the files stay. */
	function removeConfirmCopy(candidate: MatchCandidate): string {
		return `Remove “${stripExtension(candidate.title)}” from the batch? This removes the book’s record from Bragi Books; files on disk are not touched.`;
	}

	/** Batch rows: same two-step flow, with explicit focus handling. */
	async function removeCandidateBatch(candidate: MatchCandidate) {
		if (removingMany || saving) return;
		removeCandidate(candidate);
		await tick();
		// focus="visible" targets only the danger button if it is visible,
		// which is not the case on narrow viewports where the band wraps —
		// so query the band itself.
		const band = document.querySelector(
			`.batch__removal[data-book-id='${candidate.bookId}']`
		);
		(
			band?.querySelector<HTMLButtonElement>('[data-yes]') ??
			band?.querySelector<HTMLButtonElement>('button')
		)
			?.focus();
	}

	function cancelBatchRemove(candidate: MatchCandidate) {
		cancelRemoveCandidate(candidate);
		void returnFocusToBatchRemove(candidate.bookId);
	}

	async function returnFocusToBatchRemove(id: number) {
		await tick();
		document.getElementById(`batch-remove-${id}`)?.focus();
	}

	// ── Batch removal — the dialog's checked set ───────────────
	// The removal dialog owns the destructive selection. The page's include
	// boxes only choose the save set. The dialog opens empty (or with failed
	// survivors after a retry) and cannot run while a save, a bulk delete, or
	// a per-row delete is in flight.

	function resetRemovalState() {
		removeDialogOpen = false;
		removePick = new Set();
		removeFailure = null;
		removalNotice = '';
	}

	async function openRemoveDialog(preselect: number[] = []) {
		if (removingMany || saving || removalInFlight || candidates.length === 0) return;
		// Retire any inline per-row confirmation so two removal flows never
		// share a row.
		for (const candidate of candidates) {
			if (candidate.removing) cancelRemoveCandidate(candidate);
		}
		removeFailure = null;
		removePick = new Set(preselect.filter((id) => candidates.some((c) => c.bookId === id)));
		removeDialogOpen = true;
		await tick();
		const first = candidates.find((c) => removePick.has(c.bookId)) ?? candidates[0];
		if (first) document.getElementById(`remove-pick-${first.bookId}`)?.focus();
	}

	async function cancelRemoveDialog() {
		removeDialogOpen = false;
		removePick = new Set();
		await focusBatchActions();
	}

	/** Return focus to the head's red action (always present in batch review),
	 *  or the gold Save when it is not live. Never focus a disabled button. */
	async function focusBatchActions() {
		await tick();
		const remove = document.getElementById('batch-remove-open') as HTMLButtonElement | null;
		if (remove && !remove.disabled) {
			remove.focus();
			return;
		}
		const save = document.querySelector<HTMLButtonElement>('.match__save');
		if (save && !save.disabled) {
			save.focus();
			return;
		}
		document.getElementById('match-title')?.focus();
	}

	function toggleRemovePick(candidate: MatchCandidate) {
		if (removingMany) return;
		const next = new Set(removePick);
		if (next.has(candidate.bookId)) next.delete(candidate.bookId);
		else next.add(candidate.bookId);
		removePick = next;
	}

	function toggleAllRemovePicks(event: Event) {
		if (removingMany) return;
		const input = event.currentTarget as HTMLInputElement;
		removePick = input.checked ? new Set(candidates.map((c) => c.bookId)) : new Set();
	}

	/** Delete the checked set one record at a time. A failure never hides the
	 *  records that did not go: their ids stay checked for the retry. */
	async function confirmRemoveMany() {
		if (removingMany) return;
		const targets = candidates.filter((c) => removePick.has(c.bookId));
		if (targets.length === 0) return;
		removingMany = true;
		removeFailure = null;
		removalNotice = '';
		let removed = 0;
		let failed = 0;
		let failure: CleanedAdbError | null = null;
		const failedIds: number[] = [];
		for (const target of targets) {
			removingNow = target.bookId;
			try {
				await del(`/books/${target.bookId}`);
				removed += 1;
				candidates = candidates.filter((c) => c.bookId !== target.bookId);
			} catch (e) {
				failed += 1;
				failedIds.push(target.bookId);
				failure ??= toCleanedError(e);
			}
		}
		removingNow = null;
		removingMany = false;
		removeDialogOpen = false;
		if (removed > 0) {
			removalNotice = `Removed ${removed} ${removed === 1 ? 'record' : 'records'}.`;
		}
		if (failed > 0 && failure) {
			removePick = new Set(failedIds);
			removeFailure = {
				failed,
				attempted: targets.length,
				error: failure,
				ids: failedIds
			};
			await tick();
			// The alert carries the retry action; bring the cause and the recovery
			// below the docked toolbar before focusing it.
			document.querySelector<HTMLElement>('.batch__alert')?.scrollIntoView({ block: 'start' });
			document
				.querySelector<HTMLButtonElement>('.batch__alert button')
				?.focus({ preventScroll: true });
			return;
		}
		removePick = new Set();
		await focusBatchActions();
	}

	// ── People / metadata helpers ──────────────────────────────

	/** Merge book- and release-level people; one person per role. */
	function rolePeople(
		book: AudiobookDBBook | null,
		release: AudiobookDBRelease | null,
		role: string
	): { name: string; audiobookdb_person_id: string | null }[] {
		const wanted = role.toLowerCase();
		const seenIds = new Set<string>();
		const seenNames = new Set<string>();
		const out: { name: string; audiobookdb_person_id: string | null }[] = [];
		for (const entry of [...(book?.people ?? []), ...(release?.people ?? [])]) {
			const entryRole = entry?.role?.name?.toLowerCase();
			const name = entry?.person?.name ?? '';
			if (entryRole !== wanted || !name) continue;
			const id = entry.person?.id || '';
			const nameKey = name.toLowerCase();
			// IDs are authoritative; only credits without an ID fall back to
			// name deduplication so distinct people sharing a name survive.
			if (id) {
				if (seenIds.has(id)) continue;
				seenIds.add(id);
			} else if (seenNames.has(nameKey)) {
				continue;
			}
			seenNames.add(nameKey);
			out.push({ name, audiobookdb_person_id: id || null });
		}
		return out;
	}

	function roleNames(book: AudiobookDBBook | null, release: AudiobookDBRelease | null, role: string) {
		return rolePeople(book, release, role)
			.map((p) => p.name)
			.join(', ');
	}

	function releaseRuntimeMs(release: AudiobookDBRelease | null): number {
		if (!release) return 0;
		if (release.runtimeLengthMs) return release.runtimeLengthMs;
		if (release.runtimeLengthSec) return release.runtimeLengthSec * 1000;
		return 0;
	}

	function runtimeLabel(ms: number): string {
		if (!ms || ms <= 0) return '';
		const minutes = Math.round(ms / 60000);
		const h = Math.floor(minutes / 60);
		const m = minutes % 60;
		if (h <= 0) return `${m}m`;
		return m ? `${h}h ${m}m` : `${h}h`;
	}

	function yearOf(value: string | null | undefined): string {
		if (!value) return '';
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? '' : String(date.getUTCFullYear());
	}

	function dateLabel(value: string | null | undefined): string {
		if (!value) return '';
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		return date.toLocaleDateString(undefined, {
			year: 'numeric',
			month: 'short',
			day: 'numeric',
			timeZone: 'UTC'
		});
	}

	function plainText(value: string | null | undefined): string {
		if (!value) return '';
		return decodeHtmlEntities(value.replace(/<[^>]*>/g, ' ')).replace(/\s+/g, ' ').trim();
	}

	/** Series records carry IDs and positions only. A display name is used
	 *  solely when upstream actually provides one — never the ID. */
	function seriesRecordName(series: unknown): string {
		if (!series || typeof series !== 'object') return '';
		const record = series as Record<string, unknown>;
		for (const key of ['name', 'title', 'label']) {
			const value = record[key];
			if (typeof value === 'string' && value.trim()) return value.trim();
		}
		const nested = record.series;
		if (nested && typeof nested === 'object') {
			const inner = nested as Record<string, unknown>;
			for (const key of ['name', 'title', 'label']) {
				const value = inner[key];
				if (typeof value === 'string' && value.trim()) return value.trim();
			}
		}
		return '';
	}

	function valueFor(record: unknown, key: string): string {
		if (!record || typeof record !== 'object') return '';
		const value = (record as Record<string, unknown>)[key];
		return typeof value === 'string' ? value.trim() : '';
	}

	/** Upstream sends series positions as a number, a string, or an object
	 *  carrying `value`/`label`. Accept those forms, never invent one. */
	function seriesPositionValue(series: unknown): string {
		if (!series || typeof series !== 'object') return '';
		const position = (series as Record<string, unknown>).position;
		if (typeof position === 'number' && Number.isFinite(position)) {
			return String(position);
		}
		if (typeof position === 'string' && position.trim()) return position.trim();
		if (position && typeof position === 'object') {
			const inner = position as Record<string, unknown>;
			const value = inner.value ?? inner.label;
			if (typeof value === 'number' || (typeof value === 'string' && value.trim())) {
				return String(value).trim();
			}
		}
		return '';
	}

	function selectedTitle(candidate: MatchCandidate): string {
		return (
			candidate.details.release?.title ||
			candidate.details.book?.title ||
			candidate.title
		);
	}

	function selectedAuthors(candidate: MatchCandidate): string {
		return (
			roleNames(candidate.details.book, candidate.details.release, 'author') ||
			'Unknown author'
		);
	}

	function selectedNarrators(candidate: MatchCandidate): string {
		return roleNames(candidate.details.book, candidate.details.release, 'narrator');
	}

	/** The quiet credit line under a batch record; also its tooltip, so a
	 *  clipped narrator list stays recoverable without opening Focused. */
	function recordCredits(candidate: MatchCandidate): string {
		const parts = [selectedAuthors(candidate)];
		const narrators = selectedNarrators(candidate);
		if (narrators) parts.push(narrators);
		const runtime = runtimeLabel(releaseRuntimeMs(candidate.details.release));
		if (runtime) parts.push(runtime);
		return parts.join(' · ');
	}

	function selectedCover(candidate: MatchCandidate): string {
		return (
			pickCover(candidate.details.release?.images) ||
			pickCover(candidate.details.book?.images) ||
			''
		);
	}

	function editionLine(candidate: MatchCandidate): string {
		const book = candidate.details.book;
		const release = candidate.details.release;
		const parts: string[] = [];
		if (release?.title && book?.title && release.title !== book.title) {
			parts.push(release.title);
		}
		if (book?.disambiguation) parts.push(book.disambiguation);
		return parts.join(' · ');
	}

	// Only a real audio extension is an extension: a period that is part of the
	// name (author initials, "Dr.", "Vol. 2") must survive, or auto-match
	// searches the catalog for a truncated title.
	function stripExtension(name: string): string {
		return name.replace(AUDIO_FILE_EXTS, '');
	}

	function pathLast(name: string): string {
		const trimmed = name.replace(/\/+$/, '');
		const i = trimmed.lastIndexOf('/');
		return i >= 0 ? trimmed.slice(i + 1) : trimmed;
	}

	function sourceBase(candidate: MatchCandidate): string {
		return pathLast(candidate.srcPath) || candidate.title;
	}

	// ── Proposed output naming ─────────────────────────────────
	// Preview only: the processor writes ASIN + scheme and reports the actual
	// final path. Resolved through the shared scheme resolver, so this
	// preview agrees with the Settings example (Go segment rules, no
	// double `.m4b`). As with that resolver, unknown placeholders stay
	// visible (unrecognized spellings are literal), and `{asin}` stays
	// literal — m4b-merge does not substitute it (engine parity).

	function schemeValues(candidate: MatchCandidate): Record<string, string> {
		const book = candidate.details.book;
		const release = candidate.details.release;
		const series = book?.series?.[0];
		const releaseDate = release?.releaseDate || book?.originallyPublishedAt || '';
		return {
			author: roleNames(book, release, 'author'),
			narrator: roleNames(book, release, 'narrator'),
			series_name: seriesRecordName(series),
			series_position: seriesPositionValue(series),
			subtitle: valueFor(release, 'subtitle') || valueFor(book, 'subtitle'),
			title: release?.title || book?.title || '',
			year: yearOf(releaseDate)
		};
	}

	// ── Output naming preview (global Settings) ───────────────
	function proposedRelativeName(candidate: MatchCandidate): string {
		if (!outputScheme || !candidate.details.book) return '';
		return resolveScheme(outputScheme, schemeValues(candidate));
	}

	function fullOutputPath(candidate: MatchCandidate): string {
		if (!outputScheme) return 'Output scheme is not set — configure it in Settings.';
		if (!candidate.details.book) return 'Select a record to preview the output name.';
		if (!outputDir) return 'Output folder is not set — configure it in Settings.';
		const relative = proposedRelativeName(candidate);
		const trimmed = outputDir.replace(/\/+$/, '');
		return relative ? `${trimmed}/${relative}` : trimmed;
	}

	// ── Eligibility — one predicate for counting and saving ────

	/** Loaded and internally consistent for saving. A source pending removal
	 *  is never ready; inclusion itself is applied by isSavable. */
	function isReady(candidate: MatchCandidate): boolean {
		if (candidate.removing || candidate.loading || candidate.releaseLoading) return false;
		if (candidate.error || candidate.releaseError) return false;
		const book = candidate.details.book;
		if (!book || !candidate.selectedBookId || book.id !== candidate.selectedBookId) return false;
		if (candidate.selectedReleaseId) {
			return candidate.details.release?.id === candidate.selectedReleaseId;
		}
		return candidate.details.release == null;
	}

	/** The exact set counted in the approval action and written on save. */
	function isSavable(candidate: MatchCandidate): boolean {
		return candidate.included && isReady(candidate);
	}

	function toggleAllReady(event: Event) {
		if (removingMany || saving) return;
		const input = event.currentTarget as HTMLInputElement;
		const include = input.checked;
		for (const candidate of candidates) {
			if (isReady(candidate)) candidate.included = include;
		}
		removalNotice = '';
	}

	/** Inclusion is the save set; changing it re-arms the live region so a
	 *  stale removal sentence never narrates the new count. */
	function setIncluded(candidate: MatchCandidate, included: boolean) {
		if (removingMany || saving) return;
		candidate.included = included;
		removalNotice = '';
	}

	function measureDescriptionOverflow(
		node: HTMLElement,
		value: { candidate: MatchCandidate; description: string; open: boolean }
	) {
		let current = value;
		let frame = 0;
		const measure = () => {
			cancelAnimationFrame(frame);
			frame = requestAnimationFrame(() => {
				const wasOpen = node.classList.contains('match__desc-text--open');
				if (wasOpen) node.classList.remove('match__desc-text--open');
				current.candidate.descriptionOverflow = node.scrollHeight > node.clientHeight + 1;
				if (wasOpen) node.classList.add('match__desc-text--open');
			});
		};
		const observer = new ResizeObserver(measure);
		observer.observe(node);
		measure();
		return {
			update(next: typeof current) {
				current = next;
				measure();
			},
			destroy() {
				observer.disconnect();
				cancelAnimationFrame(frame);
			}
		};
	}

	function setIndeterminate(node: HTMLInputElement, value: boolean) {
		node.indeterminate = value;
		return {
			update(next: boolean) {
				node.indeterminate = next;
			}
		};
	}

	type MatchStateKind = 'auto' | 'manual' | 'none' | 'low' | 'loading' | 'error';

	/** The catalog match state — the existing manualPick, details.book,
	 *  searchResults, error, releaseError, and readiness flags only. No
	 *  invented confidence scores. */
	function matchState(candidate: MatchCandidate): { kind: MatchStateKind; text: string } {
		if (candidate.removing || removingNow === candidate.bookId) {
			return {
				kind: 'loading',
				text:
					candidate.removingConfirming || removingNow === candidate.bookId
						? 'Removing…'
						: 'Removal pending'
			};
		}
		if (candidate.loading && !candidate.details.book && !candidate.error)
			return { kind: 'loading', text: 'Loading record…' };
		if (candidate.error) return { kind: 'error', text: 'Catalog lookup failed' };
		if (candidate.releaseError) return { kind: 'error', text: 'Edition failed to load' };
		if (!candidate.details.book) {
			return candidate.searchResults.length === 0
				? { kind: 'none', text: 'No catalog match' }
				: { kind: 'low', text: 'No confident match' };
		}
		if (!isReady(candidate)) return { kind: 'loading', text: 'Loading edition…' };
		return candidate.manualPick
			? { kind: 'manual', text: 'Matched manually' }
			: { kind: 'auto', text: 'Auto-matched' };
	}

	/** Honest per-source status for the focused index and the batch chip. */
	function statusText(candidate: MatchCandidate): string {
		if (candidate.details.book && isReady(candidate) && !candidate.included)
			return 'Excluded from save';
		return matchState(candidate).text;
	}

	function statusKind(
		candidate: MatchCandidate
	): 'auto' | 'manual' | 'none' | 'low' | 'loading' | 'error' | 'excluded' {
		if (candidate.details.book && isReady(candidate) && !candidate.included) return 'excluded';
		return matchState(candidate).kind;
	}

	// ── Rich option metadata (hydrated, cached) ────────────────

	function optionMeta(bookId: string, hit: AudiobookDBBook) {
		const entry = hydration[bookId];
		const book = entry?.book ?? hit;
		const release = entry?.release ?? null;
		const authors = roleNames(book, release, 'author');
		const narrators = roleNames(book, release, 'narrator');
		const runtime = runtimeLabel(releaseRuntimeMs(release));
		const disambiguation = book.disambiguation || hit.disambiguation || '';
		const editionTitle =
			release?.title && book.title && release.title !== book.title ? release.title : '';
		const edition = [editionTitle, disambiguation].filter(Boolean).join(' · ');
		const state: 'loading' | 'error' | 'ready' = entry
			? entry.loading
				? 'loading'
				: entry.error
					? 'error'
					: 'ready'
			: 'loading';
		return { authors, narrators, runtime, edition, state };
	}

	/** Edition labels lead with what actually distinguishes releases: narrator,
	 *  runtime and release date. A title shared with the book is noise; a
	 *  missing title falls back to the real release ID. */
	function editionOptionLabel(option: { id: string; title: string }, bookTitle: string): string {
		const entry = editionDetails[option.id];
		const release = entry?.release ?? null;
		const title = release?.title || option.title || '';
		const parts: string[] = [];
		if (title && title !== bookTitle) parts.push(title);
		if (release) {
			const narrators = roleNames(null, release, 'narrator');
			const runtime = runtimeLabel(releaseRuntimeMs(release));
			const released = dateLabel(release.releaseDate);
			if (narrators) parts.push(narrators);
			if (runtime) parts.push(runtime);
			if (released) parts.push(released);
			// Publisher only helps when no stronger differentiator exists.
			if (!narrators && !runtime && !released && release.publisher?.name) {
				parts.push(release.publisher.name);
			}
		}
		const state: 'loading' | 'error' | 'ready' = !entry
			? 'loading'
			: entry.loading
				? 'loading'
				: entry.error
					? 'error'
					: 'ready';
		if (state === 'loading') {
			return parts.length > 0
				? `${parts.join(' · ')} · loading details…`
				: `${title || `Release ${option.id}`} · loading details…`;
		}
		if (state === 'error') {
			return parts.length > 0
				? `${parts.join(' · ')} · details unavailable`
				: `${title || `Release ${option.id}`} · details unavailable · #${option.id}`;
		}
		if (parts.length === 0) {
			return title ? `${title} · #${option.id}` : `Release ${option.id}`;
		}
		return parts.join(' · ');
	}

	// ── Candidate picker — in-flow rich listbox ────────────────

	function togglePicker(candidate: MatchCandidate) {
		if (pickerCandidateId === candidate.bookId) {
			closePicker();
		} else {
			openPicker(candidate);
		}
	}

	function openPicker(candidate: MatchCandidate) {
		pickerCandidateId = candidate.bookId;
		// Reopening is an explicit request: recover options whose detail
		// request previously failed, while successful entries stay cached.
		hydrateOptions(candidate.searchResults, true);
		// Cursor starts on the current selection, else the first hit
		const idx = candidate.searchResults.findIndex((b) => b.id === candidate.selectedBookId);
		pickerIndex = idx >= 0 ? idx : candidate.searchResults.length > 0 ? 0 : -1;
	}

	function closePicker() {
		pickerCandidateId = null;
		pickerIndex = -1;
	}

	function pickResult(candidate: MatchCandidate, book: AudiobookDBBook) {
		const triggerId = `match-picker-trigger-${candidate.bookId}`;
		void selectResult(candidate, book.id, true).finally(() => {
			const trigger = document.getElementById(triggerId);
			if (trigger instanceof HTMLButtonElement) trigger.focus();
		});
	}

	function movePickerCursor(delta: number) {
		const candidate = pickerCandidate;
		if (!candidate) return;
		const n = candidate.searchResults.length;
		if (n === 0) return;
		pickerIndex = (pickerIndex + delta + n) % n;
	}

	function movePickerIndex(i: number) {
		const candidate = pickerCandidate;
		if (candidate && i >= 0 && i < candidate.searchResults.length) pickerIndex = i;
	}

	function pickerKeydown(event: KeyboardEvent, candidate: MatchCandidate) {
		switch (event.key) {
			case 'ArrowDown':
				event.preventDefault();
				if (pickerCandidateId === candidate.bookId) {
					movePickerCursor(1);
				} else {
					openPicker(candidate);
				}
				break;
			case 'ArrowUp':
				event.preventDefault();
				if (pickerCandidateId === candidate.bookId) {
					movePickerCursor(-1);
				}
				break;
			case 'Enter':
			case ' ':
				event.preventDefault();
				if (pickerCandidateId === candidate.bookId) {
					if (pickerIndex >= 0 && pickerIndex < candidate.searchResults.length) {
						pickResult(candidate, candidate.searchResults[pickerIndex]);
					}
				} else {
					openPicker(candidate);
				}
				break;
			case 'Escape':
				event.preventDefault();
				closePicker();
				break;
		}
	}

	function pickedTitle(candidate: MatchCandidate): string {
		return (
			candidate.searchResults.find((b) => b.id === candidate.selectedBookId)?.title ??
			candidate.details.book?.title ??
			''
		);
	}

	// ── Batch save ─────────────────────────────────────────────

	async function saveMatches() {
		if (removingMany || saving) return;
		const ready = candidates.filter(isSavable);
		if (ready.length === 0) return;
		saving = true;
		pageError = null;
		try {
			await Promise.all(
				ready.map(async (candidate) => {
					await put<BookWithPeople>(`/books/${candidate.bookId}`, buildUpdate(candidate));
				})
			);
			await goto('/process');
		} catch (e) {
			pageError = toCleanedError(e);
			throw e;
		} finally {
			saving = false;
		}
	}

	function buildUpdate(candidate: MatchCandidate): UpdateBookRequest {
		const book = candidate.details.book;
		const release = candidate.details.release;
		const releaseDate = release?.releaseDate || book?.originallyPublishedAt || '';
		const runtimeMs = releaseRuntimeMs(release);
		const series = (book?.series ?? [])
			.map((entry) => {
				const position = seriesPositionValue(entry);
				return [entry.seriesId, position ? `#${position}` : ''].filter(Boolean).join(' ');
			})
			.filter(Boolean)
			.join(', ');
		return {
			title: release?.title || book?.title || candidate.title,
			asin: extractAsin(book?.external) || undefined,
			audiobookdb_book_id: book?.id,
			audiobookdb_release_id: release?.id,
			description: book?.description || undefined,
			release_date: releaseDate,
			series: series || undefined,
			publisher: release?.publisher?.name || undefined,
			language: release?.language?.title || undefined,
			runtime_length_minutes: runtimeMs ? Math.round(runtimeMs / 60000) : undefined,
			cover_image_url: selectedCover(candidate),
			status: 'matched',
			authors: rolePeople(book, release, 'author'),
			narrators: rolePeople(book, release, 'narrator')
		};
	}

	$effect(() => {
		loadPending();
	});
</script>

<svelte:head>
	<title>Match — Bragi Books</title>
</svelte:head>

<svelte:window
	onclick={(e) => {
		if (
			pickerCandidateId !== null &&
			!(e.target instanceof Element && e.target.closest('.match-picker, .match-picker__trigger'))
		) {
			closePicker();
		}
	}}
/>

{#snippet matchChip(kind: MatchStateKind | 'excluded', text: string)}
	<span class="match__label match__label--{kind}">
		{#if kind === 'auto' || kind === 'manual'}
			<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 4 4 10-10" /></svg>
		{:else if kind === 'loading'}
			<span class="match__label-dot" aria-hidden="true"></span>
		{:else if kind === 'excluded'}
			<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14" /></svg>
		{:else}
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M12 8v4m0 4h.01M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z" />
			</svg>
		{/if}
		{text}
	</span>
{/snippet}

<div class="match" style="--head-h: {headHeight}px">
	<p class="sr-only" aria-live="polite">{removalNotice || announcements}</p>
	<div class="match__head" bind:this={headEl}>
		<div class="match__head-in">
			<div class="match__intro">
				<h1 class="match__title" id="match-title" tabindex="-1">Match metadata</h1>
			</div>
			{#if candidates.length > 0}
				<div class="match__head-actions">
					<div class="match__modes" role="group" aria-label="Review mode">
						<button
							type="button"
							class="match__mode"
							aria-pressed={mode === 'batch'}
							disabled={removingMany}
							onclick={() => setMode('batch')}
						>
							Batch review
						</button>
						<button
							type="button"
							class="match__mode"
							aria-pressed={mode === 'focused'}
							disabled={removingMany}
							onclick={() => setMode('focused')}
						>
							Focused review
						</button>
					</div>
					<div class="match__commit-row">
						{#if mode === 'batch'}
							<Button
								variant="danger"
								class="match__remove"
								id="batch-remove-open"
								aria-haspopup="dialog"
								aria-label="Remove sources"
								disabled={saving || removingMany || removalInFlight}
								onclick={() => openRemoveDialog()}
							>
								Remove…
							</Button>
						{/if}
						<Button
							class="match__save"
							loading={saving}
							disabled={includedReadyCount === 0 || removingMany}
							onclick={saveMatches}
						>
							{#if saving}
								Saving…
							{:else}
								Save {includedReadyCount}<span class="sr-only">
									matches to the queue</span
								><span aria-hidden="true"> → Queue</span>
							{/if}
						</Button>
					</div>
				</div>
			{:else if !initialLoadComplete}
				<div class="match__head-actions" aria-hidden="true">
					<Skeleton height="40px" width="13rem" />
					<Skeleton height="44px" width="11rem" />
				</div>
			{/if}
		</div>
	</div>

	{#if pageError}
		<div class="match__alert">
			<Alert variant="error" onretry={loadPending}>
				{pageError.message}
				<ErrorDetail raw={pageError.raw} needsSettings={pageError.needsSettings} />
			</Alert>
		</div>
	{/if}

	{#if !initialLoadComplete || dl.showSkeleton}
		{#if mode === 'batch'}
			<div class="batch" aria-busy="true" aria-label="Loading sources">
				<div class="batch__bar">
					<Skeleton height="1rem" width="9rem" />
				</div>
				{#each Array(6) as _}
					<div class="batch__row batch__row--skeleton">
						<div class="batch__skel-check">
							<Skeleton variant="rect" width="18px" height="18px" />
						</div>
						<div class="batch__skel-thumb">
							<Skeleton variant="rect" width="40px" height="40px" />
						</div>
						<div class="batch__skel-block batch__skel-names">
							<Skeleton height="0.8rem" width="10rem" />
							<Skeleton height="0.7rem" width="7rem" />
						</div>
						<div class="batch__skel-record">
							<Skeleton variant="rect" width="40px" height="40px" />
							<div class="batch__skel-block">
								<Skeleton height="0.8rem" width="9rem" />
								<Skeleton height="0.7rem" width="12rem" />
							</div>
						</div>
						<div class="batch__skel-action">
							<Skeleton height="2rem" width="5rem" />
						</div>
					</div>
				{/each}
			</div>
		{:else}
			<div class="match__grid" aria-busy="true" aria-label="Loading sources">
				<div class="match__index">
					<div class="match__index-head">
						<Skeleton height="1rem" width="6rem" />
						<Skeleton height="0.8rem" width="4rem" />
					</div>
					{#each Array(5) as _}
						<div class="match__src match__src--skeleton">
							<div class="match__skel-thumb">
								<Skeleton variant="rect" width="40px" height="40px" />
							</div>
							<div class="batch__skel-block">
								<Skeleton height="0.8rem" width="8rem" />
								<Skeleton height="0.7rem" width="5.5rem" />
							</div>
						</div>
					{/each}
				</div>
				<div class="match__inspector">
					<div class="match__source">
						<div class="batch__skel-block">
							<Skeleton height="1.25rem" width="12rem" />
							<Skeleton height="0.8rem" width="9rem" />
							<Skeleton height="0.8rem" width="7rem" />
						</div>
						<Skeleton height="2rem" width="8rem" />
					</div>
					<div class="match__record">
						<Skeleton variant="rect" width="128px" height="128px" />
						<div class="batch__skel-block">
							<Skeleton height="1.5rem" width="16rem" />
							<Skeleton height="0.9rem" width="11rem" />
							<Skeleton height="0.9rem" width="14rem" />
							<Skeleton height="0.8rem" width="18rem" />
						</div>
					</div>
				</div>
			</div>
		{/if}
	{:else if candidates.length === 0}
		<EmptyState
			title="Nothing to match"
			description="Import source directories first, then come back here to choose metadata."
			actionLabel="Import books"
			actionHref="/import"
		/>
	{:else if mode === 'batch'}
		<!-- ── Batch review — every source, one approval ──────── -->
		<section class="batch" aria-label="Batch review" aria-busy={removingMany}>
			<div class="batch__bar">
				<label class="batch__all">
					<input
						type="checkbox"
						checked={allReadyIncluded}
						use:setIndeterminate={someReadyIncluded && !allReadyIncluded}
						disabled={readyCandidates.length === 0 || removingMany || saving}
						onchange={toggleAllReady}
					/>
					<span>Select all ready</span>
				</label>
				<span class="batch__count">
					{readyCandidates.length} ready · {includedReadyCount} of {candidates.length}
					{candidates.length === 1 ? 'book' : 'books'} included
				</span>
			</div>
			{#if removeFailure}
				<div class="batch__alert">
					<Alert
						variant="error"
						onretry={() => openRemoveDialog(removeFailure?.ids ?? [])}
					>
						<p class="batch__alert-lead">
							{#if removeFailure.failed === removeFailure.attempted}
								No records were removed.
							{:else}
								{removeFailure.failed} of {removeFailure.attempted} records were not
								removed.
							{/if}
							They stay in the batch; remove them again to retry.
						</p>
						<p>{removeFailure.error.message}</p>
						<ErrorDetail
							raw={removeFailure.error.raw}
							needsSettings={removeFailure.error.needsSettings}
						/>
					</Alert>
				</div>
			{/if}

			<ul class="batch__list">
				{#each candidates as c (c.bookId)}
					{@const ready = isReady(c)}
					{@const selected = c.details.book}
					{@const proposed = proposedRelativeName(c)}
					{@const rowKind = statusKind(c)}
					{@const rowText = statusText(c)}
					<li
						class="batch__row"
						class:batch__row--excluded={ready && !c.included}
						aria-busy={removingNow === c.bookId}
					>
						<label class="batch__check">
							<input
								type="checkbox"
								checked={c.included && ready}
								disabled={!ready || removingMany || saving}
								onchange={(e) => setIncluded(c, e.currentTarget.checked)}
								aria-label="Include {sourceBase(c)} in this save"
							/>
						</label>

						{#if c.coverUrl}
							<img
								class="batch__thumb"
								src={bookCoverUrl(c.coverUrl, 'small')}
								alt=""
								loading="lazy"
								decoding="async"
							/>
						{:else}
							<span class="batch__thumb batch__thumb--empty" aria-hidden="true"></span>
						{/if}

						<div class="batch__names">
							<div class="batch__name">
								<span class="batch__name-head">
									<span class="batch__name-label">Before</span>
								</span>
								<span
									class="batch__name-value batch__name-value--source"
									title={c.srcPath}
								>
									{#if c.coverUrl}
										<img
											class="batch__name-art"
											src={bookCoverUrl(c.coverUrl, 'small')}
											alt=""
											loading="lazy"
											decoding="async"
										/>
									{/if}
									<span class="batch__name-source-text">{sourceBase(c)}</span>
								</span>
							</div>
							<svg class="batch__arrow" viewBox="0 0 24 24" aria-hidden="true">
								<path d="M5 12h13m-5-5 5 5-5 5" />
							</svg>
							<div class="batch__name">
								<span class="batch__name-head">
									<span class="batch__name-label">After</span>
									<span class="batch__name-note">proposed</span>
								</span>
								{#if !outputScheme}
									<a class="batch__name-link" href="/settings">
										{settingsLoaded
											? 'Set an output scheme'
											: 'Check output settings'}
									</a>
								{:else if selected && proposed}
									<code
										class="batch__name-value batch__name-value--code"
										title={fullOutputPath(c)}
									>
										{proposed}
									</code>
								{:else if selected}
									<span class="batch__name-value batch__name-value--empty">
										Output scheme is empty
									</span>
								{:else}
									<span class="batch__name-value batch__name-value--empty">
										Select a record
									</span>
								{/if}
							</div>
						</div>

						<div class="batch__record">
							{#if selected}
								{@const catalogCover = selectedCover(c)}
								<span
									class="batch__record-art"
									class:batch__record-art--empty={!catalogCover}
									aria-hidden="true"
								>
									{#if catalogCover}
										<img src={catalogCover} alt="" loading="lazy" decoding="async" />
									{/if}
								</span>
								<span class="batch__record-body">
									<span class="batch__record-title">
										<span class="batch__record-name" title={selectedTitle(c)}
											>{selectedTitle(c)}</span
										>
										<AbdTag href={`https://audiobookdb.org/books/${selected.id}`} />
									</span>
									<span class="batch__record-meta-line">
										{@render matchChip(rowKind, rowText)}
										<span class="batch__record-meta" title={recordCredits(c)}>
											{recordCredits(c)}
										</span>
									</span>
									{#if editionLine(c)}
										<span class="batch__record-edition">{editionLine(c)}</span>
									{/if}
								</span>
							{:else}
								<span class="batch__record-body">
									{@render matchChip(rowKind, rowText)}
								</span>
							{/if}
						</div>

						<div class="batch__actions">
							<div class="batch__btns">
								<Button
									variant="secondary"
									class="batch__review"
									aria-label="Review {sourceBase(c)}"
									disabled={saving || removingMany}
									onclick={() => openFocused(c.bookId)}
								>
									Review
								</Button>
								{#if !c.removing}
									<Button
										variant="ghost"
										class="batch__remove"
										id={`batch-remove-${c.bookId}`}
										aria-label="Remove {sourceBase(c)} from the batch"
										disabled={saving || removingMany}
										onclick={() => removeCandidateBatch(c)}
									>
										Remove
									</Button>
								{/if}
							</div>
						</div>

						{#if c.removing}
							<div
								class="batch__removal"
								data-book-id={c.bookId}
								role="alert"
								aria-busy={c.removingConfirming}
							>
								<span id={`batch-removal-${c.bookId}`} tabindex="-1">
									{removeConfirmCopy(c)}
								</span>
								<span class="batch__removal-actions">
									<Button
										variant="danger"
										data-yes
										aria-describedby={`batch-removal-${c.bookId}`}
										loading={c.removingConfirming}
										onclick={() => confirmRemoveCandidate(c)}
									>
										{c.removingConfirming ? 'Removing…' : 'Yes, remove'}
									</Button>
									<Button
										variant="ghost"
										aria-describedby={`batch-removal-${c.bookId}`}
										disabled={c.removingConfirming}
										onclick={() => cancelBatchRemove(c)}
									>
										Cancel
									</Button>
								</span>
							</div>
						{/if}
					</li>
				{/each}
			</ul>

			<p class="batch__foot">
				Approval saves every included ready match and opens the Queue. Proposed names are
				previews; the processor builds the final name from the ASIN and scheme and reports
				the actual output path.
			</p>
			</section>
	{:else}
		<!-- ── Focused review — index + inspector ─────────────── -->
		<div class="match__grid">
			<aside class="match__index" aria-label="Sources in this batch">
				<div class="match__index-head">
					<h2>Sources</h2>
					<span>
						{candidates.length}
						{candidates.length === 1 ? 'book' : 'books'}
					</span>
				</div>
				<div class="match__index-list">
					{#each candidates as candidate (candidate.bookId)}
						{@const kind = statusKind(candidate)}
						<button
							type="button"
							class="match__src"
							class:match__src--active={candidate.bookId === activeId}
							aria-current={candidate.bookId === activeId ? 'true' : undefined}
							onclick={() => setActive(candidate.bookId)}
						>
							{#if candidate.coverUrl}
								<img
									class="match__thumb"
									src={bookCoverUrl(candidate.coverUrl, 'small')}
									alt=""
									loading="lazy"
									decoding="async"
								/>
							{:else}
								<span class="match__thumb match__thumb--empty" aria-hidden="true"></span>
							{/if}
							<span class="match__src-body min-w-0">
								<strong>{stripExtension(candidate.title)}</strong>
								<span class="match__src-status" data-kind={kind}>
									{#if kind === 'loading'}
										<span class="match__dot match__dot--pulse" aria-hidden="true"></span>
									{:else}
										<span
											class="match__dot"
											class:match__dot--gold={kind === 'auto'}
											class:match__dot--green={kind === 'manual'}
											class:match__dot--amber={kind === 'low'}
											class:match__dot--red={kind === 'error'}
											class:match__dot--bare={kind === 'none' || kind === 'excluded'}
											aria-hidden="true"
										></span>
									{/if}
									{statusText(candidate)}
								</span>
							</span>
						</button>
					{/each}
				</div>
			</aside>

			{#if active}
				{@const c = active}
				<section class="match__inspector" aria-label="Selected source and catalog record">
					<!-- Source identity — local cover, deliberately distinct from catalog art -->
					<div class="match__source">
						<div class="match__source-id">
							<figure class="match__source-art">
								{#if c.coverUrl}
									<img
										class="match__source-cover"
										src={bookCoverUrl(c.coverUrl, 'small')}
										alt=""
										decoding="async"
									/>
								{:else}
									<span
										class="match__source-cover match__source-cover--empty"
										aria-hidden="true"
									></span>
								{/if}
								<figcaption>Source cover</figcaption>
							</figure>
							<div class="match__source-copy">
								<h2>{sourceBase(c)}</h2>
								{#if outputScheme}
									<p class="match__proposed">
										<span>After (proposed) — processor reports the final path</span>
										<code title={fullOutputPath(c)}>
											{proposedRelativeName(c) || '—'}
										</code>
									</p>
								{/if}
								<details class="match__fullpath">
									<summary>Full source path</summary>
									<code>{c.srcPath}</code>
								</details>
								<details class="match__fullpath">
									<summary>Full output path</summary>
									<code>{fullOutputPath(c)}</code>
								</details>
							</div>
						</div>
						<div class="match__position">
							<label
								class="match__include"
								class:match__include--disabled={!isReady(c)}
							>
								<input
									type="checkbox"
									checked={isSavable(c)}
									disabled={!isReady(c)}
									onchange={(e) => setIncluded(c, e.currentTarget.checked)}
									aria-label="Include {sourceBase(c)} in this save"
								/>
								<span>Include in save</span>
							</label>
							<span class="match__count">{activeIndex + 1} of {candidates.length}</span>
							<button
								type="button"
								class="match__nav"
								disabled={activeIndex <= 0}
								aria-label="Previous source"
								onclick={() => setActive(candidates[activeIndex - 1].bookId)}
							>
								<svg class="match__icon" viewBox="0 0 24 24" aria-hidden="true">
									<path d="m14 6-6 6 6 6" />
								</svg>
							</button>
							<button
								type="button"
								class="match__nav"
								disabled={activeIndex >= candidates.length - 1}
								aria-label="Next source"
								onclick={() => setActive(candidates[activeIndex + 1].bookId)}
							>
								<svg class="match__icon" viewBox="0 0 24 24" aria-hidden="true">
									<path d="m10 6 6 6-6 6" />
								</svg>
							</button>
							<Button
								variant="ghost"
								id="focused-remove"
								onclick={() => removeCandidate(c)}
							>
								Remove
							</Button>
						</div>
					</div>

					{#if c.removing}
						<!-- Explicit removal confirmation -->
						<div class="match__removal" role="alert" aria-busy={c.removingConfirming}>
							{removeConfirmCopy(c)}
							<span class="match__removal-actions">
								<Button
									variant="danger"
									loading={c.removingConfirming}
									onclick={() => confirmRemoveCandidate(c)}
								>
									{c.removingConfirming ? 'Removing…' : 'Yes, remove'}
								</Button>
								<Button
									variant="ghost"
									disabled={c.removingConfirming}
									onclick={() => cancelRemoveCandidate(c)}
								>
									Cancel
								</Button>
							</span>
						</div>
					{/if}

					<div class="match__chosen">
						{#if c.loading && !c.details.book && !c.error}
							<div class="match__record match__record--loading">
								<Skeleton variant="rect" width="110px" height="110px" />
								<div class="batch__skel-block">
									<Skeleton height="1.6rem" width="13rem" />
									<Skeleton height="0.9rem" width="9rem" />
									<Skeleton height="0.9rem" width="11rem" />
								</div>
							</div>
						{:else if c.error}
							<!-- Per-source failure — human copy, raw detail expandable -->
							{@const cleaned = c.error}
							<div class="match__failed">
								<p class="match__failed-title">Catalog lookup failed</p>
								<p class="match__failed-msg">{cleaned.message}</p>
								<div class="match__failed-actions">
									<Button variant="secondary" onclick={() => retryCandidate(c)}>
										Retry
									</Button>
									{#if cleaned.rateLimited}
										<a
											href="/settings"
											class="text-sm font-medium text-[var(--text-secondary)] underline underline-offset-2 hover:text-[var(--text)]"
										>
											Settings → API key
										</a>
									{/if}
								</div>
								<ErrorDetail raw={cleaned.raw} needsSettings={cleaned.needsSettings} class="mt-3" />
							</div>
						{:else if c.details.book}
							<!-- Selected record, in full -->
							{@const book = c.details.book}
							{@const release = c.details.release}
							{@const description = plainText(book.description)}
							{@const catalogCover = selectedCover(c)}
							<div class="match__sechead">
								<h2>Selected catalog record</h2>
								{@render matchChip(
									c.manualPick ? 'manual' : 'auto',
									c.manualPick ? 'Matched manually · not saved' : 'Auto-matched · not saved'
								)}
							</div>
							<div class="match__record">
								<figure class="match__art">
									{#if catalogCover}
										<img
											class="match__cover"
											src={catalogCover}
											alt="Catalog cover for {selectedTitle(c)}"
											loading="lazy"
											decoding="async"
										/>
									{:else}
										<div class="match__cover match__cover--empty" aria-hidden="true">
											No catalog cover
										</div>
									{/if}
									<figcaption>Catalog cover</figcaption>
								</figure>
								<div class="match__record-copy min-w-0">
									<h3>
										{selectedTitle(c)}
										<AbdTag href={`https://audiobookdb.org/books/${book.id}`} />
									</h3>
									{#if book.title && book.title !== selectedTitle(c)}
										<p class="match__book-title">{book.title}</p>
									{/if}
									{#if book.disambiguation}
										<p class="match__disambig">{book.disambiguation}</p>
									{/if}
									<p class="match__author">{selectedAuthors(c)}</p>
									<dl class="match__meta">
										{#if selectedNarrators(c)}
											<div>
												<dt>Narrated by</dt>
												<dd>{selectedNarrators(c)}</dd>
											</div>
										{/if}
										{#if runtimeLabel(releaseRuntimeMs(release))}
											<div>
												<dt>Runtime</dt>
												<dd>{runtimeLabel(releaseRuntimeMs(release))}</dd>
											</div>
										{/if}
										{#if release?.publisher?.name}
											<div>
												<dt>Publisher</dt>
												<dd>{release?.publisher?.name}</dd>
											</div>
										{/if}
										{#if release?.releaseDate || book.originallyPublishedAt}
											<div>
												<dt>Released</dt>
												<dd>{dateLabel(release?.releaseDate || book.originallyPublishedAt)}</dd>
											</div>
										{/if}
										{#if release?.language?.title}
											<div>
												<dt>Language</dt>
												<dd>{release?.language?.title}</dd>
											</div>
										{/if}
									</dl>
									{#if description}
										<div class="match__description">
											<p
												use:measureDescriptionOverflow={{
													candidate: c,
													description,
													open: c.descriptionOpen
												}}
												class="match__desc-text"
												class:match__desc-text--open={c.descriptionOpen}
											>
												{description}
											</p>
											{#if c.descriptionOverflow || c.descriptionOpen}
												<button
													type="button"
													class="match__desc-toggle"
													onclick={() => (c.descriptionOpen = !c.descriptionOpen)}
												>
													{c.descriptionOpen ? 'Show less' : 'Show full description'}
												</button>
											{/if}
										</div>
									{/if}
								</div>
							</div>

							{#if (book.releases?.length ?? 0) > 1}
								<div class="match__edition">
									<label for={`match-edition-${c.bookId}`}>Edition / release</label>
									<select
										id={`match-edition-${c.bookId}`}
										value={c.selectedReleaseId ?? ''}
										disabled={c.releaseLoading}
										onchange={(e) => chooseRelease(c, e.currentTarget.value || null)}
									>
										{#if !c.selectedReleaseId}
											<option value="">Choose an edition</option>
										{/if}
										{#each book.releases as option (option.id)}
											<option value={option.id}>{editionOptionLabel(option, book.title)}</option>
										{/each}
									</select>
									<p class="match__edition-note">
										{book.releases.length} releases · edition details load for this
										book only
									</p>
									{#if c.releaseLoading}
										<span class="match__edition-state" role="status">
											Loading edition details…
										</span>
									{/if}
									{#if c.releaseError}
										<div class="match__edition-error" role="alert">
											<span>{c.releaseError.message}</span>
											<button
												type="button"
												class="match__search-retry"
												onclick={() => chooseRelease(c, c.selectedReleaseId)}
											>
												Retry edition
											</button>
										</div>
									{/if}
								</div>
							{/if}
						{:else}
							<!-- No confident match -->
							<div class="match__nomatch">
								<p class="match__nomatch-title">No match selected</p>
								<p class="match__nomatch-msg">
									{#if c.searchResults.length === 0 && !c.loading}
										The catalog search found no records for “{stripExtension(c.title)}”.
									{:else}
										No catalog record matched confidently.
									{/if}
									Search the catalog below to choose one.
								</p>
							</div>
						{/if}

						<!-- Change selection: picker + inline catalog search -->
						<div class="match__change">
							<h2>Change selection</h2>

							{#if c.searchResults.length > 0}
								<div class="match-picker__field">
									<button
										type="button"
										role="combobox"
										id={`match-picker-trigger-${c.bookId}`}
										class="match-picker__trigger"
										class:match-picker__trigger--placeholder={!c.selectedBookId}
										disabled={c.loading}
										aria-expanded={pickerCandidateId === c.bookId}
										aria-haspopup="listbox"
										aria-controls={pickerCandidateId === c.bookId
											? `match-list-${c.bookId}`
											: undefined}
										aria-activedescendant={pickerCandidateId === c.bookId && pickerIndex >= 0
											? `match-opt-${c.bookId}-${pickerIndex}`
											: undefined}
										aria-label="Select catalog record for {stripExtension(c.title)}"
										onclick={() => togglePicker(c)}
										onkeydown={(e) => pickerKeydown(e, c)}
									>
										<span class="truncate" title={pickedTitle(c)}>
											{c.selectedBookId ? pickedTitle(c) || '—' : 'No match selected'}
										</span>
										<svg class="match__icon" viewBox="0 0 24 24" aria-hidden="true">
											<path d="m6 9 6 6 6-6" />
										</svg>
									</button>

									{#if pickerCandidateId === c.bookId}
										<!-- In-flow listbox: no absolute positioning, no clipping -->
										<ul
											class="match-picker"
											id="match-list-{c.bookId}"
											role="listbox"
											aria-label="Catalog records for {stripExtension(c.title)}"
										>
											{#each c.searchResults as result, i (result.id)}
												{@const meta = optionMeta(result.id, result)}
												{@const rCover = pickCover(result.images) || hitCoverUrl(result.image, 'small')}
												{@const rYear = yearOf(result.originallyPublishedAt)}
												<li role="presentation">
													<button
														type="button"
														role="option"
														tabindex="-1"
														aria-selected={result.id === c.selectedBookId}
														id="match-opt-{c.bookId}-{i}"
														class="match-picker__row"
														class:match-picker__row--active={i === pickerIndex}
														class:match-picker__row--selected={result.id === c.selectedBookId}
														onclick={() => pickResult(c, result)}
														onmousemove={() => movePickerIndex(i)}
													>
														{#if rCover}
															<img
																src={rCover}
																alt=""
																loading="lazy"
																class="match-picker__cover"
															/>
														{:else}
															<div
																class="match-picker__cover match-picker__cover--empty"
																aria-hidden="true"
															>
																—
															</div>
														{/if}
														<span class="match-picker__body">
															<span class="match-picker__row-title">
																{result.title}
																{#if result.id === c.selectedBookId}
																	<span class="match-picker__mark">selected</span>
																{/if}
															</span>
															<span class="match-picker__row-sub">
																{meta.authors || 'Unknown author'}
																{#if meta.state === 'loading'}
																	· loading details…
																{:else if meta.state === 'error'}
																	· details unavailable
																{:else if meta.narrators}
																	· {meta.narrators}
																{:else}
																	· no narrator listed
																{/if}
																{#if meta.state === 'ready'}
																	{#if meta.runtime}
																		· {meta.runtime}
																	{:else}
																		· runtime unknown
																	{/if}
																{/if}
															</span>
															{#if meta.edition}
																<span class="match-picker__row-edition">
																	{meta.edition}
																</span>
															{/if}
														</span>
														{#if rYear}
															<span class="match-picker__year">{rYear}</span>
														{/if}
													</button>
												</li>
											{/each}
										</ul>
									{/if}
								</div>
							{/if}

							<!-- Inline catalog search -->
							<div class="match__search">
								<label for="catalog-search">Search catalog</label>
								<div class="match__search-wrap">
									<svg class="match__search-icon" viewBox="0 0 24 24" aria-hidden="true">
										<circle cx="10.5" cy="10.5" r="6.5" />
										<path d="m16 16 4 4" />
									</svg>
									<input
										id="catalog-search"
										type="text"
										value={searchQuery}
										oninput={(e) => (searchQuery = e.currentTarget.value)}
										onblur={(e) => (searchFormQuery = e.currentTarget.value)}
										onkeydown={(e) => {
											if (e.key === 'Enter') {
												searchFormQuery = e.currentTarget.value;
												runSearch();
											}
										}}
										aria-label="Search catalog"
									/>
								</div>
								<Button variant="secondary" loading={searching} onclick={runSearch}>
									Search
								</Button>
							</div>
							{#if searching}
								<span class="sr-only" aria-live="polite">Searching catalog…</span>
							{/if}

							{#if searching}
								<div class="match__search-skeleton">
									{#each Array(3) as _}
										<Skeleton height="2.75rem" />
									{/each}
								</div>
							{:else if searched && !searchError && searchResults.length > 0}
								<p class="match__search-state">
									{searchResults.length}
									{searchResults.length === 1 ? 'result' : 'results'} for “{searchedQuery}”
								</p>
								<ul class="match__hits">
									{#each searchResults as result (result.id)}
										{@const meta = optionMeta(result.id, result)}
										{@const rCover = pickCover(result.images) || hitCoverUrl(result.image, 'small')}
										{@const rYear = yearOf(result.originallyPublishedAt)}
										<li>
											<button
												type="button"
												class="match__hit"
												class:match__hit--selected={
													c.details.book?.id === result.id || c.selectedBookId === result.id
												}
												onclick={() => pickSearchResult(result)}
											>
												{#if rCover}
													<img
														src={rCover}
														alt=""
														loading="lazy"
														class="match-picker__cover"
													/>
												{:else}
													<div
														class="match-picker__cover match-picker__cover--empty"
														aria-hidden="true"
													>
														—
													</div>
												{/if}
												<span class="match-picker__body">
													<span class="match-picker__row-title">
														{result.title}
														{#if c.details.book?.id === result.id || c.selectedBookId === result.id}
															<span class="match-picker__mark">selected</span>
														{/if}
													</span>
													<span class="match-picker__row-sub">
														{meta.authors || 'Unknown author'}
														{#if meta.state === 'loading'}
															· loading details…
														{:else if meta.state === 'error'}
															· details unavailable
														{:else if meta.narrators}
															· {meta.narrators}
														{:else}
															· no narrator listed
														{/if}
														{#if meta.state === 'ready'}
															{#if meta.runtime}
																· {meta.runtime}
															{:else}
																· runtime unknown
															{/if}
														{/if}
													</span>
													{#if meta.edition}
														<span class="match-picker__row-edition">{meta.edition}</span>
													{/if}
												</span>
												{#if rYear}
													<span class="match-picker__year">{rYear}</span>
												{/if}
												<span class="match__hit-btn">Select</span>
											</button>
										</li>
									{/each}
								</ul>
								{#if searchResults.some((result) => hydration[result.id]?.error)}
									<div class="match__hydration">
										Some option details could not be loaded. Selecting a record still
										fetches it directly.
										{#each searchResults as result (result.id)}
											{#if hydration[result.id]?.error}
												<button
													type="button"
													class="match__search-retry"
													onclick={() => retryHydration(result.id)}
												>
													Retry {result.title}
												</button>
											{/if}
										{/each}
									</div>
								{/if}
							{:else if searched && searchError}
								<!-- Search failed — distinguish from zero results -->
								{@const cleaned = searchError}
								<div class="match__search-error">
									<p class="match__failed-title">Search failed</p>
									<p class="match__failed-msg">{cleaned.message}</p>
									<ErrorDetail raw={cleaned.raw} needsSettings={cleaned.needsSettings} />
									<button type="button" class="match__search-retry" onclick={runSearch}>
										Retry
									</button>
								</div>
							{:else if searched && searchResults.length === 0}
								<p class="match__search-state">
									No matches found for “{searchedQuery}”. Try different keywords.
								</p>
							{/if}
						</div>
					</div>
				</section>
			{:else}
				<div class="match__inspector">
					<p class="match__inspector-empty">
						Select a source to inspect its catalog record.
					</p>
				</div>
			{/if}
		</div>
	{/if}

	<Modal
		bind:open={removeDialogOpen}
		title="Remove sources"
		closeDisabled={removingMany}
		beforeClose={() => {
			if (removingMany) return false;
			cancelRemoveDialog();
			return true;
		}}
	>
		<p class="batch-dialog__lead">
			Check the sources to remove. Their book records are deleted from Bragi Books; files on
			disk are not touched.
		</p>
		<div class="batch-dialog__pick-head">
			<label class="batch__all">
				<input
					type="checkbox"
					checked={allRemovePicked}
					use:setIndeterminate={someRemovePicked && !allRemovePicked}
					disabled={removingMany || candidates.length === 0}
					onchange={toggleAllRemovePicks}
				/>
				<span>Select all sources</span>
			</label>
			<span class="batch-dialog__pick-count" aria-live="polite">
				{removeCount} of {candidates.length} selected
			</span>
		</div>
		<ul class="batch-dialog__list">
			{#each candidates as target (target.bookId)}
				<li
					class="batch-dialog__item"
					class:batch-dialog__item--picked={removePick.has(target.bookId)}
				>
					<label class="batch-dialog__pick">
						<input
							id={`remove-pick-${target.bookId}`}
							type="checkbox"
							checked={removePick.has(target.bookId)}
							disabled={removingMany}
							onchange={() => toggleRemovePick(target)}
							aria-label={`Remove ${sourceBase(target)}`}
						/>
						{#if target.coverUrl}
							<img
								class="batch-dialog__thumb"
								src={bookCoverUrl(target.coverUrl, 'small')}
								alt=""
								loading="lazy"
								decoding="async"
							/>
						{:else}
							<span
								class="batch-dialog__thumb batch-dialog__thumb--empty"
								aria-hidden="true"
							></span>
						{/if}
						<span class="batch-dialog__pick-body">
							<span class="batch-dialog__name" title={target.srcPath}
								>{sourceBase(target)}</span
							>
							<span class="batch-dialog__record">
								{target.details.book
									? selectedTitle(target)
									: matchState(target).text}
							</span>
						</span>
					</label>
				</li>
			{/each}
		</ul>
		{#snippet footer()}
			<div class="batch-dialog__foot">
				<div class="batch-dialog__actions">
					<Button
						variant="ghost"
						id="batch-remove-cancel"
						disabled={removingMany}
						onclick={cancelRemoveDialog}
					>
						Cancel
					</Button>
					<Button
						variant="danger"
						loading={removingMany}
						disabled={removeCount === 0}
						onclick={confirmRemoveMany}
					>
						{removingMany
							? 'Removing…'
							: `Remove ${removeCount} ${
									removeCount === 1 ? 'record' : 'records'
								}`}
					</Button>
				</div>
			</div>
		{/snippet}
	</Modal>
</div>

<style>
	/* ── Semantic aliases for this surface ───────────────────── */
	.match {
		--m-panel: var(--bg-panel);
		--m-surface: var(--surface);
		--m-surface-hover: var(--surface-hover);
		--m-border: var(--border);
		--m-border-soft: var(--border-subtle);
		--m-border-strong: var(--border-strong);
		--m-ink: var(--text);
		--m-ink-2: var(--text-secondary);
		--m-muted: var(--text-muted);
		--m-selected: var(--workspace-selected);
		--m-r-sm: var(--radius-sm);
		--m-r-md: var(--radius-md);
	}

	.match ::selection {
		background: var(--workspace-selected);
		color: var(--workspace-ink);
	}

	.match button:focus-visible,
	.match input:focus-visible,
	.match select:focus-visible,
	.match summary:focus-visible,
	.match a:focus-visible {
		outline: 2px solid var(--focus-ring);
		outline-offset: 2px;
	}

	/* ── Sticky head: title, mode, counted approval ──────────── */
	.match__head {
		position: sticky;
		top: var(--nav-height);
		z-index: 30;
		margin: 0 0 20px;
		padding: 12px 0 14px;
		background: var(--bg);
		border-bottom: 1px solid var(--m-border);
	}

	.match__head-in {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 20px;
	}

	.match__intro {
		min-width: 0;
	}

	.match__title {
		font-size: 28px;
		line-height: 1.2;
		letter-spacing: -0.025em;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0;
	}

	.match__head-actions {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-shrink: 0;
	}

	.match__modes {
		display: inline-flex;
		padding: 2px;
		gap: 2px;
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-sm);
	}

	.match__mode {
		min-height: 36px;
		padding: 6px 12px;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--m-ink-2);
		font-size: 13px;
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.match__mode:hover {
		background: var(--m-surface-hover);
		color: var(--m-ink);
	}

	.match__mode:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.match__mode:disabled:hover {
		background: transparent;
		color: var(--m-ink-2);
	}

	.match__mode[aria-pressed='true'] {
		background: var(--m-selected);
		color: var(--m-ink);
		box-shadow: inset 0 0 0 1px var(--m-border);
	}

	.match__head :global(.match__save) {
		flex-shrink: 0;
		min-height: 44px;
	}

	.match__commit-row {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-shrink: 0;
	}

	.match__head :global(.match__remove) {
		min-height: 44px;
		flex-shrink: 0;
	}

	.match__alert {
		margin-bottom: 16px;
	}

	/* ── Batch review ────────────────────────────────────────── */
	.batch {
		background: var(--m-panel);
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-md);
	}

	.batch__bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px 20px;
		flex-wrap: wrap;
		padding: 14px 18px;
		background: var(--m-surface);
		border-bottom: 1px solid var(--m-border);
		border-radius: var(--m-r-md) var(--m-r-md) 0 0;
	}



	.batch__alert {
		padding: 12px 18px;
		border-bottom: 1px solid var(--m-border);
		scroll-margin-top: calc(var(--nav-height) + var(--head-h, 7rem) + 5.5rem);
	}

	.batch__alert-lead {
		margin: 0 0 4px;
		font-weight: 600;
	}

	.batch__all {
		display: inline-flex;
		align-items: center;
		gap: 9px;
		min-height: 44px;
		padding: 0 4px;
		font-size: 14px;
		font-weight: 600;
		color: var(--m-ink);
		cursor: pointer;
	}

	.batch__all input {
		width: 18px;
		height: 18px;
		accent-color: var(--focus-ring);
		cursor: pointer;
	}

	.batch__all input:disabled {
		cursor: not-allowed;
	}

	.batch__count {
		font-size: 13px;
		color: var(--m-muted);
		font-variant-numeric: tabular-nums;
	}

	.batch__foot {
		margin: 0;
		padding: 12px 18px;
		border-top: 1px solid var(--m-border-soft);
		font-size: 12.5px;
		line-height: 1.45;
		color: var(--m-muted);
		max-width: 90ch;
	}

	.batch__list {
		list-style: none;
		margin: 0;
		padding: 0;
	}

	/* Programmatic focus (removal confirm/cancel) must not land underneath
	 * the sticky page head. */
	.batch__bar,
	.batch__row {
		scroll-margin-top: calc(var(--nav-height) + 7rem);
	}

	.batch__row {
		display: grid;
		grid-template-columns: 20px 44px minmax(0, 1fr) minmax(0, 1.45fr) auto;
		grid-template-areas: 'check thumb names record actions';
		align-items: center;
		gap: 14px;
		padding: 14px 18px;
		border-bottom: 1px solid var(--m-border-soft);
	}

	.batch__row:last-child {
		border-bottom: 0;
	}

	.batch__row:hover {
		background: var(--m-surface);
	}

	.batch__row--excluded {
		background: var(--m-surface);
	}

	.batch__row--excluded .batch__names,
	.batch__row--excluded .batch__record {
		color: var(--m-muted);
	}

	.batch__row--excluded .batch__record-title {
		color: var(--m-ink-2);
	}

	.batch__check {
		grid-area: check;
		position: relative;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-height: 44px;
		cursor: pointer;
	}

	/* The grid cell is 20px; widen only the hit area to the 44px floor. */
	.batch__check::before {
		content: '';
		position: absolute;
		inset: 0 -12px;
	}

	.batch__check input {
		width: 18px;
		height: 18px;
		accent-color: var(--focus-ring);
		cursor: pointer;
	}

	.batch__check input:disabled {
		cursor: not-allowed;
	}

	.batch__thumb {
		grid-area: thumb;
		display: block;
		width: 44px;
		height: 44px;
		object-fit: cover;
		border: 1px solid var(--m-border);
		border-radius: 4px;
		background: var(--elevated);
		flex-shrink: 0;
	}

	.batch__thumb--empty {
		background: linear-gradient(135deg, var(--elevated) 50%, var(--m-border-soft) 50%);
	}

	.batch__names {
		grid-area: names;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1.15fr);
		align-items: center;
		gap: 8px;
		min-width: 0;
	}

	.batch__name {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.batch__name-label {
		font-size: 11.5px;
		font-weight: 600;
		color: var(--m-muted);
	}

	.batch__name-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}

	.batch__name-note {
		font-size: 11px;
		font-weight: 500;
		color: var(--m-muted);
		white-space: nowrap;
	}

	.batch__name-value {
		font-size: 14px;
		font-weight: 600;
		line-height: 1.35;
		color: var(--m-ink);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.batch__name-value--code {
		font-size: 12.5px;
		font-weight: 500;
		white-space: normal;
		overflow-wrap: anywhere;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}

	.batch__name-value--empty {
		color: var(--m-muted);
		font-weight: 500;
	}

	/* Source identity wraps in both batch and removal review. */
	.batch__name-value--source {
		display: block;
		white-space: normal;
		overflow-wrap: anywhere;
	}

	.batch__name-source-text {
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.batch__name-art {
		display: none;
		width: 24px;
		height: 24px;
		flex-shrink: 0;
		object-fit: cover;
		border: 1px solid var(--m-border);
		border-radius: 4px;
		background: var(--elevated);
	}

	.batch__name-link {
		font-size: 13px;
		font-weight: 600;
		color: var(--m-ink-2);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.batch__arrow {
		width: 15px;
		height: 15px;
		fill: none;
		stroke: var(--m-muted);
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.batch__record {
		grid-area: record;
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}

	.batch__record-body {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.batch__record-art {
		display: block;
		width: 44px;
		height: 44px;
		flex-shrink: 0;
		overflow: hidden;
		background: var(--elevated);
		border: 1px solid var(--m-border);
		border-radius: 4px;
	}

	.batch__record-art img {
		display: block;
		width: 100%;
		height: 100%;
		object-fit: cover;
	}

	.batch__record-art--empty {
		background: linear-gradient(135deg, var(--elevated) 50%, var(--m-border-soft) 50%);
	}

	.batch__record-title {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
		font-size: 14px;
		font-weight: 650;
		line-height: 1.35;
		color: var(--m-ink);
	}

	.batch__record-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.batch__record-title :global(.abd-tag) {
		flex-shrink: 0;
	}

	.batch__record-meta-line {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		min-width: 0;
	}

	.batch__record-meta-line .match__label {
		flex-shrink: 0;
	}

	.batch__record-meta {
		flex: 1 1 auto;
		min-width: 0;
		font-size: 12.5px;
		line-height: 1.4;
		color: var(--m-muted);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.batch__record-edition {
		font-size: 12.5px;
		line-height: 1.4;
		color: var(--m-ink-2);
		font-style: italic;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.batch__actions {
		grid-area: actions;
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 5px;
		flex-shrink: 0;
	}

	.batch__actions :global(.batch__review) {
		min-height: 36px;
		padding: 0 12px;
		font-size: 13px;
	}

	.batch__btns {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.batch__actions :global(.batch__remove) {
		min-height: 36px;
		padding: 0 12px;
		font-size: 13px;
	}

	.batch__removal {
		grid-column: 1 / -1;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
		padding: 10px 18px;
		background: var(--state-red-bg);
		font-size: 13.5px;
		font-weight: 600;
		color: var(--m-ink);
	}

	.batch__removal span[tabindex='-1']:focus {
		outline: 0;
	}

	.batch__removal-actions {
		display: flex;
		gap: 8px;
	}

	.batch__removal :global(.btn) {
		padding: 0 10px;
		font-size: 13px;
	}

	.batch__row--skeleton {
		pointer-events: none;
	}

	.batch__skel-check {
		grid-area: check;
		display: flex;
		justify-content: center;
	}

	.batch__skel-thumb {
		grid-area: thumb;
	}

	.batch__skel-names {
		grid-area: names;
	}

	.batch__skel-record {
		grid-area: record;
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.batch__skel-action {
		grid-area: actions;
	}

	.match__skel-thumb {
		display: flex;
		flex-shrink: 0;
	}

	.batch__skel-block {
		display: flex;
		flex-direction: column;
		gap: 6px;
		min-width: 0;
	}

	/* ── Focused review grid ─────────────────────────────────── */
	.match__grid {
		display: grid;
		grid-template-columns: 290px minmax(0, 1fr);
		gap: 20px;
		align-items: start;
	}

	.match__index {
		background: var(--m-panel);
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-md);
		display: flex;
		flex-direction: column;
		max-height: calc(100vh - var(--nav-height, 4.25rem) - 8rem);
		min-height: 320px;
		overflow: hidden;
	}

	.match__index-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 14px 16px 12px;
		border-bottom: 1px solid var(--m-border-soft);
	}

	.match__index-head h2 {
		font-size: 15px;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0;
	}

	.match__index-head span {
		font-size: 12.5px;
		color: var(--m-muted);
		font-variant-numeric: tabular-nums;
	}

	.match__index-list {
		flex: 1;
		overflow-y: auto;
		overscroll-behavior: contain;
		scrollbar-color: var(--m-border) transparent;
		scrollbar-width: thin;
	}

	.match__index-list::-webkit-scrollbar {
		width: 10px;
	}

	.match__index-list::-webkit-scrollbar-track {
		background: transparent;
	}

	.match__index-list::-webkit-scrollbar-thumb {
		background: var(--m-border);
		border-radius: 5px;
		border: 2px solid var(--m-panel);
	}

	.match__index-list::-webkit-scrollbar-thumb:hover {
		background: var(--m-border-strong);
	}

	.match__src {
		display: flex;
		width: 100%;
		align-items: flex-start;
		gap: 11px;
		text-align: left;
		padding: 12px 14px;
		background: transparent;
		border: 0;
		border-bottom: 1px solid var(--m-border-soft);
		border-radius: 0;
		cursor: pointer;
		min-height: 64px;
	}

	.match__src:hover {
		background: var(--m-surface-hover);
	}

	.match__src:active {
		background: var(--surface-pressed);
	}

	.match__src--active,
	.match__src--active:hover {
		background: var(--m-selected);
	}

	.match__thumb {
		display: block;
		width: 40px;
		height: 40px;
		object-fit: cover;
		border-radius: 4px;
		border: 1px solid var(--m-border);
		flex-shrink: 0;
		background: var(--elevated);
	}

	.match__thumb--empty {
		background: linear-gradient(135deg, var(--elevated) 50%, var(--m-border-soft) 50%);
	}

	.match__src-body {
		display: flex;
		flex-direction: column;
		gap: 3px;
		padding-top: 1px;
	}

	.match__src-body strong {
		font-size: 13.5px;
		line-height: 1.3;
		color: var(--m-ink);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.match__src-status {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 11.5px;
		line-height: 1.35;
		color: var(--m-muted);
	}

	.match__src-status[data-kind='auto'],
	.match__src-status[data-kind='manual'] {
		color: var(--m-ink-2);
	}

	.match__src-status[data-kind='error'] {
		color: var(--state-red);
	}

	.match__src-status[data-kind='low'] {
		color: var(--state-amber);
	}

	.match__dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.match__dot--gold {
		background: var(--accent);
		box-shadow: 0 0 0 1px var(--border-strong);
	}

	.match__dot--green {
		background: var(--state-green);
	}

	.match__dot--amber {
		background: var(--state-amber);
	}

	.match__dot--red {
		background: var(--state-red);
	}

	.match__dot--bare {
		background: var(--m-border-strong);
	}

	.match__dot--pulse {
		background: var(--m-border-strong);
		animation: match-pulse 1.1s ease-in-out infinite;
	}

	@keyframes match-pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.35;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.match__dot--pulse {
			animation: none;
		}
	}

	.match__src--skeleton {
		border-bottom: 1px solid var(--m-border-soft);
		cursor: default;
	}

	/* ── Inspector ───────────────────────────────────────────── */
	.match__inspector {
		background: var(--m-panel);
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-md);
		/* No overflow clipping: the picker is in flow, so nothing is cut off. */
	}

	.match__inspector-empty {
		padding: 32px 24px;
		font-size: 14px;
		color: var(--m-muted);
	}

	.match__source {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		align-items: flex-start;
		gap: 16px;
		padding: 16px 22px;
		background: var(--m-surface);
		border-bottom: 1px solid var(--m-border);
		border-radius: var(--m-r-md) var(--m-r-md) 0 0;
	}

	.match__source-id {
		display: flex;
		gap: 14px;
		min-width: 0;
	}

	.match__source-art {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 4px;
		margin: 0;
		flex-shrink: 0;
	}

	.match__source-art figcaption {
		font-size: 10.5px;
		line-height: 1.2;
		color: var(--m-muted);
	}

	.match__source-cover {
		display: block;
		width: 56px;
		height: 56px;
		object-fit: cover;
		border: 1px solid var(--m-border);
		border-radius: 4px;
		background: var(--elevated);
	}

	.match__source-cover--empty {
		background: linear-gradient(135deg, var(--elevated) 50%, var(--m-border-soft) 50%);
	}

	.match__source-copy {
		min-width: 0;
	}

	.match__source-copy h2 {
		font-size: 18px;
		line-height: 1.3;
		letter-spacing: -0.01em;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0 0 4px;
		overflow-wrap: anywhere;
	}

	.match__proposed {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 6px;
		margin: 0 0 6px;
		font-size: 12px;
		color: var(--m-muted);
	}

	.match__proposed code {
		font-size: 12px;
		color: var(--m-ink-2);
		overflow-wrap: anywhere;
	}

	.match__fullpath {
		margin-top: 5px;
		font-size: 12px;
		color: var(--m-muted);
	}

	.match__fullpath summary {
		cursor: pointer;
		width: max-content;
		max-width: 100%;
		font-weight: 500;
	}

	.match__fullpath summary:hover {
		color: var(--m-ink-2);
	}

	.match__fullpath code {
		display: block;
		margin-top: 5px;
		max-width: min(64ch, 100%);
		overflow-wrap: anywhere;
		font-size: 11.5px;
		color: var(--m-ink-2);
		background: var(--m-panel);
		border: 1px solid var(--m-border-soft);
		border-radius: var(--m-r-sm);
		padding: 7px 9px;
	}

	.match__position {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		color: var(--m-muted);
		font-size: 12.5px;
		flex-shrink: 0;
		margin-top: 2px;
	}

	.match__position span {
		font-variant-numeric: tabular-nums;
	}

	.match__include {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: 12.5px;
		font-weight: 500;
		color: var(--m-ink-2);
		cursor: pointer;
		user-select: none;
	}

	.match__include--disabled {
		color: var(--m-muted);
		cursor: default;
	}

	.match__nav {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 34px;
		height: 34px;
		padding: 0;
		border: 1px solid var(--m-border-strong);
		border-radius: var(--m-r-sm);
		background: var(--m-panel);
		color: var(--m-ink-2);
		cursor: pointer;
	}

	.match__nav:hover:not(:disabled) {
		background: var(--m-surface-hover);
	}

	.match__nav:disabled {
		opacity: 0.45;
		cursor: default;
	}

	.match__position :global(.btn) {
		min-height: 34px;
		padding: 0 10px;
		font-size: 13px;
	}

	.match__icon {
		width: 17px;
		height: 17px;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.match__removal {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
		padding: 12px 22px;
		background: var(--state-red-bg);
		border-bottom: 1px solid var(--m-border);
		font-size: 13.5px;
		font-weight: 600;
		color: var(--m-ink);
	}

	.match__removal-actions {
		display: flex;
		gap: 8px;
	}

	.match__chosen {
		display: flex;
		flex-direction: column;
	}

	.match__sechead {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
		padding: 20px 22px 0;
	}

	.match__sechead h2 {
		font-size: 15px;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0;
	}

	.match__label {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: 12px;
		font-weight: 600;
		border-radius: 4px;
		padding: 3px 8px;
	}

	.match__label svg {
		width: 14px;
		height: 14px;
		fill: none;
		stroke: currentColor;
		stroke-width: 2.2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.match__label--auto {
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		color: var(--m-ink-2);
	}

	.match__label--manual {
		background: var(--state-green-bg);
		border: 1px solid var(--state-green-border);
		color: var(--state-green);
	}

	.match__label--none {
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		color: var(--m-muted);
	}

	.match__label--excluded {
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		color: var(--m-ink-2);
	}

	.match__label--low {
		background: var(--state-amber-bg);
		border: 1px solid var(--state-amber-border);
		color: var(--state-amber);
	}

	.match__label--error {
		background: var(--state-red-bg);
		border: 1px solid var(--state-red-border);
		color: var(--state-red);
	}

	.match__label--loading {
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		color: var(--m-muted);
	}

	.match__label-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--m-border-strong);
		animation: match-pulse 1.1s ease-in-out infinite;
	}

	@media (prefers-reduced-motion: reduce) {
		.match__label-dot {
			animation: none;
		}
	}

	.match__record {
		display: flex;
		gap: 22px;
		padding: 16px 22px 22px;
	}

	.match__record--loading {
		padding-top: 22px;
	}

	.match__art {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 5px;
		margin: 0;
		flex-shrink: 0;
	}

	.match__art figcaption {
		font-size: 10.5px;
		line-height: 1.2;
		color: var(--m-muted);
	}

	.match__cover {
		display: block;
		width: 132px;
		height: 132px;
		object-fit: cover;
		border-radius: 4px;
		border: 1px solid var(--m-border);
		flex-shrink: 0;
	}

	.match__cover--empty {
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--elevated);
		color: var(--m-muted);
		font-size: 12px;
		text-align: center;
		padding: 8px;
	}

	.match__record-copy {
		flex: 1;
		min-width: 0;
	}

	.match__record-copy h3 {
		display: flex;
		align-items: baseline;
		gap: 8px;
		flex-wrap: wrap;
		font-size: 22px;
		line-height: 1.22;
		letter-spacing: -0.02em;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0 0 5px;
		overflow-wrap: anywhere;
	}

	.match__book-title {
		margin: 0 0 4px;
		font-size: 13.5px;
		color: var(--m-muted);
	}

	.match__disambig {
		margin: 0 0 6px;
		font-size: 13.5px;
		font-weight: 600;
		color: var(--m-ink-2);
	}

	.match__author {
		font-size: 15px;
		color: var(--m-ink-2);
		margin: 0 0 14px;
		overflow-wrap: anywhere;
	}

	.match__meta {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(135px, 1fr));
		gap: 10px 22px;
		margin: 0;
	}

	.match__meta div {
		min-width: 0;
	}

	.match__meta dt {
		font-size: 11.5px;
		color: var(--m-muted);
		margin-bottom: 2px;
	}

	.match__meta dd {
		margin: 0;
		font-size: 14px;
		font-weight: 600;
		color: var(--m-ink);
		overflow-wrap: anywhere;
	}

	.match__description {
		margin-top: 16px;
		max-width: 68ch;
	}

	.match__desc-text {
		display: -webkit-box;
		-webkit-line-clamp: 4;
		line-clamp: 4;
		-webkit-box-orient: vertical;
		overflow: hidden;
		margin: 0;
		font-size: 13.5px;
		line-height: 1.55;
		color: var(--m-ink-2);
	}

	.match__desc-text--open {
		display: block;
		-webkit-line-clamp: unset;
		line-clamp: unset;
		overflow: visible;
	}

	.match__desc-toggle {
		margin-top: 6px;
		padding: 0;
		border: 0;
		background: none;
		color: var(--m-ink-2);
		font-size: 12.5px;
		font-weight: 600;
		text-decoration: underline;
		text-underline-offset: 2px;
		cursor: pointer;
	}

	.match__edition {
		display: grid;
		grid-template-columns: minmax(0, 320px) minmax(0, 1fr);
		align-items: center;
		gap: 4px 14px;
		margin: 0 22px 20px;
		padding: 14px;
		background: var(--m-surface);
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-sm);
	}

	.match__edition label {
		font-size: 13px;
		font-weight: 600;
		color: var(--m-ink-2);
	}

	.match__edition select {
		min-height: 38px;
		width: 100%;
		padding: 6px 10px;
		background: var(--m-panel);
		border: 1px solid var(--m-border-strong);
		border-radius: var(--m-r-sm);
		color: var(--m-ink);
		font-size: 13.5px;
	}

	.match__edition select:disabled {
		opacity: 0.6;
		cursor: wait;
	}

	.match__edition-note {
		grid-column: 1 / -1;
		margin: 0;
		font-size: 12px;
		color: var(--m-muted);
	}

	.match__edition-state {
		grid-column: 1 / -1;
		font-size: 12px;
		color: var(--m-ink-2);
	}

	.match__edition-error {
		grid-column: 1 / -1;
		display: flex;
		align-items: baseline;
		gap: 10px;
		flex-wrap: wrap;
		font-size: 12.5px;
		color: var(--state-red);
	}

	/* ── Change selection: picker + inline search ────────────── */
	.match__change {
		margin: 0 22px 22px;
		padding: 18px 0 0;
		border-top: 1px solid var(--m-border);
	}

	.match__change h2 {
		font-size: 15px;
		font-weight: 700;
		color: var(--m-ink);
		margin: 0 0 12px;
	}

	.match-picker__field {
		display: flex;
		flex-direction: column;
	}

	.match-picker__trigger {
		display: flex;
		width: 100%;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		min-height: 42px;
		padding: 8px 12px;
		background: var(--m-panel);
		color: var(--m-ink);
		border: 1px solid var(--m-border-strong);
		border-radius: var(--m-r-sm);
		font-size: 14px;
		font-weight: 500;
		text-align: left;
		cursor: pointer;
	}

	.match-picker__trigger:hover:not(:disabled) {
		background: var(--m-surface-hover);
	}

	.match-picker__trigger--placeholder {
		color: var(--text-placeholder);
	}

	.match-picker__trigger:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	/* In-flow, bounded listbox. No absolute positioning and no clipping. */
	.match-picker {
		max-height: 320px;
		overflow-y: auto;
		overscroll-behavior: contain;
		scrollbar-color: var(--m-border) transparent;
		scrollbar-width: thin;
		margin: 6px 0 0;
		padding: 4px;
		list-style: none;
		background: var(--m-panel);
		border: 1px solid var(--m-border-strong);
		border-radius: var(--m-r-sm);
		box-shadow: 0 4px 14px rgb(41 41 35 / 0.1);
	}

	.match-picker::-webkit-scrollbar {
		width: 10px;
	}

	.match-picker::-webkit-scrollbar-thumb {
		background: var(--m-border);
		border-radius: 5px;
		border: 2px solid var(--m-panel);
	}

	.match-picker__row {
		display: flex;
		width: 100%;
		align-items: flex-start;
		gap: 10px;
		min-height: 54px;
		padding: 8px;
		border-radius: var(--m-r-sm);
		cursor: pointer;
		background: transparent;
		border: 0;
		color: inherit;
		text-align: left;
	}

	.match-picker__row--active {
		background: var(--m-surface-hover);
	}

	.match-picker__row--selected {
		background: var(--m-selected);
	}

	.match-picker__cover {
		width: 38px;
		height: 38px;
		flex-shrink: 0;
		border-radius: 4px;
		object-fit: cover;
		border: 1px solid var(--m-border);
		background: var(--elevated);
	}

	.match-picker__cover--empty {
		display: flex;
		align-items: center;
		justify-content: center;
		color: var(--m-muted);
		font-size: 0.75rem;
	}

	.match-picker__body {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
		flex: 1;
	}

	.match-picker__row-title {
		font-size: 13.5px;
		font-weight: 650;
		line-height: 1.3;
		color: var(--m-ink);
	}

	.match-picker__mark {
		display: inline-block;
		margin-left: 7px;
		font-size: 10px;
		font-weight: 700;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		color: var(--m-ink-2);
		vertical-align: 1px;
	}

	.match-picker__row-sub {
		font-size: 12px;
		line-height: 1.4;
		color: var(--m-muted);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.match-picker__row-edition {
		font-size: 11.5px;
		line-height: 1.4;
		color: var(--m-ink-2);
		font-style: italic;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.match-picker__year {
		font-size: 11.5px;
		color: var(--m-muted);
		flex-shrink: 0;
		font-variant-numeric: tabular-nums;
		padding-top: 1px;
	}

	.match__search {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 16px;
	}

	.match__search label {
		font-size: 14px;
		font-weight: 600;
		color: var(--m-ink-2);
		white-space: nowrap;
		margin-right: 4px;
	}

	.match__search-wrap {
		position: relative;
		flex: 1;
		min-width: 0;
	}

	.match__search-icon {
		position: absolute;
		top: 50%;
		left: 12px;
		transform: translateY(-50%);
		width: 16px;
		height: 16px;
		fill: none;
		stroke: var(--m-muted);
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
		pointer-events: none;
	}

	.match__search-wrap input {
		width: 100%;
		height: 42px;
		padding: 0 12px 0 38px;
		background: var(--m-panel);
		border: 1px solid var(--m-border-strong);
		border-radius: var(--m-r-sm);
		color: var(--m-ink);
		font-size: 14px;
		outline: none;
	}

	.match__search-wrap input::placeholder {
		color: var(--text-placeholder);
	}

	.match__search-wrap input:focus-visible {
		border-color: var(--focus-ring);
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--focus-ring) 24%, transparent);
	}

	.match__search-state {
		margin: 14px 0 0;
		font-size: 13px;
		color: var(--m-muted);
	}

	.match__search-skeleton {
		display: flex;
		flex-direction: column;
		gap: 8px;
		margin-top: 12px;
	}

	.match__hits {
		list-style: none;
		margin: 10px 0 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.match__hit {
		display: flex;
		width: 100%;
		align-items: flex-start;
		gap: 10px;
		min-height: 54px;
		padding: 8px;
		border-radius: var(--m-r-sm);
		cursor: pointer;
		background: transparent;
		border: 1px solid transparent;
		color: inherit;
		text-align: left;
	}

	.match__hit:hover {
		background: var(--m-surface-hover);
	}

	.match__hit--selected {
		border-color: var(--m-border-strong);
		background: var(--m-selected);
	}

	.match__hit-btn {
		font-size: 12px;
		font-weight: 700;
		color: var(--m-ink-2);
		flex-shrink: 0;
		padding-top: 2px;
	}

	.match__hit:hover .match__hit-btn {
		text-decoration: underline;
	}

	.match__hydration {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 6px 12px;
		margin-top: 10px;
		font-size: 12px;
		color: var(--m-muted);
	}

	.match__search-error {
		margin-top: 14px;
		border: 1px solid var(--state-red-border);
		background: var(--state-red-bg);
		border-radius: var(--m-r-sm);
		padding: 12px 14px;
	}

	.match__search-retry {
		margin-top: 8px;
		font-size: 12.5px;
		font-weight: 600;
		color: var(--m-ink-2);
		background: none;
		border: 0;
		padding: 0;
		cursor: pointer;
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	/* ── Failed / no-match states ────────────────────────────── */
	.match__failed {
		padding: 24px 22px;
	}

	.match__failed-title,
	.match__nomatch-title {
		font-size: 16px;
		font-weight: 700;
		color: var(--state-red);
		margin: 0;
	}

	.match__failed-msg {
		margin: 6px 0 0;
		font-size: 13.5px;
		color: var(--m-ink-2);
	}

	.match__failed-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px 16px;
		margin-top: 12px;
	}

	.match__nomatch {
		padding: 26px 22px;
	}

	.match__nomatch-title {
		color: var(--m-ink-2);
	}

	.match__nomatch-msg {
		margin: 6px 0 0;
		font-size: 13.5px;
		color: var(--m-muted);
		max-width: 52ch;
	}

	/* ── Batch removal selection ─────────────────────────────── */
	.batch-dialog__lead {
		margin: 0 0 12px;
		font-size: 13.5px;
		line-height: 1.5;
		color: var(--m-ink-2);
		max-width: 58ch;
	}

	.batch-dialog__pick-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px 12px;
		flex-wrap: wrap;
		margin: 0 0 8px;
	}

	.batch-dialog__pick-count {
		font-size: 12.5px;
		color: var(--m-muted);
		font-variant-numeric: tabular-nums;
	}

	.batch-dialog__list {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--m-border);
		border-radius: var(--m-r-sm);
		max-height: 20rem;
		overflow-y: auto;
		overscroll-behavior: contain;
		scrollbar-color: var(--m-border) transparent;
		scrollbar-width: thin;
	}

	.batch-dialog__list::-webkit-scrollbar {
		width: 10px;
	}

	.batch-dialog__list::-webkit-scrollbar-track {
		background: transparent;
	}

	.batch-dialog__list::-webkit-scrollbar-thumb {
		background: var(--m-border);
		border-radius: 5px;
		border: 2px solid var(--m-panel);
	}

	.batch-dialog__list::-webkit-scrollbar-thumb:hover {
		background: var(--m-border-strong);
	}

	.batch-dialog__item {
		border-bottom: 1px solid var(--m-border-soft);
		background: var(--m-panel);
	}

	.batch-dialog__item--picked {
		background: var(--m-selected);
	}

	.batch-dialog__item--picked .batch-dialog__pick:hover {
		background: transparent;
	}

	.batch-dialog__item:last-child {
		border-bottom: 0;
	}

	.batch-dialog__pick {
		display: flex;
		align-items: center;
		gap: 10px;
		min-height: 48px;
		padding: 8px 12px;
		cursor: pointer;
	}

	.batch-dialog__pick:hover {
		background: var(--m-surface);
	}

	.batch-dialog__pick input {
		width: 18px;
		height: 18px;
		flex-shrink: 0;
		accent-color: var(--focus-ring);
		cursor: pointer;
	}

	.batch-dialog__thumb {
		display: block;
		width: 32px;
		height: 32px;
		flex-shrink: 0;
		object-fit: cover;
		background: var(--elevated);
		border: 1px solid var(--m-border);
		border-radius: 4px;
	}

	.batch-dialog__thumb--empty {
		background: linear-gradient(135deg, var(--elevated) 50%, var(--m-border-soft) 50%);
	}

	.batch-dialog__pick-body {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
	}

	.batch-dialog__name {
		font-size: 14px;
		font-weight: 600;
		line-height: 1.4;
		color: var(--m-ink);
		overflow-wrap: anywhere;
	}

	.batch-dialog__record {
		font-size: 12.5px;
		line-height: 1.4;
		color: var(--m-muted);
		overflow-wrap: anywhere;
	}

	.batch-dialog__foot {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 12px;
		flex-wrap: wrap;
	}

	.batch-dialog__actions {
		display: flex;
		gap: 8px;
	}

	/* ── Screen-reader live region ───────────────────────────── */
	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}

	/* ── Responsive ──────────────────────────────────────────── */
	@media (max-width: 1100px) {
		.batch__row {
			grid-template-columns: 20px 40px minmax(0, 0.95fr) minmax(0, 1.25fr);
			grid-template-areas:
				'check thumb names record'
				'check thumb names actions';
			row-gap: 6px;
		}

		.batch__actions {
			flex-direction: row;
			align-items: center;
			justify-content: flex-start;
			gap: 10px;
		}

		.batch__record-art {
			width: 40px;
			height: 40px;
		}
	}

	@media (max-width: 900px) {
		/* The page head scrolls with the list on phones. */
		.match__head {
			position: static;
			margin: 0 -1rem 16px;
			padding: 12px 1rem 12px;
		}

		.match__head-in {
			flex-wrap: wrap;
			gap: 12px;
		}

		.match__title {
			font-size: 24px;
		}

		.match__head-actions {
			width: 100%;
			flex-direction: column;
			align-items: stretch;
			gap: 10px;
		}

		.match__commit-row {
			width: 100%;
		}

		.match__commit-row :global(.btn) {
			flex: 1;
		}

		.match__modes {
			display: flex;
			width: 100%;
		}

		.match__mode {
			flex: 1;
			min-height: 44px;
		}

		.match__head :global(.match__save) {
			width: 100%;
			min-height: 44px;
		}

		.match__grid {
			display: block;
		}

		.match__index {
			max-height: none;
			min-height: 0;
			margin-bottom: 14px;
		}

		.match__index-list {
			max-height: 220px;
			overflow-y: auto;
		}

		.match__src {
			padding: 10px 12px;
			min-height: 64px;
		}

		.match__source {
			flex-wrap: wrap;
			padding: 14px 16px;
		}

		.match__position {
			margin-top: 0;
			width: 100%;
			justify-content: flex-start;
		}

		.match__sechead,
		.match__record,
		.match__removal,
		.match__failed,
		.match__nomatch {
			padding-left: 16px;
			padding-right: 16px;
		}

		.match__change {
			margin: 0 16px 18px;
		}

		.match__edition {
			margin-left: 16px;
			margin-right: 16px;
			grid-template-columns: 1fr;
		}

		.match__cover {
			width: 112px;
			height: 112px;
		}

		.match__record-copy h3 {
			font-size: 20px;
		}

		.match__search {
			flex-wrap: wrap;
		}

		.match__search label {
			width: 100%;
			margin-right: 0;
			margin-bottom: 0;
		}
	}

	@media (max-width: 760px) {
		.batch__bar {
			flex-direction: row;
			align-items: center;
			justify-content: space-between;
			gap: 4px 12px;
			padding: 10px 14px;
		}

		.batch__alert {
			padding: 12px 14px;
		}

		.batch-dialog__foot {
			flex-direction: column;
			align-items: stretch;
		}

		.batch-dialog__actions :global(.btn) {
			flex: 1;
		}

		.batch__row,
		.batch__row--skeleton {
			grid-template-columns: 20px minmax(0, 1fr);
			grid-template-areas:
				'check names'
				'record record'
				'actions actions';
			row-gap: 8px;
			padding: 12px 14px;
		}

		.batch__thumb,
		.batch__skel-thumb {
			display: none;
		}

		.batch__names {
			grid-template-columns: minmax(0, 1fr);
			gap: 2px;
		}

		.batch__name {
			display: grid;
			grid-template-columns: 46px minmax(0, 1fr);
			align-items: baseline;
			column-gap: 4px;
			row-gap: 0;
		}

		.batch__name-head {
			grid-column: 1;
			flex-wrap: nowrap;
		}

		.batch__name-note {
			display: none;
		}

		.batch__name-value {
			grid-column: 2;
		}

		.batch__arrow {
			display: block;
			width: 12px;
			height: 12px;
			transform: rotate(90deg);
			justify-self: start;
			margin-left: 16px;
			margin-block: -2px;
		}

		.batch__thumb {
			align-self: start;
		}

		.batch__name-value {
			white-space: normal;
			overflow-wrap: anywhere;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			line-clamp: 2;
			-webkit-box-orient: vertical;
		}

		.batch__name-value.batch__name-value--source {
			display: flex;
			align-items: center;
			gap: 6px;
		}

		.batch__name-source-text {
			min-width: 0;
		}

		.batch__name-art {
			display: block;
		}

		.batch__record {
			align-items: flex-start;
		}

		.batch__record-art {
			margin-bottom: 2px;
		}

		.batch__record-name {
			white-space: normal;
			overflow-wrap: anywhere;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			line-clamp: 2;
			-webkit-box-orient: vertical;
		}

		.batch__record-meta,
		.batch__record-edition {
			white-space: normal;
		}

		.batch__actions {
			justify-content: space-between;
			padding-left: 30px;
			flex-wrap: wrap;
			gap: 8px;
		}

		.batch__removal {
			padding: 10px 14px;
		}

		.batch__check {
			align-self: start;
			min-height: 44px;
		}

		.batch__actions :global(.btn) {
			min-height: 44px;
		}

		.batch__foot {
			padding: 12px 14px;
		}

		.match__record {
			gap: 14px;
		}

		.match__source-art figcaption,
		.match__art figcaption {
			display: none;
		}

		.match__source-cover {
			width: 48px;
			height: 48px;
		}

		.match__cover {
			width: 96px;
			height: 96px;
		}
	}

	@media (max-width: 430px) {
		.match__record {
			flex-wrap: wrap;
		}

		.match__art {
			flex-direction: row;
		}

		.match__cover {
			width: 84px;
			height: 84px;
		}

		.match__record-copy {
			flex-basis: 100%;
		}
	}
</style>
