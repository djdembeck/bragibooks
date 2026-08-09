<script lang="ts">
	import { get } from '$lib/api';
import { goto } from '$app/navigation';
import { page as pageStore } from '$app/stores';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, Button } from '$lib/components';
	import { formatRuntime } from '$lib/types';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';

	const limit = 20;
	const filters: { value: string; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'matched', label: 'Matched' },
		{ value: 'processing', label: 'Processing' },
		{ value: 'done', label: 'Done' },
		{ value: 'error', label: 'Error' },
	];

	let filter = $state('all');
	let currentPage = $state(0);
	let response = $state<BooksListResponse | null>(null);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	const validFilters = new Set(filters.map((item) => item.value));

	function normalizedFilter(value: string | null): string {
		return value && validFilters.has(value) ? value : 'all';
	}

	function normalizedPage(value: string | null): number {
		const parsed = Number(value);
		return Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
	}

	function updateUrl(nextFilter: string, nextPage: number) {
		const params = new URLSearchParams($pageStore.url.searchParams);
		if (nextFilter === 'all') params.delete('status');
		else params.set('status', nextFilter);
		if (nextPage > 0) params.set('page', String(nextPage));
		else params.delete('page');
		const query = params.toString();
		void goto(`/books${query ? `?${query}` : ''}`, {
			replaceState: true,
			keepFocus: true,
			noScroll: true
		});
	}

	async function loadBooks() {
		await dl.run(async () => {
			const statusParam = filter === 'all' ? '' : `status=${filter}&`;
			const nextResponse = await get<BooksListResponse>(`/books?${statusParam}page=${currentPage}&limit=${limit}`);
			const lastPage = Math.max(0, Math.ceil(nextResponse.total / limit) - 1);
			if (currentPage > lastPage) {
				currentPage = lastPage;
				updateUrl(filter, lastPage);
				return;
			}
			response = nextResponse;
		});
	}

	function setFilter(value: string) {
		filter = normalizedFilter(value);
		currentPage = 0;
		updateUrl(filter, 0);
	}

	function setPage(nextPage: number) {
		currentPage = Math.max(0, Math.min(nextPage, totalPages - 1));
		updateUrl(filter, currentPage);
	}

	$effect(() => {
		const nextFilter = normalizedFilter($pageStore.url.searchParams.get('status'));
		const nextPage = normalizedPage($pageStore.url.searchParams.get('page'));
		if (filter !== nextFilter) filter = nextFilter;
		if (currentPage !== nextPage) currentPage = nextPage;
	});

	$effect(() => {
		filter;
		currentPage;
		loadBooks();
	});

	const activeFilterLabel = $derived(filters.find((item) => item.value === filter)?.label ?? 'All');
	const totalPages = $derived(response ? Math.max(1, Math.ceil(response.total / limit)) : 1);
	const startIndex = $derived(response && response.total > 0 ? currentPage * limit + 1 : 0);
	const endIndex = $derived(response ? Math.min((currentPage + 1) * limit, response.total) : 0);
</script>

<svelte:head>
	<title>Finish — Bragi Books</title>
</svelte:head>

<PageHeader
	title="Library Catalog"
	description="Station #04 — paginated manifest of all library entries with status filters."
/>

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={loadBooks}>{dl.error}</Alert>
	</div>
{/if}

