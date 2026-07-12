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
		UpdateBookRequest
	} from '$lib/types';
	import {
		extractAsin,
		peopleByRole,
		pickCover,
		formatRuntime
	} from '$lib/types';
	import { PageHeader, Button, Alert, Skeleton, EmptyState, Modal } from '$lib/components';

	interface CandidateDetails {
		book: AudiobookDBBook | null;
		release: AudiobookDBRelease | null;
	}

	interface MatchCandidate {
		bookId: number;
		srcPath: string;
		title: string;
		searchResults: AudiobookDBBook[];
		selectedBookId: string | null;
		selectedReleaseId: string | null;
		details: CandidateDetails;
		loading: boolean;
		error: string | null;
	}

	let candidates = $state<MatchCandidate[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let pageError = $state<string | null>(null);

	let modalOpen = $state(false);
	let modalCandidateId = $state<number | null>(null);
	let modalQuery = $state('');
	let modalSearching = $state(false);
	let modalResults = $state<AudiobookDBBook[]>([]);

	async function loadPending() {
		loading = true;
		pageError = null;
		try {
			const response = await get<BooksListResponse>('/books?status=pending&limit=200');
			candidates = (response.books || []).map((book) => ({
				bookId: book.id,
				srcPath: book.src_path,
				title: book.title,
				searchResults: [],
				selectedBookId: null,
				selectedReleaseId: null,
				details: { book: null, release: null },
				loading: true,
				error: null
			}));
			for (const candidate of candidates) {
				autoMatch(candidate);
			}
		} catch (e) {
			pageError = e instanceof Error ? e.message : 'Failed to load pending books';
		} finally {
			loading = false;
		}
	}

	async function autoMatch(candidate: MatchCandidate) {
		try {
			const hits = await searchBooks(stripExtension(candidate.title));
			candidate.searchResults = hits;
			if (hits.length > 0) {
				candidate.selectedBookId = hits[0].id;
				candidate.details = await fetchDetails(hits[0].id);
				candidate.selectedReleaseId = candidate.details.release?.id ?? null;
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
		} catch (e) {
			candidate.error = e instanceof Error ? e.message : 'Could not load details';
		} finally {
			candidate.loading = false;
		}
	}

	async function removeCandidate(id: number) {
		try {
			await del(`/books/${id}`);
			candidates = candidates.filter((c) => c.bookId !== id);
		} catch (e) {
			pageError = e instanceof Error ? e.message : 'Failed to remove book';
		}
	}

	function stripExtension(name: string): string {
		return name.replace(/\.[^/.]+$/, '');
	}

	function selectedTitle(candidate: MatchCandidate): string {
		return candidate.details.release?.title || candidate.details.book?.title || candidate.title;
	}

	function selectedAuthors(candidate: MatchCandidate): string {
		const people = candidate.details.release?.people || candidate.details.book?.people || [];
		return peopleByRole(people, 'author')
			.map((p) => p.name)
			.join(', ') || 'Unknown author';
	}

	function selectedNarrators(candidate: MatchCandidate): string {
		const people = candidate.details.release?.people || candidate.details.book?.people || [];
		return peopleByRole(people, 'narrator')
			.map((p) => p.name)
			.join(', ') || '';
	}

	function selectedCover(candidate: MatchCandidate): string {
		return pickCover(candidate.details.release?.images) || pickCover(candidate.details.book?.images) || '';
	}

	function openCustomSearch(candidate: MatchCandidate) {
		modalCandidateId = candidate.bookId;
		modalQuery = stripExtension(candidate.title);
		modalResults = [];
		modalOpen = true;
	}

	async function runCustomSearch() {
		if (!modalQuery.trim()) return;
		modalSearching = true;
		try {
			modalResults = await searchBooks(modalQuery);
		} catch {
			modalResults = [];
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
		const people = release?.people || book?.people || [];
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

<PageHeader title="Match" description="Confirm or change the audiobookdb metadata for each source." />

{#if pageError}
	<div class="mb-6">
		<Alert variant="error" onretry={loadPending}>{pageError}</Alert>
	</div>
{/if}

{#if loading}
	<div class="space-y-4">
		{#each Array(3) as _}
			<div class="card flex gap-4">
				<Skeleton variant="rect" width="120px" height="160px" />
				<div class="flex-1 space-y-2">
					<Skeleton height="1.25rem" width="40%" />
					<Skeleton height="1rem" width="60%" />
					<Skeleton height="2.5rem" width="100%" />
				</div>
			</div>
		{/each}
	</div>
{:else if candidates.length === 0}
	<EmptyState
		title="Nothing to match"
		description="Import source directories first, then come back here to choose metadata."
		actionLabel="Import books"
		actionHref="/import"
	/>
{:else}
	<div class="space-y-4">
		{#each candidates as candidate (candidate.bookId)}
			<div class="card">
				<div class="flex flex-col gap-4 sm:flex-row">
					<div class="flex-shrink-0">
						{#if selectedCover(candidate)}
							<img src={selectedCover(candidate)} alt="" class="h-40 w-28 rounded object-cover" />
						{:else}
							<div class="flex h-40 w-28 items-center justify-center rounded bg-[var(--elevated)] text-xs text-[var(--text-muted)]">
								No cover
							</div>
						{/if}
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between">
							<div>
								<p class="truncate text-xs font-mono text-[var(--text-muted)]">{candidate.srcPath}</p>
								<h3 class="mt-0.5 text-lg font-semibold">{candidate.title}</h3>
							</div>
							<Button variant="ghost" class="self-start text-[var(--error)] hover:bg-[var(--error-bg)]" onclick={() => removeCandidate(candidate.bookId)}>
								Remove
							</Button>
						</div>

						{#if candidate.loading && !candidate.details.book}
							<div class="mt-3">
								<Skeleton height="1.5rem" width="80%" />
							</div>
						{:else if candidate.error}
							<p class="mt-3 text-sm text-[var(--error)]">{candidate.error}</p>
						{:else if candidate.details.book}
							<div class="mt-3 space-y-1">
								<p class="font-medium text-[var(--text)]">{selectedTitle(candidate)}</p>
								<p class="text-sm text-[var(--text-muted)]">
									{selectedAuthors(candidate)}
									{#if selectedNarrators(candidate)}
										<span class="text-[var(--text-muted)]"> · Narrated by {selectedNarrators(candidate)}</span>
									{/if}
								</p>
								{#if candidate.details.release?.runtimeLengthMs}
									<p class="text-xs text-[var(--text-muted)]">
										{formatRuntime(Math.round(candidate.details.release.runtimeLengthMs / 60000))}
									</p>
								{/if}
							</div>
						{:else}
							<p class="mt-3 text-sm text-[var(--text-muted)]">No match found.</p>
						{/if}

						<div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-center">
							<select
								class="sm:w-96"
								value={candidate.selectedBookId ?? ''}
								onchange={(e) => {
									const book = candidate.searchResults.find((b) => b.id === e.currentTarget.value);
									if (book) selectResult(candidate, book);
								}}
								disabled={candidate.loading}
							>
								<option value="" disabled>No match selected</option>
								{#each candidate.searchResults as result (result.id)}
									<option value={result.id}>{result.title}</option>
								{/each}
							</select>
							<Button variant="secondary" disabled={candidate.loading} onclick={() => openCustomSearch(candidate)}>
								Custom search
							</Button>
						</div>
					</div>
				</div>
			</div>
		{/each}
	</div>

	<div class="mt-6 flex justify-end">
		<Button variant="primary" loading={saving} disabled={candidates.every((c) => !c.selectedBookId)} onclick={saveMatches}>
			Save matches and continue
		</Button>
	</div>
{/if}

<Modal bind:open={modalOpen} title="Custom search">
	<div class="space-y-4">
		<div class="flex gap-2">
			<input type="text" bind:value={modalQuery} placeholder="Title, author, or keywords" onkeydown={(e) => e.key === 'Enter' && runCustomSearch()} />
			<Button variant="primary" loading={modalSearching} onclick={runCustomSearch}>Search</Button>
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
						class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg)] p-3 text-left transition-colors hover:bg-[var(--surface-hover)]"
						onclick={() => pickModalResult(result)}
					>
						<p class="font-medium text-[var(--text)]">{result.title}</p>
						<p class="text-sm text-[var(--text-muted)]">
							{peopleByRole(result.people, 'author').map((p) => p.name).join(', ') || 'Unknown author'}
						</p>
					</button>
				{/each}
			</div>
		{:else if modalResults.length === 0 && !modalSearching && modalQuery}
			<p class="text-sm text-[var(--text-muted)]">No results. Try different keywords.</p>
		{/if}
	</div>
</Modal>
