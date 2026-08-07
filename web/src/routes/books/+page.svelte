<script lang="ts">
	import { get } from '$lib/api';
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
	let page = $state(0);
	let response = $state<BooksListResponse | null>(null);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	async function loadBooks() {
		await dl.run(async () => {
			const statusParam = filter === 'all' ? '' : `status=${filter}&`;
			response = await get<BooksListResponse>(`/books?${statusParam}page=${page}&limit=${limit}`);
		});
	}

	function setFilter(value: string) {
		filter = value;
		page = 0;
	}

	$effect(() => {
		loadBooks();
	});

	const totalPages = $derived(response ? Math.max(1, Math.ceil(response.total / limit)) : 1);
	const startIndex = $derived(response ? page * limit + 1 : 0);
	const endIndex = $derived(response ? Math.min((page + 1) * limit, response.total) : 0);
</script>

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
<div class="mb-6 flex flex-wrap gap-2" role="group" aria-label="Filter by status">
	{#each filters as f}
		<button
			type="button"
			class="rounded-sm border border-[var(--border)] px-2.5 py-1 text-xs font-mono font-semibold tracking-wide transition-colors sm:text-sm"
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
			{f.label.toUpperCase()}
		</button>
	{/each}
</div>

<!-- ── Manifest List ─────────────────────────────────────────── -->
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
						class="h-16 w-12 flex-shrink-0 rounded-sm object-cover"
					/>
				{:else}
					<div class="flex h-16 w-12 flex-shrink-0 items-center justify-center rounded-sm bg-[var(--elevated)] text-xs text-[var(--text-muted)]">
						—
					</div>
				{/if}

				<!-- Book identity block -->
				<div class="min-w-0 flex-1">
					<h3 class="truncate font-medium">{book.title}</h3>
					<p class="truncate text-sm text-[var(--text-muted)]">
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
				<Button variant="secondary" disabled={page === 0} onclick={() => page--}>
					← Previous
				</Button>
				<span class="font-mono text-xs text-[var(--text-muted)]">
					Page {page + 1} of {totalPages}
				</span>
				<Button variant="secondary" disabled={page >= totalPages - 1} onclick={() => page++}>
					Next →
				</Button>
			</div>
		</div>
	{/if}
{/if}