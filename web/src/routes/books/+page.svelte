<script lang="ts">
	import { get, post } from '#lib/api.js';
	import { goto } from '$app/navigation';
	import { page as pageStore } from '$app/state';
	import type { BooksListResponse, BookWithPeople } from '#lib/types.js';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, ErrorDetail, Button, CoverArt } from '#lib/components/index.js';
	import { formatRuntime } from '#lib/types.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { toCleanedError, type CleanedAdbError } from '#lib/adbError.js';

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
	let q = $state('');
	let response = $state<BooksListResponse | null>(null);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	const validFilters = new Set(filters.map((item) => item.value));

	let searchInput = $state<HTMLInputElement | null>(null);
	// Uncontrolled-style input value; the debounce below commits it to `q`.
	let inputValue = $state('');

	// Select-all-matching selection state
	let allMatchedIds = $state<Set<number> | null>(null);
	let allMatchedTotal = $state(0);
	let selectingAll = $state(false);
	let bulkRequeueing = $state(false);
	let bulkError = $state<CleanedAdbError | null>(null);

	function normalizedFilter(value: string | null): string {
		return value && validFilters.has(value) ? value : 'all';
	}

	function normalizedPage(value: string | null): number {
		const parsed = Number(value);
		return Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
	}

	function updateUrl(nextFilter: string, nextPage: number, nextQ?: string) {
		const params = new URLSearchParams(pageStore.url.search);
		if (nextFilter === 'all') params.delete('status');
		else params.set('status', nextFilter);
		if (nextPage > 0) params.set('page', String(nextPage));
		else params.delete('page');
		if (nextQ !== undefined) {
			if (nextQ) params.set('q', nextQ);
			else params.delete('q');
		}
		const query = params.toString();
		void goto(`/books${query ? `?${query}` : ''}`, {
			replace: true,
			reset: false
		});
	}

	function buildBooksQuery(extra = '') {
		const params = new URLSearchParams();
		if (filter !== 'all') params.set('status', filter);
		if (q) params.set('q', q);
		params.set('page', String(currentPage));
		params.set('limit', String(limit));
		return `/books?${params.toString()}${extra}`;
	}

	async function loadBooks() {
		await dl.run(async () => {
			const nextResponse = await get<BooksListResponse>(buildBooksQuery());
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
		clearSelection(); // A new filter redefines "all matching" — the old selection is stale.
		updateUrl(filter, 0);
	}

	function setPage(nextPage: number) {
		currentPage = Math.max(0, Math.min(nextPage, totalPages - 1));
		updateUrl(filter, currentPage);
	}

	// Debounced search — plain timer in the input handler (not a reactive
	// effect, so it can't be re-armed by state churn). Fires 300ms after the
	// user stops typing: commit q, reset the page, and drive the ?q= nav.
	let searchTimer: ReturnType<typeof setTimeout> | null = null;
	function onSearchInput() {
		if (searchTimer) clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			const next = inputValue.trim();
			if (next === q) return;
			q = next;
			currentPage = 0;
			clearSelection(); // A new query redefines "all matching" — the old selection is stale.
			updateUrl(filter, 0, next);
		}, 300);
	}
	$effect(() => () => {
		if (searchTimer) clearTimeout(searchTimer);
	});

	// Select all matching books for the current filter + search
	async function selectAllMatching() {
		selectingAll = true;
		bulkError = null;
		try {
			const params = new URLSearchParams();
			if (filter !== 'all') params.set('status', filter);
			if (q) params.set('q', q);
			params.set('all_ids', '1');
			const res = await get<{ ids: number[] }>(`/books?${params.toString()}`);
			if (!res || !Array.isArray(res.ids)) throw new Error('Could not load matching book IDs.');
			allMatchedIds = new Set(res.ids);
			allMatchedTotal = res.ids.length;
		} catch (e) {
			bulkError = toCleanedError(e);
			allMatchedIds = null;
		} finally {
			selectingAll = false;
		}
	}

	function clearSelection() {
		allMatchedIds = null;
		allMatchedTotal = 0;
		bulkError = null;
	}

	async function bulkRequeue() {
		if (!allMatchedIds || allMatchedIds.size === 0) return;
		bulkRequeueing = true;
		bulkError = null;
		try {
			const ids = [...allMatchedIds];
			// One POST whose src_dirs are parallel to book_ids: each book
			// requeues from its own recorded source path, fetched per book.
			const books = await Promise.all(
				ids.map((id) => get<BookWithPeople>(`/books/${id}`)),
			);
			await post(`/process`, { book_ids: ids, src_dirs: books.map((b) => b.src_path) });
			await goto('/process');
		} catch (e) {
			bulkError = toCleanedError(e);
		} finally {
			bulkRequeueing = false;
		}
	}

	// Pagination jump
	let jumpValue = $state('');
	function jumpToPage() {
		const n = Number(jumpValue);
		if (!Number.isInteger(n) || n < 1) return;
		setPage(n - 1);
		jumpValue = '';
	}

	// Keyboard: j/k page, '/' focus search. Ignore inputs and open modals.
	function onKeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement | null;
		if (target) {
			const tag = target.tagName;
			if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || tag === 'DIALOG') return;
			if (target.isContentEditable) return;
		}
		if (document.querySelector('dialog[open]')) return;
		if (e.metaKey || e.ctrlKey || e.altKey) return;
		if (e.key === 'j') {
			setPage(currentPage + 1);
		} else if (e.key === 'k') {
			setPage(currentPage - 1);
		} else if (e.key === '/') {
			e.preventDefault();
			searchInput?.focus();
		}
	}

	$effect(() => {
		const nextFilter = normalizedFilter(pageStore.url.searchParams.get('status'));
		const nextPage = normalizedPage(pageStore.url.searchParams.get('page'));
		const nextQ = pageStore.url.searchParams.get('q') ?? '';
		if (filter !== nextFilter) filter = nextFilter;
		if (currentPage !== nextPage) currentPage = nextPage;
		if (q !== nextQ) q = nextQ;
		// Mirror the search term into the input field when the URL changes from
		// outside (back/forward, deep link). Gate on focus: while the user is
		// typing (input focused) we never touch the field, which keeps the
		// in-progress value authoritative and avoids racing the debounce nav.
		if (searchInput && document.activeElement !== searchInput && inputValue !== nextQ) {
			inputValue = nextQ;
		}
	});

	$effect(() => {
		filter;
		currentPage;
		q;
		loadBooks();
	});

	const activeFilterLabel = $derived(filters.find((item) => item.value === filter)?.label ?? 'All');
	const totalPages = $derived(response ? Math.max(1, Math.ceil(response.total / limit)) : 1);
	const startIndex = $derived(response && response.total > 0 ? currentPage * limit + 1 : 0);
	const endIndex = $derived(response ? Math.min((currentPage + 1) * limit, response.total) : 0);
