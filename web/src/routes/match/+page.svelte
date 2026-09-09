<script lang="ts">
	import { get, put, del } from '$lib/api';
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
	} from '$lib/types';
	import {
		extractAsin,
		peopleByRole,
		pickCover,
		hitCoverUrl,
		bookCoverUrl,
		formatRuntime
	} from '$lib/types';
	import { PageHeader, Button, Alert, Skeleton, EmptyState, Modal, AbdTag } from '$lib/components';
	import { cleanAdbError } from '$lib/adbError';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';

	interface CandidateDetails {
		book: AudiobookDBBook | null;
		release: AudiobookDBRelease | null;
	}

	interface MatchCandidate {
		bookId: number;
		srcPath: string;
		title: string;
		/** Local cover (assigned at intake) — the identity column's image. */
		coverUrl: string;
		searchResults: AudiobookDBBook[];
		selectedBookId: string | null;
		selectedReleaseId: string | null;
		details: CandidateDetails;
		loading: boolean;
		error: string | null;
		// Track how the selection was made
		manualPick: boolean;
		// Remove confirmation/undo state
		removing: boolean;
		_removeSnapshot?: { bookId: number; srcPath: string; title: string };
	}

	let candidates = $state<MatchCandidate[]>([]);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let saving = $state(false);
	let pageError = $state<string | null>(null);

	let modalOpen = $state(false);
	let modalCandidateId = $state<number | null>(null);
	let modalQuery = $state('');
	let modalSearching = $state(false);
	let modalResults = $state<AudiobookDBBook[]>([]);
	let modalError = $state<string | null>(null);
	let modalSearched = $state(false);

	// Custom match listbox — which candidate's picker is open + keyboard cursor
	let listboxCandidateId = $state<number | null>(null);
	let listboxIndex = $state(-1);

	// Settings for the pipeline strip's "Out" path (output dir + scheme)
	let inputDir = $state('');
	let outputDir = $state('');
	let outputScheme = $state('');

	async function loadPending() {
		await dl.run(async () => {
			pageError = null;
			try {
				// Pipeline strip needs the roots for the "In"/"Out" shorthand
				try {
					const settings = await get<Settings>('/settings');
					inputDir = settings.input_dir || '';
					outputDir = settings.output_dir || '';
					outputScheme = settings.output_scheme || '';
				} catch {
					// Settings unavailable — the strip still shows the source path
				}
				const response = await get<BooksListResponse>('/books?status=pending&limit=200');
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
					manualPick: false,
					removing: false
				}));
				for (const candidate of candidates) {
					autoMatch(candidate);
				}
			} catch (e) {
				pageError = e instanceof Error ? e.message : 'Failed to load pending books';
			}
		});
	}

	async function autoMatch(candidate: MatchCandidate) {
		try {
			const hits = await searchBooks(stripExtension(candidate.title));
			candidate.searchResults = hits;
			if (hits.length > 0) {
				candidate.selectedBookId = hits[0].id;
				candidate.details = await fetchDetails(hits[0].id);
				candidate.selectedReleaseId = candidate.details.release?.id ?? null;
				candidate.manualPick = false;
			}
		} catch (e) {
			candidate.error = e instanceof Error ? e.message : 'Search failed';
		} finally {
			candidate.loading = false;
		}
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

	async function fetchDetails(bookId: string): Promise<CandidateDetails> {
		const book = await get<AudiobookDBBook>(
			`/search/books/${bookId}?include=releases,people,series,images,external`
		);
		const releaseId = book.releases?.[0]?.id;
		if (!releaseId) return { book, release: null };
		const release = await get<AudiobookDBRelease>(
			`/search/releases/${releaseId}?include=book,chapterDetail,external,images,language,people,publisher`
		);
		return { book, release };
	}

	async function fetchRelease(releaseId: string): Promise<AudiobookDBRelease> {
		return get<AudiobookDBRelease>(
			`/search/releases/${releaseId}?include=book,chapterDetail,external,images,language,people,publisher`
		);
	}

	async function selectResult(candidate: MatchCandidate, book: AudiobookDBBook) {
		candidate.loading = true;
		candidate.error = null;
		try {
			candidate.selectedBookId = book.id;
			candidate.details = await fetchDetails(book.id);
			candidate.selectedReleaseId = candidate.details.release?.id ?? null;
			candidate.manualPick = true;
		} catch (e) {
			candidate.error = e instanceof Error ? e.message : 'Could not load details';
		} finally {
			candidate.loading = false;
		}
	}

	async function removeCandidate(id: number, candidate: MatchCandidate) {
		// Keep the candidate in place until the operator explicitly confirms.
		candidate.removing = true;
		candidate._removeSnapshot = { bookId: id, srcPath: candidate.srcPath, title: candidate.title };
	}

	async function confirmRemoveCandidate(candidate: MatchCandidate) {
		if (!candidate._removeSnapshot) return;
		const id = candidate._removeSnapshot.bookId;
		try {
			await del(`/books/${id}`);
			candidates = candidates.filter((c) => c.bookId !== id);
		} catch (e) {
			pageError = e instanceof Error ? e.message : 'Failed to remove book';
			candidate.removing = false;
			candidate._removeSnapshot = undefined;
		}
	}

	function undoRemoveCandidate(candidate: MatchCandidate) {
		candidate.removing = false;
		candidate._removeSnapshot = undefined;
	}

	// ── Pipeline strip ─────────────────────────────────────────
	// The output path this book will get from its current metadata.
	// The scheme's {placeholders} are resolved with the selected match;
	// unresolved ones stay visibly unfilled so nothing is implied.
	function resolveScheme(candidate: MatchCandidate): string {
		if (!outputScheme) return '';
		const details = candidate.details;
		const people = [...(details.book?.people || []), ...(details.release?.people || [])];
		const author = peopleByRole(people, 'author')[0]?.name ?? '';
		const title = details.release?.title || details.book?.title || candidate.title;
		return outputScheme.replace(/\{(\w+)\}/g, (raw, key: string) => {
			const k = key.toLowerCase();
			if (k === 'author') return author || raw;
			if (k === 'title') return title || raw;
			if (k === 'narrator') return peopleByRole(people, 'narrator')[0]?.name || raw;
			if (k === 'year') {
				const date = details.release?.releaseDate || details.book?.originallyPublishedAt;
				return date ? String(new Date(date).getFullYear()) : raw;
			}
			return raw;
		});
	}

	function routesTo(candidate: MatchCandidate): string {
		if (!outputDir) return 'output folder — set in Settings';
		const resolved = resolveScheme(candidate);
		return `${outputDir.replace(/\/$/, '')}/${resolved || outputScheme}`;
	}

	/** Shorthand display: paths under the input/output roots become
	 *  '‹input›relative/path' / '‹output›relative/path'; anything
	 *  else shows absolute. Full path stays in the tooltip. */
	function srcShort(candidate: MatchCandidate): string {
		if (inputDir && candidate.srcPath.startsWith(inputDir)) {
			return `‹input›${candidate.srcPath.slice(inputDir.length).replace(/^\//, '')}`;
		}
		// Seed/mounted paths may differ from the configured root; anchor on
		// the root's folder name instead ('…/input/…' → ‹input›rest).
		const rootName = inputDir ? inputDir.split('/').filter(Boolean).pop() : '';
		if (rootName) {
			const m = candidate.srcPath.match(`/${rootName}/(.+)$`);
			if (m) return `‹input›${m[1]}`;
		}
		return candidate.srcPath;
	}

	function outShort(candidate: MatchCandidate): string {
		if (!outputDir) return 'output — set in Settings';
		if (!outputScheme) return '‹output›';
		const resolved = resolveScheme(candidate);
		return `‹output›${resolved}`;
	}


	/** Split '‹chip›rest' into {chip label, rest} for chip-styled rendering. */
	function splitShort(text: string): { chip: string; rest: string } {
		const m = text.match(/^‹(\w+)›(.*)$/);
		return m ? { chip: m[1], rest: m[2] } : { chip: '', rest: text };
	}

	function stripExtension(name: string): string {
		return name.replace(/\.[^/.]+$/, '');
	}

	// API errors embed raw response bodies ("API 502: {\"error\":...}").
	// Pull the human sentence out; callers keep the raw string for the expandable detail.
	function cleanErrorMessage(message: string): string {
		let cleaned = message;
		const start = cleaned.indexOf('{');
		const end = cleaned.lastIndexOf('}');
		if (start !== -1 && end > start) {
			try {
				const parsed = JSON.parse(cleaned.slice(start, end + 1)) as Record<string, unknown>;
				const inner = (parsed.error ?? parsed.detail ?? parsed.message) as unknown;
				if (typeof inner === 'string' && inner.trim()) cleaned = inner;
			} catch {
				// not JSON — keep original
			}
		}
		cleaned = cleaned.replace(/^API \d{3}:\s*/, '');
		// Go unmarshal noise → operator-sensible copy
		if (/cannot unmarshal|unmarshal/i.test(cleaned)) {
			return 'AudiobookDB returned a record the pipeline couldn’t read — try Custom search below.';
		}
		return cleaned;
	}

	function selectedTitle(candidate: MatchCandidate): string {
		return candidate.details.release?.title || candidate.details.book?.title || candidate.title;
	}

	function selectedAuthors(candidate: MatchCandidate): string {
		const people = [...(candidate.details.book?.people || []), ...(candidate.details.release?.people || [])];
		return peopleByRole(people, 'author')
			.map((p) => p.name)
			.join(', ') || 'Unknown author';
	}

	function selectedNarrators(candidate: MatchCandidate): string {
		const people = [...(candidate.details.book?.people || []), ...(candidate.details.release?.people || [])];
		return peopleByRole(people, 'narrator')
			.map((p) => p.name)
			.join(', ') || '';
	}

	function selectedCover(candidate: MatchCandidate): string {
		return pickCover(candidate.details.release?.images) || pickCover(candidate.details.book?.images) || '';
	}


	// ── Custom match listbox ───────────────────────────────────
	function toggleListbox(candidate: MatchCandidate) {
		if (listboxCandidateId === candidate.bookId) {
			listboxCandidateId = null;
			listboxIndex = -1;
		} else {
			listboxCandidateId = candidate.bookId;
			// Cursor starts on the current selection, else the first hit
			const idx = candidate.searchResults.findIndex((b) => b.id === candidate.selectedBookId);
			listboxIndex = idx >= 0 ? idx : candidate.searchResults.length > 0 ? 0 : -1;
		}
	}

	function closeListbox() {
		listboxCandidateId = null;
		listboxIndex = -1;
	}

	function pickResult(candidate: MatchCandidate, book: AudiobookDBBook) {
		closeListbox();
		selectResult(candidate, book);
	}

	function moveListboxCursor(candidate: MatchCandidate, delta: number) {
		const n = candidate.searchResults.length;
		if (n === 0) return;
		listboxIndex = (listboxIndex + delta + n) % n;
	}

	function listboxKeydown(event: KeyboardEvent, candidate: MatchCandidate) {
		switch (event.key) {
			case 'ArrowDown':
				event.preventDefault();
				moveListboxCursor(candidate, 1);
				break;
			case 'ArrowUp':
				event.preventDefault();
				moveListboxCursor(candidate, -1);
				break;
			case 'Enter':
				event.preventDefault();
				if (listboxIndex >= 0 && listboxIndex < candidate.searchResults.length) {
					pickResult(candidate, candidate.searchResults[listboxIndex]);
				}
				break;
			case 'Escape':
				event.preventDefault();
				closeListbox();
				break;
		}
	}

	// Returns a provenance label for how the current selection was made
	function matchLabel(candidate: MatchCandidate): string {
		if (candidate.loading && !candidate.details.book) return 'Loading…';
		if (candidate.error) return 'Search failed';
		if (candidate.details.book) {
			return candidate.manualPick ? 'Manually selected' : 'Auto-selected';
		}
		return 'No match';
	}

	// Returns a visual kind for the match label (used for color coding)
	function matchKind(candidate: MatchCandidate): 'auto' | 'manual' | 'none' | 'loading' | 'error' {
		if (candidate.loading && !candidate.details.book) return 'loading';
		if (candidate.error) return 'error';
		if (candidate.details.book) {
			return candidate.manualPick ? 'manual' : 'auto';
		}
		return 'none';
	}

	function openCustomSearch(candidate: MatchCandidate) {
		modalCandidateId = candidate.bookId;
		modalQuery = stripExtension(candidate.title);
		modalResults = [];
		modalError = null;
		modalSearched = false;
		modalOpen = true;
	}

	async function runCustomSearch() {
		if (!modalQuery.trim()) return;
		modalSearching = true;
		modalError = null;
		modalResults = [];
		modalSearched = false;
		try {
			modalResults = await searchBooks(modalQuery);
			modalSearched = true;
		} catch (e) {
			modalError = e instanceof Error ? e.message : 'Search failed. Check your connection and try again.';
			modalSearched = true;
		} finally {
			modalSearching = false;
		}
	}

	async function pickModalResult(book: AudiobookDBBook) {
		const candidate = candidates.find((c) => c.bookId === modalCandidateId);
		if (!candidate) return;
		await selectResult(candidate, book);
		modalOpen = false;
	}

	async function saveMatches() {
		saving = true;
		pageError = null;
		try {
			const matched = candidates.filter((c) => c.selectedBookId && c.details.book);
			await Promise.all(
				matched.map(async (candidate) => {
					const body = buildUpdate(candidate);
					await put<BookWithPeople>(`/books/${candidate.bookId}`, body);
				})
			);
			await goto('/process');
		} catch (e) {
			pageError = e instanceof Error ? e.message : 'Failed to save matches';
			throw e;
		} finally {
			saving = false;
		}
	}

	function buildUpdate(candidate: MatchCandidate): UpdateBookRequest {
		const book = candidate.details.book;
		const release = candidate.details.release;
		const people = [...(candidate.details.book?.people || []), ...(candidate.details.release?.people || [])];
		const releaseDate = release?.releaseDate || book?.originallyPublishedAt || '';
		const runtime = release?.runtimeLengthMs ? Math.round(release.runtimeLengthMs / 60000) : 0;
		const series = (book?.series || [])
			.map((s) => `${s.seriesId || 'Series'} #${s.position}`)
			.join(', ');
		const external = book?.external || [];
		const asin = extractAsin(external);
		return {
			title: release?.title || book?.title || candidate.title,
			asin: asin || undefined,
			audiobookdb_book_id: book?.id,
			audiobookdb_release_id: release?.id,
			description: book?.description || undefined,
			release_date: releaseDate,
			series: series || undefined,
			publisher: release?.publisher?.name || undefined,
			language: release?.language?.title || undefined,
			runtime_length_minutes: runtime || undefined,
			cover_image_url: selectedCover(candidate),
			status: 'matched',
			authors: peopleByRole(people, 'author'),
			narrators: peopleByRole(people, 'narrator')
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
		if (listboxCandidateId !== null && !(e.target instanceof Element && e.target.closest('.match-listbox, .match-listbox__trigger'))) {
			closeListbox();
		}
	}}
/>

<PageHeader
	title="Match"
	station="#02 · MATCH"
	description="Confirm or change the AudiobookDB metadata for each source routed from intake."
/>

{#if pageError}
	<div class="mb-6">
		<Alert variant="error" onretry={loadPending}>{pageError}</Alert>
	</div>
{/if}

{#if dl.showSkeleton}
	<!-- Skeleton loading state -->
	<div class="space-y-4">
		{#each Array(3) as _}
			<div class="panel p-4">
				<div class="flex gap-6">
					<Skeleton variant="rect" width="160px" height="160px" />
					<div class="flex-1 space-y-2">
						<Skeleton height="1.25rem" width="40%" />
						<Skeleton height="1rem" width="60%" />
						<Skeleton height="2.5rem" width="100%" />
					</div>
				</div>
			</div>
		{/each}
	</div>
{:else if candidates.length === 0}
	<!-- Empty state -->
	<EmptyState
		title="Nothing to match"
		description="Import source directories first, then come back here to choose metadata."
		actionLabel="Import books"
		actionHref="/import"
	/>
{:else}
	<!-- Match bay: list of candidates with metadata panels -->
	<div class="space-y-4">
		{#each candidates as candidate (candidate.bookId)}
			<div class="panel p-4">
				<!-- Pipeline strip — full-width: where this row's files enter and exit.
				     Roots are shortened to ‹input›/‹output› chips; full absolute
				     paths live in the tooltips. -->
				<div class="matchrow__pipeline" aria-label="Processing path">
					<span class="matchrow__pipe-label">In</span>
					<code class="matchrow__pipe-path" title={candidate.srcPath}>{#if splitShort(srcShort(candidate)).chip}<span class="matchrow__pipe-chip">{splitShort(srcShort(candidate)).chip}</span>{/if}{splitShort(srcShort(candidate)).rest}</code>
					<span class="matchrow__pipe-arrow" aria-hidden="true">→</span>
					<span class="matchrow__pipe-label">Out</span>
					<code class="matchrow__pipe-path" title="{routesTo(candidate)}">{#if splitShort(outShort(candidate)).chip}<span class="matchrow__pipe-chip">{splitShort(outShort(candidate)).chip}</span>{/if}{splitShort(outShort(candidate)).rest}</code>
				</div>

				<div class="mt-4 flex flex-col gap-5 sm:flex-row sm:gap-6">
					<!-- Identity column — the LOCAL book's cover only -->
					<div class="flex flex-shrink-0 sm:block sm:w-40">
						{#if candidate.coverUrl}
							<img
								src={bookCoverUrl(candidate.coverUrl, 'medium')}
								alt=""
								loading="lazy"
								decoding="async"
								class="h-24 w-24 flex-shrink-0 rounded-sm object-cover border border-[var(--border-subtle)] sm:h-40 sm:w-full"
							/>
						{:else}
							<div class="flex h-24 w-24 flex-shrink-0 items-center justify-center rounded-sm border border-[var(--border-subtle)] bg-[var(--elevated)] text-xs text-[var(--text-muted)] sm:h-40 sm:w-full">
								No cover
							</div>
						{/if}
					</div>

					<!-- Decision column — the record being confirmed -->
					<div class="min-w-0 flex-1">
						<div class="flex items-start justify-between gap-3">
							{#if candidate.loading && !candidate.details.book}
								<span class="flex items-center gap-1.5 text-xs font-medium text-[var(--text-muted)]">
									<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--text-muted)]" aria-hidden="true"></span>
									Searching…
								</span>
							{:else if candidate.details.book}
								<span class="flex items-center gap-1.5 text-xs font-medium text-[var(--text-muted)]">
									{#if matchKind(candidate) === 'auto'}
										<span class="h-1.5 w-1.5 rounded-full bg-[var(--accent)]" aria-hidden="true"></span>
										Auto-selected
									{:else}
										<span class="h-1.5 w-1.5 rounded-full bg-[var(--state-green)]" aria-hidden="true"></span>
										Manually selected
									{/if}
									from
									<!-- Data provenance mark: linked to the matched record on AudiobookDB -->
									<AbdTag href={candidate.details.book ? `https://audiobookdb.org/book/${candidate.details.book.id}` : undefined} />
								</span>
							{:else}
								<span class="flex items-center gap-1.5 text-xs font-medium text-[var(--text-muted)]">
									<span class="h-1.5 w-1.5 rounded-full bg-[var(--border)]" aria-hidden="true"></span>
									No match yet
								</span>
							{/if}
							{#if candidate.removing}
								<!-- Branded confirmation/undo for destructive remove -->
								<div class="flex flex-shrink-0 items-center gap-2">
									<span class="text-sm text-[var(--text-muted)]">Remove "{candidate.title}"?</span>
									<button
										type="button"
										class="text-sm font-medium text-[var(--state-red)] hover:underline"
										onclick={() => confirmRemoveCandidate(candidate)}
									>
										Yes, remove
									</button>
									<button
										type="button"
										class="text-sm text-[var(--accent)] hover:underline"
										onclick={() => undoRemoveCandidate(candidate)}
									>
										Cancel
									</button>
								</div>
							{:else}
								<Button
									variant="ghost"
									class="flex-shrink-0 text-[var(--state-red)] hover:bg-[var(--state-red-bg)]"
									onclick={() => removeCandidate(candidate.bookId, candidate)}
								>
									Remove
								</Button>
							{/if}
						</div>

						{#if candidate.loading && !candidate.details.book}
							<!-- Still searching -->
							<div class="mt-3">
								<Skeleton height="1.5rem" width="60%" />
							</div>
						{:else if candidate.error}
							<!-- Per-candidate search failure — human copy, raw detail expandable -->
							{@const cleaned = cleanAdbError(candidate.error)}
							<div class="mt-3">
								<div class="flex flex-wrap items-center gap-2">
									<span class="flex h-2 w-2 flex-shrink-0 rounded-full bg-[var(--state-red)]" aria-hidden="true"></span>
									<span class="font-medium text-[var(--state-red)]">Search failed</span>
									<span class="text-[var(--text-muted)]">— {cleaned.message}</span>
									<button
										type="button"
										class="text-sm font-medium text-[var(--accent)] hover:underline"
										onclick={() => autoMatch(candidate)}
									>
										Retry
									</button>
									{#if cleaned.rateLimited}
										<a href="/settings" class="text-sm font-medium text-[var(--accent)] hover:underline">
											Settings → API key
										</a>
									{/if}
								</div>
								<details class="mt-1.5">
									<summary class="cursor-pointer select-none text-xs text-[var(--text-muted)] hover:text-[var(--text-secondary)]">
										Technical detail
									</summary>
									<p class="mt-1 break-all rounded-sm border border-[var(--border-subtle)] bg-[var(--surface)] p-2 font-mono text-xs text-[var(--text-muted)]">
										{cleaned.raw}
									</p>
								</details>
							</div>
						{:else if candidate.details.book}
							<!-- Match found — the one record, in full -->
							<div class="mt-2">
								<h3 class="text-xl font-semibold text-[var(--enamel)]">{selectedTitle(candidate)}</h3>
								<p class="mt-1 text-sm text-[var(--text)]">{selectedAuthors(candidate) || 'Unknown author'}</p>
								{#if selectedNarrators(candidate)}
									<p class="mt-0.5 text-sm text-[var(--text-secondary)]">
										Narrated by {selectedNarrators(candidate)}
									</p>
								{/if}
								{#if candidate.details.release?.runtimeLengthMs}
									<p class="mt-1.5 font-mono text-xs text-[var(--text-muted)]">
										{formatRuntime(Math.round(candidate.details.release.runtimeLengthMs / 60000))}
									</p>
								{/if}
							</div>
						{:else}
							<!-- No matches found for this title -->
							<div class="mt-3">
								<div class="flex items-center gap-2 text-sm">
									<span class="font-medium text-[var(--text-secondary)]">No matches found</span>
									<span class="text-[var(--text-muted)]">— search below to find metadata for this book.</span>
								</div>
							</div>
						{/if}

						<!-- Selection controls: rich listbox + custom search -->
						<div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-center">
							<div class="sm:w-96" class:relative={listboxCandidateId === candidate.bookId}>
								<button
									type="button"
									role="combobox"
									class="match-listbox__trigger"
									class:match-listbox__trigger--placeholder={!candidate.selectedBookId}
									disabled={candidate.loading}
									aria-expanded={listboxCandidateId === candidate.bookId}
									aria-haspopup="listbox"
									aria-controls={listboxCandidateId === candidate.bookId ? `match-list-${candidate.bookId}` : undefined}
									aria-activedescendant={listboxCandidateId === candidate.bookId && listboxIndex >= 0 ? `match-opt-${candidate.bookId}-${listboxIndex}` : undefined}
									aria-label="Select match for {candidate.title}"
									onclick={() => toggleListbox(candidate)}
									onkeydown={(e) => {
										if (listboxCandidateId === candidate.bookId) {
											listboxKeydown(e, candidate);
										} else if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
											e.preventDefault();
											toggleListbox(candidate);
										}
									}}
								>
									<span class="truncate">
										{#if candidate.selectedBookId}
											{candidate.searchResults.find((b) => b.id === candidate.selectedBookId)?.title
												?? candidate.details.book?.title
												?? '—'}
										{:else}
											No match selected
										{/if}
									</span>
									<svg class="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="square" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
								</button>

								{#if listboxCandidateId === candidate.bookId}
									<ul
										class="match-listbox"
										id="match-list-{candidate.bookId}"
										role="listbox"
										aria-label="Match results for {candidate.title}"
									>
										{#each candidate.searchResults as result, i (result.id)}
											{@const rAuthors = peopleByRole(result.people, 'author').map((p) => p.name).join(', ')}
											{@const rNarrators = peopleByRole(result.people, 'narrator').map((p) => p.name).join(', ')}
											{@const rCover = pickCover(result.images) || hitCoverUrl(result.image, 'small')}
											<li role="presentation">
												<button
													type="button"
													role="option"
													aria-selected={result.id === candidate.selectedBookId}
													id="match-opt-{candidate.bookId}-{i}"
													class="match-listbox__row"
													class:match-listbox__row--active={i === listboxIndex}
													class:match-listbox__row--selected={result.id === candidate.selectedBookId}
													onclick={() => pickResult(candidate, result)}
													onmousemove={() => (listboxIndex = i)}
												>
													{#if rCover}
														<img src={rCover} alt="" loading="lazy" class="match-listbox__cover" />
													{:else}
														<div class="match-listbox__cover match-listbox__cover--empty" aria-hidden="true">—</div>
													{/if}
													<div class="min-w-0 flex-1 text-left">
														<p class="truncate text-sm font-semibold text-[var(--text)]">{result.title}</p>
														<p class="truncate text-xs text-[var(--text-muted)]">
															{rAuthors || 'Unknown author'}
															{#if rNarrators}
																· {rNarrators}
															{/if}
														</p>
													</div>
													{#if result.originallyPublishedAt}
														<span class="font-mono text-xs text-[var(--text-muted)]">
															{new Date(result.originallyPublishedAt).getFullYear()}
														</span>
													{/if}
												</button>
											</li>
										{/each}
									</ul>
								{/if}
							</div>
							<Button
								variant="secondary"
								disabled={candidate.loading}
								onclick={() => openCustomSearch(candidate)}
							>
								Custom search
							</Button>
						</div>
					</div>
				</div>
			</div>
		{/each}
	</div>

	<!-- Batch save action -->
	<div class="mt-6 flex justify-end">
		<Button
			variant="primary"
			loading={saving}
			disabled={candidates.every((c) => !c.selectedBookId)}
			onclick={saveMatches}
		>
			{saving ? 'Saving…' : 'Save matches → Process'}
		</Button>
	</div>
{/if}

<!-- Custom search dialog (uses native HTML dialog via shared Modal) -->
<Modal bind:open={modalOpen} title="Custom search">
	<div class="space-y-4">
		<div class="flex gap-2">
			<input
				type="text"
				bind:value={modalQuery}
				placeholder="Title, author, or keywords"
				onkeydown={(e) => e.key === 'Enter' && runCustomSearch()}
				aria-label="Search query"
			/>
			<Button variant="primary" loading={modalSearching} onclick={runCustomSearch}>
				Search
			</Button>
		</div>

		{#if modalSearching}
			<div class="space-y-2">
				{#each Array(3) as _}
					<Skeleton height="3rem" />
				{/each}
			</div>
		{:else if modalResults.length > 0}
			<div class="max-h-72 space-y-2 overflow-auto">
				{#each modalResults as result (result.id)}
					<button
						type="button"
						class="w-full rounded-sm border border-[var(--border)] bg-[var(--bg)] p-3 text-left transition-colors hover:bg-[var(--surface-hover)]"
						onclick={() => pickModalResult(result)}
					>
						<p class="font-medium text-[var(--text)]">{result.title}</p>
						<p class="text-sm text-[var(--text-muted)]">
							{peopleByRole(result.people, 'author').map((p) => p.name).join(', ') || 'Unknown author'}
							{#if result.people && peopleByRole(result.people, 'narrator').length > 0}
								<span class="text-[var(--text-muted)]"> · Narrated by {peopleByRole(result.people, 'narrator').map((p) => p.name).join(', ')}</span>
							{/if}
						</p>
						{#if result.originallyPublishedAt}
							<p class="text-xs text-[var(--text-muted)] mt-0.5">Published {new Date(result.originallyPublishedAt).toLocaleDateString()}</p>
						{/if}
					</button>
				{/each}
			</div>
		{:else if modalError}
			<!-- Search failed — distinguish from zero results -->
			<div class="rounded-sm border border-[var(--state-red-border)] bg-[var(--state-red-bg)] p-3 text-sm text-[var(--text)]">
				<p class="font-medium">Search failed</p>
				<p class="mt-1 text-[var(--text-muted)]">{modalError}</p>
				<button
					type="button"
					class="mt-2 text-sm font-medium text-[var(--accent)] hover:underline"
					onclick={runCustomSearch}
				>
					Retry
				</button>
			</div>
		{:else if modalResults.length === 0 && modalSearched}
			<!-- Zero results — query was executed successfully -->
			<p class="text-sm text-[var(--text-muted)]">No matches found for "{modalQuery}". Try different keywords.</p>
		{/if}
	</div>
</Modal>

<style>
	/* Rows stay open-borders; the listbox popover must be able to
	   overflow the row panel. */
	.panel {
		overflow: visible;
	}

	/* ── Pipeline strip — In → Out across the full row top ────── */
	.matchrow__pipeline {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.5rem 0.625rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		min-width: 0;
	}

	.matchrow__pipe-label {
		flex-shrink: 0;
		font-size: 0.65rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.matchrow__pipe-path {
		flex: 1 1 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.75rem;
		color: var(--text-secondary);
	}

	.matchrow__pipe-chip {
		display: inline-block;
		padding: 0 0.3rem;
		margin-right: 0.25rem;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		color: var(--text);
		font-weight: 700;
		letter-spacing: 0.03em;
	}

	.matchrow__pipe-arrow {
		flex-shrink: 0;
		color: var(--border-strong);
		font-size: 0.875rem;
	}

	/* ── Match listbox — rich candidate picker ────────────────── */
	.match-listbox__trigger {
		display: flex;
		width: 100%;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		min-height: 2.75rem;
		padding: 0.5rem 0.75rem;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		font-size: 0.875rem;
		text-align: left;
	}

	.match-listbox__trigger:hover {
		border-color: var(--text-muted);
	}

	.match-listbox__trigger:focus-visible {
		border-color: var(--text);
		box-shadow: 0 0 0 2px var(--focus-ring);
	}

	.match-listbox__trigger--placeholder {
		color: var(--text-placeholder);
	}

	.match-listbox__trigger:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.match-listbox {
		position: absolute;
		top: calc(100% + 2px);
		left: 0;
		right: 0;
		z-index: 30;
		max-height: 20rem;
		overflow-y: auto;
		background: var(--bg-panel);
		border: 1.5px solid var(--border-strong);
		border-radius: var(--radius-sm);
		box-shadow: 0 8px 24px oklch(0 0 0 / 0.5);
		padding: 0.25rem;
		list-style: none;
		margin: 0;
	}

	.match-listbox__row {
		display: flex;
		width: 100%;
		align-items: center;
		gap: 0.625rem;
		min-height: 2.75rem;
		padding: 0.375rem 0.5rem;
		border-radius: var(--radius-sm);
		cursor: pointer;
		background: transparent;
		border: 1px solid transparent;
		color: inherit;
		text-align: left;
	}

	.match-listbox__row:focus-visible {
		outline: 2px solid var(--text);
		outline-offset: -2px;
	}

	.match-listbox__row--active {
		background: var(--surface-hover);
	}

	.match-listbox__row--selected {
		outline: 1px solid var(--border-strong);
	}

	.match-listbox__cover {
		width: 2.25rem;
		height: 2.25rem;
		flex-shrink: 0;
		border-radius: var(--radius-sm);
		object-fit: cover;
		border: 1px solid var(--border-subtle);
	}

	.match-listbox__cover--empty {
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--elevated);
		color: var(--text-muted);
		font-size: 0.75rem;
	}
</style>
