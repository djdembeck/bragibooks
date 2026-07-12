<script lang="ts">
	import { get, post } from '$lib/api';
	import { streamEvents } from '$lib/api';
	import type { BookWithPeople, BooksListResponse, ProcessingResponse, ProcessingJob } from '$lib/types';
	import { PageHeader, Button, Alert, Skeleton, EmptyState, StatusBadge } from '$lib/components';

	interface JobItem {
		id: string;
		bookId: number;
		status: string;
		output: string;
		error: string | null;
	}

	let books: BookWithPeople[] = $state([]);
	let selectedIds = $state<Set<number>>(new Set());
	let loading = $state(true);
	let starting = $state(false);
	let error = $state<string | null>(null);
	let jobs = $state<JobItem[]>([]);
	let cleanupFns: (() => void)[] = [];

	async function loadBooks() {
		loading = true;
		error = null;
		try {
			const [matched, pending] = await Promise.all([
				get<BooksListResponse>('/books?status=matched&limit=200'),
				get<BooksListResponse>('/books?status=pending&limit=200'),
			]);
			books = [...(matched.books || []), ...(pending.books || [])];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load books';
		} finally {
			loading = false;
		}
	}

	function toggleBook(id: number) {
		const next = new Set(selectedIds);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selectedIds = next;
	}

	function toggleSelectAll() {
		const allSelected = books.length > 0 && books.every((b) => selectedIds.has(b.id));
		const next = new Set(selectedIds);
		books.forEach((b) => {
			if (allSelected) next.delete(b.id);
			else next.add(b.id);
		});
		selectedIds = next;
	}

	async function start() {
		if (selectedIds.size === 0) return;
		starting = true;
		error = null;
		try {
			const selected = books.filter((b) => selectedIds.has(b.id));
			const response = await post<ProcessingResponse>('/process', {
				book_ids: selected.map((b) => b.id),
				src_dirs: selected.map((b) => b.src_path)
			});
			for (const job of response.jobs) {
				addJob(job.id, job.book_id);
			}
			selectedIds = new Set();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to start processing';
		} finally {
			starting = false;
		}
	}

	function addJob(id: string, bookId: number) {
		const item: JobItem = { id, bookId, status: 'queued', output: '', error: null };
		jobs = [item, ...jobs];
		const stop = streamEvents(
			`/api/jobs/${id}/stream`,
			(msg) => {
				item.output += msg + '\n';
				jobs = jobs;
			},
			(err) => {
				pollJob(id);
				if (err) {
					item.error = err.message;
					jobs = jobs;
				}
			}
		);
		cleanupFns.push(stop);
	}

	async function pollJob(id: string) {
		try {
			const job = await get<ProcessingJob>(`/jobs/${id}`);
			const item = jobs.find((j) => j.id === id);
			if (item) {
				item.status = job.status;
				item.error = job.error;
				if (job.output) item.output = job.output;
				jobs = jobs;
			}
		} catch {
			/* ignore polling errors */
		}
	}

	$effect(() => {
		loadBooks();
		return () => {
			cleanupFns.forEach((fn) => fn());
		};
	});

	const allSelected = $derived(books.length > 0 && books.every((b) => selectedIds.has(b.id)));
</script>

<PageHeader title="Processing" description="Queue matched books for m4b-merge and monitor progress." />

{#if error}
	<div class="mb-6">
		<Alert variant="error" onretry={loadBooks}>{error}</Alert>
	</div>
{/if}

{#if loading}
	<div class="space-y-2">
		{#each Array(4) as _}
			<Skeleton height="3rem" />
		{/each}
	</div>
{:else if books.length === 0}
	<EmptyState
		title="Nothing ready to process"
		description="Match pending books to metadata first, then queue them here."
		actionLabel="Go to Match"
		actionHref="/match"
	/>
{:else}
	<div class="card mb-6">
		<div class="mb-4 flex items-center justify-between">
			<div class="flex items-center gap-3">
				<input type="checkbox" checked={allSelected} onchange={toggleSelectAll} aria-label="Select all" />
				<span class="text-sm font-medium text-[var(--text-secondary)]">{selectedIds.size} selected</span>
			</div>
			<Button variant="primary" loading={starting} disabled={selectedIds.size === 0} onclick={start}>
				Start processing
			</Button>
		</div>

		<div class="space-y-2">
			{#each books as book (book.id)}
				<div class="flex items-center gap-3 rounded-lg border border-[var(--border-subtle)] p-3 hover:bg-[var(--surface-hover)]">
					<input type="checkbox" checked={selectedIds.has(book.id)} onchange={() => toggleBook(book.id)} aria-label="Select {book.title}" />
					{#if book.cover_image_url}
						<img src={book.cover_image_url} alt="" class="h-14 w-10 rounded object-cover" />
					{:else}
						<div class="flex h-14 w-10 items-center justify-center rounded bg-[var(--elevated)] text-xs text-[var(--text-muted)]">—</div>
					{/if}
					<div class="min-w-0 flex-1">
						<p class="truncate font-medium">{book.title}</p>
						<p class="truncate text-xs text-[var(--text-muted)]">
							{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						</p>
					</div>
					<StatusBadge status={book.status} />
				</div>
			{/each}
		</div>
	</div>
{/if}

{#if jobs.length > 0}
	<h2 class="mb-3 text-lg font-semibold">Active jobs</h2>
	<div class="space-y-3">
		{#each jobs as job (job.id)}
			<div class="card">
				<div class="mb-2 flex items-center justify-between">
					<p class="font-mono text-sm text-[var(--text-secondary)]">{job.id.slice(0, 8)}</p>
					<StatusBadge status={job.status} />
				</div>
				{#if job.output}
					<pre class="max-h-64 overflow-auto rounded-md bg-[var(--bg)] p-3 text-xs text-[var(--text-secondary)]">{job.output}</pre>
				{:else}
					<p class="text-sm text-[var(--text-muted)]">Waiting for output…</p>
				{/if}
				{#if job.error}
					<p class="mt-2 text-sm text-[var(--error)]">{job.error}</p>
				{/if}
			</div>
		{/each}
	</div>
{/if}
