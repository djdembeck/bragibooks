<script lang="ts">
	import { get, post, streamEvents } from '$lib/api';
	import type { BookWithPeople, BooksListResponse, ProcessingResponse, ProcessingJob, JobsListResponse } from '$lib/types';
	import { PageHeader, Button, Alert, Skeleton, EmptyState, StatusBadge } from '$lib/components';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';
	import { CheckCircle, Clock, AlertTriangle } from '@lucide/svelte';

	interface JobItem {
		id: string;
		bookId: number | null;
		status: string;
		output: string;
		error: string | null;
	}

	let books: BookWithPeople[] = $state([]);
	let selectedIds = $state<Set<number>>(new Set());
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let starting = $state(false);
	let jobs = $state<JobItem[]>([]);

	// SSE stream cleanup map — keyed by job id
	const cleanupMap = $state(new Map<string, () => void>());
	// Polling interval handles for active jobs
	const pollTimers = $state(new Map<string, ReturnType<typeof setInterval>>());

	$effect(() => {
		load();
		return () => {
			// Clean up all SSE streams
			cleanupMap.forEach((fn) => fn());
			// Clear all polling timers
			pollTimers.forEach((timer) => clearInterval(timer));
		};
	});

	async function load() {
		await dl.run(async () => {
			const [matched, pending, jobsResp] = await Promise.all([
				get<BooksListResponse>('/books?status=matched&limit=200'),
				get<BooksListResponse>('/books?status=pending&limit=200'),
				get<JobsListResponse>('/jobs?limit=20'),
			]);
			books = [...(matched.books || []), ...(pending.books || [])];
			restoreJobs(jobsResp.jobs);
		});
	}

	async function loadBooks() {
		try {
			const [matched, pending] = await Promise.all([
				get<BooksListResponse>('/books?status=matched&limit=200'),
				get<BooksListResponse>('/books?status=pending&limit=200'),
			]);
			books = [...(matched.books || []), ...(pending.books || [])];
		} catch {
			// Ignore — don't disrupt existing jobs
		}
	}

	function restoreJobs(jobList: ProcessingJob[]) {
		// Clear old state
		cleanupMap.forEach((fn) => fn());
		pollTimers.forEach((timer) => clearInterval(timer));
		cleanupMap.clear();
		pollTimers.clear();

		const items: JobItem[] = [];
		for (const j of jobList) {
			const item: JobItem = {
				id: j.id,
				bookId: j.book_id,
				status: j.status,
				output: j.output || '',
				error: j.error || null,
			};
			items.push(item);

			// Reconnect SSE for active (queued/running) jobs
			if (j.status === 'queued' || j.status === 'running') {
				connectSSE(item);
			} else {
				// Terminal jobs: poll for state sync
				startPolling(item);
			}
		}
		jobs = items;
	}

	function connectSSE(item: JobItem) {
		const stop = streamEvents(
			`/api/jobs/${item.id}/stream`,
			(raw) => {
				try {
					const parsed = JSON.parse(raw);
					if (parsed.status) {
						item.status = parsed.status;
					}
					if (parsed.output) {
						item.output = parsed.output;
					}
					if (parsed.message && !parsed.output) {
						item.output += parsed.message + '\n';
					}
					if (parsed.error) {
						item.error = parsed.error;
					}
					if (parsed.status === 'done' || parsed.status === 'error') {
						// Terminal transition — stop SSE, start polling, refresh books
						stop();
						cleanupMap.delete(item.id);
						startPolling(item);
						refreshAfterTerminal(item.id);
					}
					jobs = jobs;
				} catch {
					// Not JSON — treat as raw output line
					item.output += raw + '\n';
					jobs = jobs;
				}
			},
			() => {
				// SSE connection lost — fall back to polling
				stop();
				cleanupMap.delete(item.id);
				pollJob(item);
				startPolling(item);
			}
		);
		cleanupMap.set(item.id, stop);
	}

	function startPolling(item: JobItem) {
		stopPolling(item.id);
		const timer = setInterval(() => pollJob(item), 5000);
		pollTimers.set(item.id, timer);
	}

	async function pollJob(item: JobItem) {
		try {
			const job = await get<ProcessingJob>(`/jobs/${item.id}`);
			if (job.status !== item.status || job.error !== item.error || (job.output && job.output !== item.output)) {
				item.status = job.status;
				item.error = job.error;
				if (job.output) item.output = job.output;
				jobs = jobs;
			}
			// If job moved to terminal, stop polling and refresh
			if (job.status === 'done' || job.status === 'error') {
				stopPolling(item.id);
				refreshAfterTerminal(item.id);
			}
		} catch {
			/* ignore polling errors */
		}
	}

	function stopPolling(id: string) {
		const timer = pollTimers.get(id);
		if (timer) clearInterval(timer);
		pollTimers.delete(id);
	}

	async function refreshAfterTerminal(jobId: string) {
		const item = jobs.find((j) => j.id === jobId);
		if (item) {
			// Stop any existing SSE for this job
			const existing = cleanupMap.get(jobId);
			if (existing) {
				existing();
				cleanupMap.delete(jobId);
			}
		}
		await loadBooks();
	}

	function toggleBook(id: number) {
		const next = new Set(selectedIds);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selectedIds = next;
	}

	async function start() {
		if (selectedIds.size === 0) return;
		starting = true;
		dl.setError(null);
		try {
			const selected = books.filter((b) => selectedIds.has(b.id));
			const response = await post<ProcessingResponse>('/process', {
				book_ids: selected.map((b) => b.id),
				src_dirs: selected.map((b) => b.src_path)
			});
			for (const job of response.jobs) {
				const item: JobItem = {
					id: job.id,
					bookId: job.book_id,
					status: 'queued',
					output: '',
					error: null,
				};
				jobs = [item, ...jobs];
				connectSSE(item);
			}
			selectedIds = new Set();
			// Refresh books to pick up status=processing books removed from matched list
			await loadBooks();
		} catch (e) {
			dl.setError(e instanceof Error ? e.message : 'Failed to start processing');
		} finally {
			starting = false;
		}
	}

	const matchedBooks = $derived(books.filter((b) => b.status === 'matched'));
	const pendingBooks = $derived(books.filter((b) => b.status === 'pending'));

	function jobStatusLabel(status: string): string {
		switch (status.toLowerCase()) {
			case 'queued': return 'queued — waiting for a slot';
			case 'running': return 'running — m4b-merge active';
			case 'done':
			case 'completed': return 'done — merged successfully';
			case 'error':
			case 'failed': return 'error — job failed';
			default: return status;
		}
	}