<!-- ── Station Identity ──────────────────────────────────────── -->
<div class="mb-4 flex items-center gap-3">
	<span class="font-mono text-xs tracking-widest text-[var(--text-muted)]">#04 FINISH</span>
	<div class="h-px flex-1 bg-[var(--border-subtle)]" aria-hidden="true"></div>
	{#if response}
		<span class="font-mono text-xs text-[var(--text-muted)]">{response.total} entries</span>
	{/if}
</div>

<!-- ── Status Filters — route bay selectors ──────────────────── -->
<div class="mb-6" role="group" aria-labelledby="catalog-filter-heading">
	<div class="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
		<h2 id="catalog-filter-heading" class="text-sm font-semibold text-[var(--text)]">Filter catalog</h2>
		<p class="text-xs text-[var(--text-muted)]">Showing {activeFilterLabel.toLowerCase()} books.</p>
	</div>
	<div class="flex flex-wrap gap-2">
		{#each filters as f}
			<button
				type="button"
				class="min-h-11 rounded-sm border border-[var(--border)] px-3 py-2 text-sm font-mono font-semibold tracking-wide transition-colors"
				class:bg-[var(--accent)]={filter === f.value}
				class:text-[var(--accent-text)]={filter === f.value}
				class:border-transparent={filter === f.value}
				class:bg-[var(--surface)]={filter !== f.value}
				class:text-[var(--text-muted)]={filter !== f.value}
				class:hover:bg-[var(--surface-hover)]={filter !== f.value}
				class:hover:border-[var(--border-subtle)]={filter !== f.value}
				onclick={() => setFilter(f.value)}
				aria-pressed={filter === f.value ? 'true' : 'false'}
			>
				{f.label}
			</button>
		{/each}
	</div>
</div>

<!-- ── Manifest List ─────────────────────────────────────────── -->
{#if response && !dl.showSkeleton}
	<p class="sr-only" role="status" aria-live="polite">
		{response.total === 0
			? `No ${activeFilterLabel.toLowerCase()} books found.`
			: `Showing ${startIndex} to ${endIndex} of ${response.total} ${activeFilterLabel.toLowerCase()} books.`}
	</p>
{/if}
{#if dl.showSkeleton}
	<div class="space-y-2" aria-label="Catalog loading">
		{#each Array(5) as _}
			<div class="card flex items-center gap-4">
				<Skeleton variant="rect" width="48px" height="64px" />
				<div class="flex-1">
					<Skeleton height="1rem" width="35%" />
					<Skeleton class="mt-2" height="0.875rem" width="20%" />
				</div>
			</div>
		{/each}
	</div>
{:else if !response || response.books.length === 0}
	<EmptyState
		title="No books found"
		description={filter === 'all' ? 'Import some source directories to build your library.' : `No ${filter} books right now.`}
		actionLabel="Import books"
		actionHref="/import"
	/>
{:else}
	<!-- Dense catalog manifest — book rows with explicit status -->
	<div class="space-y-2" role="list" aria-label="Book catalog manifest">
		{#each response.books as book (book.id)}
			<div class="card flex items-center gap-4" role="listitem">
				<!-- Cover thumbnail or placeholder -->
				{#if book.cover_image_url}
					<img
						src={book.cover_image_url}
						alt=""
						loading="lazy"
						decoding="async"
						class="h-16 w-12 flex-shrink-0 rounded-sm object-cover"
					/>
				{:else}
					<div class="flex h-16 w-12 flex-shrink-0 items-center justify-center rounded-sm bg-[var(--elevated)] text-xs text-[var(--text-muted)]">
						—
					</div>
				{/if}

				<!-- Book identity block -->
				<div class="min-w-0 flex-1">
					<h3 class="truncate font-medium" title={book.title}>{book.title}</h3>
					<p
						class="truncate text-sm text-[var(--text-muted)]"
						title={book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
					>
						{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						{#if book.series}
							<span class="text-[var(--text-muted)]"> · {book.series}</span>
						{/if}
						{#if book.runtime_length_minutes}
							<span class="text-[var(--text-muted)]"> · {formatRuntime(book.runtime_length_minutes)}</span>
						{/if}
					</p>
					{#if book.status_message}
						<p class="mt-0.5 truncate text-xs text-[var(--text-muted)]">{book.status_message}</p>
					{/if}
				</div>

				<!-- Status badge — text + shape + color -->
				<StatusBadge status={book.status} />
			</div>
		{/each}
	</div>

	<!-- ── Pagination — page bay controls ────────────────────── -->
	{#if totalPages > 1}
		<div class="mt-6 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<p class="text-sm text-[var(--text-muted)]">
				Showing <span class="font-mono">{startIndex}–{endIndex}</span> of
				<span class="font-mono">{response.total}</span>
			</p>
			<div class="flex items-center gap-2">
				<Button variant="secondary" disabled={currentPage === 0} onclick={() => setPage(currentPage - 1)}>
					← Previous
				</Button>
				<span class="font-mono text-xs text-[var(--text-muted)]">
					Page {currentPage + 1} of {totalPages}
				</span>
				<Button variant="secondary" disabled={currentPage >= totalPages - 1} onclick={() => setPage(currentPage + 1)}>
					Next →
				</Button>
			</div>
		</div>
	{/if}
{/if}