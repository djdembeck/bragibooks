<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, Button } from '$lib/components';
	import { formatRuntime } from '$lib/types';

	let totals = $state({ all: 0, done: 0, processing: 0, error: 0 });
	let recent: BookWithPeople[] = $state([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function load() {
		loading = true;
		error = null;
		try {
			const [all, done, processing, err, recentResp] = await Promise.all([
				get<BooksListResponse>('/books?limit=1'),
				get<BooksListResponse>('/books?status=done&limit=1'),
				get<BooksListResponse>('/books?status=processing&limit=1'),
				get<BooksListResponse>('/books?status=error&limit=1'),
				get<BooksListResponse>('/books?limit=5'),
			]);
			totals = {
				all: all.total,
				done: done.total,
				processing: processing.total,
				error: err.total,
			};
			recent = recentResp.books || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load dashboard';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	const statCards = $derived([
		{ label: 'Total books', value: totals.all },
		{ label: 'Done', value: totals.done, tone: 'done' as const },
		{ label: 'Processing', value: totals.processing, tone: 'processing' as const },
		{ label: 'Errors', value: totals.error, tone: 'error' as const },
	]);
</script>

<PageHeader title="Dashboard" description="Overview of your audiobook library and recent activity." />

{#if error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>{error}</Alert>
	</div>
{/if}

{#if loading}
	<div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
		{#each Array(4) as _}
			<div class="card">
				<Skeleton height="0.875rem" width="6rem" />
				<Skeleton class="mt-3" height="2rem" width="3rem" />
			</div>
		{/each}
	</div>
{:else}
	<div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
		{#each statCards as stat}
			<div class="card">
				<p class="text-sm font-medium text-[var(--text-muted)]">{stat.label}</p>
				<p class="mt-2 text-3xl font-semibold tracking-tight" class:text-[var(--success)]={stat.tone === 'done'} class:text-[var(--info)]={stat.tone === 'processing'} class:text-[var(--error)]={stat.tone === 'error'}>
					{stat.value}
				</p>
			</div>
		{/each}
	</div>
{/if}

<div class="mt-8 flex items-center justify-between gap-4">
	<h2 class="text-lg font-semibold">Recent books</h2>
	<div class="flex gap-2">
		<Button variant="secondary" href="/import">Import</Button>
		<Button variant="primary" href="/match">Match</Button>
	</div>
</div>

{#if loading}
	<div class="mt-4 space-y-2">
		{#each Array(3) as _}
			<div class="card flex items-center gap-4">
				<Skeleton variant="rect" width="48px" height="64px" />
				<div class="flex-1">
					<Skeleton height="1rem" width="40%" />
					<Skeleton class="mt-2" height="0.875rem" width="25%" />
				</div>
			</div>
		{/each}
	</div>
{:else if recent.length === 0}
	<div class="mt-4">
		<EmptyState
			title="No books yet"
			description="Import source directories and match them to metadata to get started."
			actionLabel="Import books"
			actionHref="/import"
		/>
	</div>
{:else}
	<div class="mt-4 space-y-2">
		{#each recent as book (book.id)}
			<a href="/books?status={book.status}" class="card flex items-center gap-4 no-underline transition-colors hover:bg-[var(--surface-hover)]">
				{#if book.cover_image_url}
					<img src={book.cover_image_url} alt="" class="h-16 w-12 flex-shrink-0 rounded object-cover" />
				{:else}
					<div class="flex h-16 w-12 flex-shrink-0 items-center justify-center rounded bg-[var(--elevated)] text-xs text-[var(--text-muted)]">—</div>
				{/if}
				<div class="min-w-0 flex-1">
					<h3 class="truncate font-medium">{book.title}</h3>
					<p class="truncate text-sm text-[var(--text-muted)]">
						{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						{#if book.runtime_length_minutes}
							<span class="text-[var(--text-muted)]"> · {formatRuntime(book.runtime_length_minutes)}</span>
						{/if}
					</p>
				</div>
				<StatusBadge status={book.status} />
			</a>
		{/each}
	</div>
{/if}