</script>

<svelte:head>
	<title>Books — Bragi Books</title>
</svelte:head>

<svelte:window onkeydown={onKeydown} />

<PageHeader
	title="Books"
	description="Every book in your library, with status filters and bulk requeue."
/>

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={loadBooks}>
			{dl.error.message}
			<ErrorDetail raw={dl.error.raw} needsSettings={dl.error.needsSettings} />
		</Alert>
	</div>
{/if}

<!-- ── Status filters ────────────────────────────────────────── -->
<div class="mb-6" role="group" aria-labelledby="catalog-filter-heading">
	<div class="mb-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
		<h2 id="catalog-filter-heading" class="text-sm font-semibold text-[var(--text)]">Filter</h2>
		<div class="flex flex-wrap items-center gap-2">
			<input
				bind:this={searchInput}
				bind:value={inputValue}
				type="search"
				placeholder="Title or series…"
				class="min-h-11 w-full sm:w-64"
				aria-label="Search catalog"
				oninput={onSearchInput}
			/>
			{#if response && response.total > 0}
				<Button
					variant="secondary"
					loading={selectingAll}
					onclick={() => (allMatchedIds ? clearSelection() : selectAllMatching())}
				>
					{allMatchedIds ? 'Clear selection' : `Select all ${response.total}`}
				</Button>
			{/if}
		</div>
	</div>
	<div class="flex flex-wrap gap-2">
		{#each filters as f}
			<button
				type="button"
				class="min-h-11 rounded-sm border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-sm font-medium transition-colors hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
				class:border-[var(--border-strong)]={filter === f.value}
				class:bg-[var(--surface)]={filter === f.value}
				class:text-[var(--text)]={filter === f.value}
				class:text-[var(--text-muted)]={filter !== f.value}
				onclick={() => setFilter(f.value)}
				aria-pressed={filter === f.value ? 'true' : 'false'}
			>
				{f.label}
			</button>
		{/each}
	</div>

	<!-- ── Bulk requeue bar ──────────────────────────────────── -->
	{#if allMatchedIds}
		<div class="panel mt-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 p-3" role="region" aria-label="Bulk requeue" aria-live="polite">
			<p class="text-sm text-[var(--text)]">
				<span class="font-mono font-semibold">{allMatchedTotal}</span> books selected for bulk requeue
				<span class="text-xs text-[var(--text-muted)]">· {activeFilterLabel.toLowerCase()}{q ? ` · “${q}”` : ''}</span>
			</p>
			<div class="flex items-center gap-2">
				<Button loading={bulkRequeueing} onclick={bulkRequeue}>Requeue selected</Button>
				<Button variant="ghost" disabled={bulkRequeueing} onclick={clearSelection}>Clear</Button>
			</div>
		</div>
	{/if}
	{#if bulkError}
		<div class="mt-3">
			<Alert variant="error" onretry={selectAllMatching}>
				{bulkError.message}
				<ErrorDetail raw={bulkError.raw} needsSettings={bulkError.needsSettings} />
			</Alert>
		</div>
	{/if}
</div>

<!-- ── Catalog list ──────────────────────────────────────────── -->
{#if response && !dl.showSkeleton}
	<p class="sr-only" role="status" aria-live="polite">
		{response.total === 0
			? `No ${activeFilterLabel.toLowerCase()} books found.`
			: `Showing ${startIndex} to ${endIndex} of ${response.total} ${activeFilterLabel.toLowerCase()} books.`}
	</p>
{/if}
{#if dl.showSkeleton}
	<div class="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]" aria-label="Catalog loading">
		{#each Array(5) as _}
			<div class="flex items-center gap-4 border-b border-[var(--border-subtle)] px-4 py-3 last:border-b-0">
				<Skeleton variant="rect" width="48px" height="48px" />
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
	<!-- Catalog list — whole row links to the book record -->
	<ul class="divide-y divide-[var(--border-subtle)] overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]" aria-label="Book catalog">
		{#each response.books as book (book.id)}
			<li>
				<a
					href="/books/{book.id}"
					class="flex items-center gap-4 px-4 py-3 no-underline transition-colors hover:bg-[var(--surface-hover)]"
					aria-label="{book.title} — open record"
				>
				<CoverArt url={book.cover_image_url} title={book.title} size="sm" />

				<div class="min-w-0 flex-1">
					<h3 class="line-clamp-2 sm:truncate font-medium" title={book.title}>{book.title}</h3>
					<p
						class="truncate text-sm text-[var(--text-muted)]"
						title={book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
					>
						{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						{#if book.series}
							<span class="text-[var(--text-muted)]"> · {book.series}</span>
						{/if}
						{#if book.runtime_length_minutes}
							<span class="text-[var(--text-secondary)]"> · {formatRuntime(book.runtime_length_minutes)}</span>
						{/if}
					</p>
					{#if book.status_message}
						<p class="mt-0.5 truncate text-xs text-[var(--text-muted)]">{book.status_message}</p>
					{/if}
				</div>

				<StatusBadge status={book.status} />
				</a>
			</li>
		{/each}
	</ul>

	<!-- ── Pagination ────────────────────────────────────────── -->
	{#if totalPages > 1}
		<div class="mt-6 flex flex-wrap flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<p class="text-sm text-[var(--text-muted)]">
				Showing <span class="font-mono">{startIndex}–{endIndex}</span> of
				<span class="font-mono">{response.total}</span>
			</p>
			<div class="flex flex-wrap items-center gap-2">
				<Button variant="secondary" disabled={currentPage === 0} onclick={() => setPage(currentPage - 1)}>
					← Previous
				</Button>
				<span class="font-mono text-xs text-[var(--text-muted)]">
					Page {currentPage + 1} of {totalPages}
				</span>
				<Button variant="secondary" disabled={currentPage >= totalPages - 1} onclick={() => setPage(currentPage + 1)}>
					Next →
				</Button>
				<div class="ml-2 flex items-center gap-1">
					<input
						bind:value={jumpValue}
						type="number"
						min={1}
						max={totalPages}
						aria-label="Jump to page"
						class="w-16 font-mono text-sm"
						onkeydown={(e) => {
							if (e.key === 'Enter') jumpToPage();
						}}
					/>
					<Button variant="secondary" onclick={jumpToPage}>Go</Button>
				</div>
			</div>
		</div>
	{/if}
{/if}
