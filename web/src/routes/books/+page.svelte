<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, Button } from '$lib/components';
	import { formatRuntime } from '$lib/types';

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
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function loadBooks() {
		loading = true;
		error = null;
		try {
			const statusParam = filter === 'all' ? '' : `status=${filter}&`;
			response = await get<BooksListResponse>(`/books?${statusParam}page=${page}&limit=${limit}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load books';
		} finally {
			loading = false;
		}
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

<PageHeader title="Books" description="Browse, filter, and review all books in your library." />

{#if error}
	<div class="mb-6">
		<Alert variant="error" onretry={loadBooks}>{error}</Alert>
	</div>
{/if}

<div class="mb-6 flex flex-wrap gap-2">
	{#each filters as f}
		<button
			type="button"
			class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
			class:bg-[var(--accent)]={filter === f.value}
			class:text-[var(--accent-text)]={filter === f.value}
			class:bg-[var(--surface)]={filter !== f.value}
			class:text-[var(--text-secondary)]={filter !== f.value}
			class:hover:bg-[var(--surface-hover)]={filter !== f.value}
			onclick={() => setFilter(f.value)}
		>
			{f.label}
		</button>
	{/each}
</div>

{#if loading}
	<div class="space-y-2">
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
	<div class="space-y-2">
		{#each response.books as book (book.id)}
			<div class="card flex items-center gap-4">
				{#if book.cover_image_url}
					<img src={book.cover_image_url} alt="" class="h-16 w-12 flex-shrink-0 rounded object-cover" />
				{:else}
					<div class="flex h-16 w-12 flex-shrink-0 items-center justify-center rounded bg-[var(--elevated)] text-xs text-[var(--text-muted)]">—</div>
				{/if}
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
				<StatusBadge status={book.status} />
			</div>
		{/each}
	</div>

	{#if totalPages > 1}
		<div class="mt-6 flex items-center justify-between">
			<p class="text-sm text-[var(--text-muted)]">Showing {startIndex}–{endIndex} of {response.total}</p>
			<div class="flex items-center gap-2">
				<Button variant="secondary" disabled={page === 0} onclick={() => page--}>Previous</Button>
				<Button variant="secondary" disabled={page >= totalPages - 1} onclick={() => page++}>Next</Button>
			</div>
		</div>
	{/if}
{/if}