</script>

<PageHeader
	title="Queue"
	station="#03 · QUEUE"
	description="Queue matched books for m4b-merge and monitor job progress."
/>

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>{dl.error}</Alert>
	</div>
{/if}

{#if dl.showSkeleton}
	<div class="space-y-2">
		{#each Array(4) as _}
			<Skeleton height="3rem" />
		{/each}
	</div>
{:else if matchedBooks.length > 0}
	<!-- Intake Bay: matched (ready) books -->
	<div class="bay mb-6">
		<div class="bay__header">
			<div class="bay__title">
				<span class="bay__indicator bay__indicator--ready" aria-hidden="true"></span>
				<span>Intake Bay — Ready</span>
				<span class="bay__count">{matchedBooks.length}</span>
			</div>
			<p class="bay__hint">Metadata confirmed. Select and queue for processing.</p>
		</div>

		<div class="bay__controls">
			<div class="flex items-center gap-3">
				<input
					type="checkbox"
					checked={matchedBooks.every((b) => selectedIds.has(b.id)) && matchedBooks.length > 0}
					onchange={() => {
						const next = new Set(selectedIds);
						matchedBooks.forEach((b) => {
							if (next.has(b.id)) next.delete(b.id);
							else next.add(b.id);
						});
						selectedIds = next;
					}}
					aria-label="Select all matched books"
				/>
				<span class="text-sm text-[var(--text-secondary)] font-mono">
					{selectedIds.size} selected of {matchedBooks.length}
				</span>
			</div>
			<Button
				variant="primary"
				loading={starting}
				disabled={selectedIds.size === 0}
				onclick={start}
			>
				{#if starting}
					<SpinnerIcon />
				{/if}
				Start processing
			</Button>
		</div>

		<div class="bay__manifest">
			{#each matchedBooks as book (book.id)}
				<div class="manifest-row manifest-row--ready">
					<div class="manifest-row__select">
						<input
							type="checkbox"
							checked={selectedIds.has(book.id)}
							onchange={() => toggleBook(book.id)}
							aria-label="Select {book.title} for processing"
						/>
					</div>
					<div class="manifest-row__cover">
						{#if book.cover_image_url}
							<img src={book.cover_image_url} alt="" class="manifest-row__cover-img" />
						{:else}
							<div class="manifest-row__cover-placeholder" aria-hidden="true">—</div>
						{/if}
					</div>
					<div class="manifest-row__info">
						<p class="manifest-row__title">{book.title}</p>
						<p class="manifest-row__meta">
							{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						</p>
					</div>
					<div class="manifest-row__status">
						<StatusBadge status={book.status} />
						<span class="manifest-row__status-text" aria-label="Ready — metadata confirmed">
							<CheckCircle class="h-3.5 w-3.5" aria-hidden="true" />
							ready
						</span>
					</div>
				</div>
			{/each}
		</div>
	</div>
{/if}

<!-- Pending Bay: unmatched books (not selectable) -->
{#if pendingBooks.length > 0}
	<div class="bay bay--dim mb-6">
		<div class="bay__header">
			<div class="bay__title">
				<span class="bay__indicator bay__indicator--pending" aria-hidden="true"></span>
				<span>Awaiting Match — Not Ready</span>
				<span class="bay__count">{pendingBooks.length}</span>
			</div>
			<p class="bay__hint">These books need metadata matched before they can enter the queue.</p>
		</div>

		<div class="bay__manifest">
			{#each pendingBooks as book (book.id)}
				<div class="manifest-row manifest-row--pending">
					<div class="manifest-row__select manifest-row__select--locked" aria-hidden="true">
						<LockPlaceholder />
					</div>
					<div class="manifest-row__cover">
						{#if book.cover_image_url}
							<img src={book.cover_image_url} alt="" class="manifest-row__cover-img" />
						{:else}
							<div class="manifest-row__cover-placeholder" aria-hidden="true">—</div>
						{/if}
					</div>
					<div class="manifest-row__info">
						<p class="manifest-row__title">{book.title}</p>
						<p class="manifest-row__meta">
							{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						</p>
					</div>
					<div class="manifest-row__status">
						<StatusBadge status={book.status} />
						<span class="manifest-row__status-text manifest-row__status-text--pending" aria-label="Awaiting match — not yet ready">
							<Clock class="h-3.5 w-3.5" aria-hidden="true" />
							awaiting match
						</span>
					</div>
				</div>
			{/each}
		</div>
	</div>
{/if}

<!-- Empty state: no books AND no jobs -->
{#if matchedBooks.length === 0 && pendingBooks.length === 0 && jobs.length === 0}
	<EmptyState
		title="Nothing in the queue bay"
		description="Match pending books to metadata first, then they appear here for processing."
		actionLabel="Go to Match"
		actionHref="/match"
	/>
{/if}

<!-- Job Bay: active and recent jobs — shown independently of book count -->
{#if jobs.length > 0}
	<div class="bay bay--jobs mt-6">
		<div class="bay__header">
			<div class="bay__title">
				<span class="bay__indicator bay__indicator--active" aria-hidden="true"></span>
				<span>Job Bay</span>
				<span class="bay__count">{jobs.length}</span>
			</div>
			<p class="bay__hint">Live output from m4b-merge jobs. SSE streams with polling fallback.</p>
		</div>

		<div class="job-grid">
			{#each jobs as job (job.id)}
				<div class="job-cell">
					<div class="job-cell__header">
						<div class="job-cell__identity">
							<span class="job-cell__id" title={job.id}>{job.id.slice(0, 8)}</span>
							<span class="job-cell__bookid">book #{job.bookId ?? '?'}</span>
						</div>
						<div class="job-cell__status">
							<StatusBadge status={job.status} />
							<span class="job-cell__status-text" aria-label="Job status: {jobStatusLabel(job.status)}">
								{#if job.status.toLowerCase() === 'running'}
									<SpinnerIcon class="h-3.5 w-3.5" />
								{:else if job.status.toLowerCase() === 'error' || job.status.toLowerCase() === 'failed'}
									<AlertTriangle class="h-3.5 w-3.5" />
								{:else if job.status.toLowerCase() === 'done' || job.status.toLowerCase() === 'completed'}
									<CheckCircle class="h-3.5 w-3.5" />
								{:else}
									<Clock class="h-3.5 w-3.5" />
								{/if}
								{jobStatusLabel(job.status)}
							</span>
						</div>
					</div>

					{#if job.output}
						<div class="job-cell__output" role="log" aria-label="Job {job.id.slice(0, 8)} output" aria-live="polite">
							{job.output}
						</div>
					{:else}
						<p class="job-cell__waiting">Waiting for output…</p>
					{/if}

					{#if job.error}
						<div class="job-cell__error" role="alert">
							<AlertTriangle class="h-3.5 w-3.5" aria-hidden="true" />
							{job.error}
						</div>
					{/if}
				</div>
			{/each}
		</div>
	</div>
{/if}

<svelte:head>
	<title>Queue — Bragi Books</title>
</svelte:head>

<style>
	/* Bay — a containment area (intake, pending, jobs) */
	.bay {
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		overflow: hidden;
		background: var(--surface);
	}

	.bay--dim {
		opacity: 0.7;
	}

	.bay--jobs {
		border-color: var(--border-subtle);
	}

	.bay__header {
		padding: 0.875rem 1rem;
		border-bottom: 1px solid var(--border-subtle);
		background: var(--surface-hover);
	}

	.bay__title {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-weight: 600;
		font-size: 0.9rem;
		color: var(--text);
	}

	.bay__count {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.8rem;
		font-weight: 600;
		padding: 0.1rem 0.5rem;
		background: var(--elevated);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
	}

	.bay__hint {
		margin-top: 0.25rem;
		font-size: 0.8rem;
		color: var(--text-muted);
	}

	/* Rail indicators — small colored dots for bay status */
	.bay__indicator {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.bay__indicator--ready {
		background: var(--success);
		box-shadow: 0 0 0 3px var(--success-bg);
	}

	.bay__indicator--pending {
		background: var(--text-muted);
	}

	.bay__indicator--active {
		background: var(--info);
		box-shadow: 0 0 0 3px var(--info-bg);
	}

	/* Bay controls — select all + action button */
.bay__controls {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.75rem 1rem;
		border-bottom: 1px dashed var(--border-subtle);
		background: var(--surface);
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	/* Manifest — the dense list of book rows */
	.bay__manifest {
		border-top: 1px solid var(--border-subtle);
	}

	.manifest-row {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.6rem 1rem;
		border-bottom: 1px solid var(--border-subtle);
		transition: background 100ms var(--ease-out);
	}

	.manifest-row:last-child {
		border-bottom: none;
	}

	.manifest-row--ready:hover {
		background: var(--surface-hover);
	}

	.manifest-row--pending {
		background: var(--surface);
	}

	.manifest-row__select {
		flex-shrink: 0;
	}

	.manifest-row__select--locked {
		opacity: 0.3;
	}

	.manifest-row__cover {
		flex-shrink: 0;
	}

	.manifest-row__cover-img {
		height: 3.5rem;
		width: 2.5rem;
		border-radius: var(--radius-sm);
		object-fit: cover;
		border: 1px solid var(--border-subtle);
	}

	.manifest-row__cover-placeholder {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 3.5rem;
		width: 2.5rem;
		border-radius: var(--radius-sm);
		background: var(--elevated);
		font-size: 0.75rem;
		color: var(--text-muted);
		border: 1px solid var(--border-subtle);
	}

	.manifest-row__info {
		min-width: 0;
		flex: 1;
	}

	.manifest-row__title {
		font-weight: 500;
		font-size: 0.875rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--text);
	}

	.manifest-row__meta {
		font-size: 0.75rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--text-muted);
		margin-top: 0.1rem;
	}

	.manifest-row__status {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.manifest-row__status-text {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.75rem;
		color: var(--success);
		font-weight: 500;
	}

	.manifest-row__status-text--pending {
		color: var(--text-muted);
	}

	/* Job grid */
	.job-grid {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.job-cell {
		padding: 0.875rem 1rem;
		border-bottom: 1px solid var(--border-subtle);
	}

	.job-cell:last-child {
		border-bottom: none;
	}

	.job-cell__header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 0.5rem;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	.job-cell__identity {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}

	.job-cell__id {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.85rem;
		font-weight: 600;
		letter-spacing: 0.03em;
		color: var(--text);
	}

	.job-cell__bookid {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.7rem;
		color: var(--text-muted);
		padding: 0.1rem 0.4rem;
		background: var(--elevated);
		border-radius: var(--radius-sm);
	}

	.job-cell__status {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.job-cell__status-text {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.75rem;
		color: var(--text-secondary);
		font-weight: 500;
	}

	.job-cell__output {
		max-height: 16rem;
		overflow: auto;
		padding: 0.6rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.72rem;
		line-height: 1.55;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-all;
	}

	.job-cell__waiting {
		font-size: 0.8rem;
		color: var(--text-muted);
		font-style: italic;
		padding: 0.5rem 0;
	}

	.job-cell__error {
		display: flex;
		align-items: flex-start;
		gap: 0.4rem;
		margin-top: 0.5rem;
		padding: 0.5rem 0.75rem;
		background: var(--error-bg);
		border: 1px solid var(--error);
		border-radius: var(--radius-sm);
		font-size: 0.8rem;
		color: var(--error);
	}

	/* Spinner for loading state */
	:global(.animate-spin) {
		animation: spin 1s linear infinite;
	}

	@keyframes spin {
		from { transform: rotate(0deg); }
		to { transform: rotate(360deg); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.animate-spin) {
			animation: none;
		}
	}

	/* Narrow screens */
	@media (max-width: 640px) {
		.bay__controls {
			flex-direction: column;
			align-items: flex-start;
		}

		.manifest-row {
			padding: 0.5rem 0.75rem;
		}

		.manifest-row__cover {
			display: none;
		}

		.manifest-row__status-text {
			display: none;
		}

		.job-cell__header {
			flex-direction: column;
			align-items: flex-start;
		}
	}
</style>

<!-- Inline SVG components to avoid extra imports -->
{#snippet SpinnerIcon()}
	<span
		class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent"
		aria-hidden="true"
	></span>
{/snippet}

{#snippet LockPlaceholder()}
	<span
		class="inline-flex h-4 w-4 items-center justify-center text-[var(--text-muted)]"
		aria-hidden="true"
	>
		<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
			<rect width="18" height="11" x="3" y="11" rx="2" ry="2" />
			<path d="M7 11V7a5 5 0 0 1 10 0v4" />
		</svg>
	</span>
{/snippet}