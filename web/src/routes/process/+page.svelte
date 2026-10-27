<script lang="ts">
	import { get, post, streamEvents } from '#lib/api.js';
	import type { BookWithPeople, BooksListResponse, ProcessingResponse, ProcessingJob, JobsListResponse, OutputProfile, OutputProfilesResponse } from '#lib/types.js';
	import { Button, Alert, ErrorDetail, Skeleton, EmptyState, StatusBadge, CoverArt } from '#lib/components/index.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { cleanAdbError, toCleanedError } from '#lib/adbError.js';
	import { formatJobTime } from '#lib/datetime.js';
	import { CheckCircle, Clock, ArrowRight, FileText } from '@lucide/svelte';

	interface JobItem {
		id: string;
		bookId: number | null;
		bookTitle?: string;
		status: string;
		output: string;
		error: string | null;
		startedAt: string | null;
		completedAt: string | null;
		createdAt: string | null;
		/** Local cancellation in flight: badge is optimistic until the backend terminal state lands. */
		cancelRequested: boolean;
		/** Queue-time output destination this batch was queued under (profile-backed queues only), e.g. "output/archive · {author}/{title}". HTML-escaped by Svelte. */
		destination?: string;
	}

	let books: BookWithPeople[] = $state([]);
	let selectedIds = $state<Set<number>>(new Set());
	let profiles = $state<OutputProfile[]>([]);
	let profilesError = $state<string | null>(null);
	// null = "Global settings"; set only when the user picks a saved profile.
	let selectedProfileId = $state<number | null>(null);
	let selectAllControl = $state<HTMLInputElement>();
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let loading = $state(true);
	let starting = $state(false);
	let jobs = $state<JobItem[]>([]);
	let cancelling = $state<Set<string>>(new Set());
	const cleanupMap = $state(new Map<string, () => void>());
	const pollTimers = $state(new Map<string, number>());

	$effect(() => {
		load();
		return () => {
			cleanupMap.forEach((fn) => fn());
			pollTimers.forEach((timer) => clearInterval(timer));
		};
	});

	async function load() {
		loading = true;
		await dl.run(async () => {
			const [matched, pending, jobsResp] = await Promise.all([
				get<BooksListResponse>('/books?status=matched&limit=100'),
				get<BooksListResponse>('/books?status=pending&limit=100'),
				get<JobsListResponse>('/jobs?limit=20'),
			]);
			books = [...(matched.books || []), ...(pending.books || [])];
			restoreJobs(jobsResp.jobs);
		});
		loading = false;
		// Independent of the queue load: a profile-list failure must not
		// fail the page; the selector simply offers Global settings only.
		void loadProfiles();
	}

	async function loadProfiles() {
		try {
			const res = await get<OutputProfilesResponse>('/output-profiles');
			profiles = res.profiles || [];
			profilesError = null;
		} catch {
			profilesError = 'Saved profiles unavailable — queuing uses global settings.';
		}
	}

	async function loadBooks() {
		try {
			const [matched, pending] = await Promise.all([
				get<BooksListResponse>('/books?status=matched&limit=100'),
				get<BooksListResponse>('/books?status=pending&limit=100'),
			]);
			books = [...(matched.books || []), ...(pending.books || [])];
		} catch {
			// Keep the current job view when the book-list refresh is unavailable.
		}
	}

	function restoreJobs(jobList: ProcessingJob[]) {
		cleanupMap.forEach((fn) => fn());
		pollTimers.forEach((timer) => clearInterval(timer));
		cleanupMap.clear();
		pollTimers.clear();

		const items: JobItem[] = [];
		const untitledJobs = new Map<number, JobItem[]>();
		for (const j of jobList) {
			const item = $state<JobItem>({
				id: j.id,
				bookId: j.book_id,
				bookTitle: books.find((book) => book.id === j.book_id)?.title,
				status: j.status,
				output: j.output || '',
				error: j.error || null,
				startedAt: j.started_at,
				completedAt: j.completed_at,
				createdAt: j.created_at ?? null,
				cancelRequested: false,
			});
			items.push(item);
			if (item.bookId !== null && !item.bookTitle) {
				const relatedJobs = untitledJobs.get(item.bookId);
				if (relatedJobs) relatedJobs.push(item);
				else untitledJobs.set(item.bookId, [item]);
			}
			if (isActiveJob(item)) connectSSE(item);
		}
		jobs = items;
		for (const [bookId, relatedJobs] of untitledJobs) {
			void get<BookWithPeople>(`/books/${bookId}`)
				.then((book) => {
					for (const item of relatedJobs) item.bookTitle = book.title;
				})
				.catch(() => {
					// Deleted or temporarily unavailable books retain the ID fallback.
				});
		}
	}

	// Heartbeats ("alive") and unknown payloads must not clobber the job
	// status; only real lifecycle values are whitelisted.
	const JOB_STATUSES = new Set(['queued', 'running', 'done', 'error', 'cancelled']);
	const TERMINAL_STATUSES = new Set(['done', 'error', 'cancelled']);

	function connectSSE(item: JobItem) {
		const stop = streamEvents(
			`/api/jobs/${item.id}/stream`,
			(raw) => {
				try {
					const parsed = JSON.parse(raw);
					if (parsed.status && JOB_STATUSES.has(String(parsed.status).toLowerCase())) {
						const next = String(parsed.status).toLowerCase();
						if (next !== item.status) {
							item.status = next;
							// SSE events carry no clock fields; pull the canonical timestamps.
							void pollJob(item);
						}
					}
					if (parsed.output) item.output = parsed.output;
					if (parsed.message && !parsed.output) item.output += parsed.message + '\n';
					if (parsed.error) item.error = parsed.error;
					if (TERMINAL_STATUSES.has(item.status)) {
						stop();
						cleanupMap.delete(item.id);
						// The SSE payload carries no clock fields and a running cancel
						// persists its terminal row from the worker; keep polling until
						// the canonical job row (with completed_at) arrives.
						startPolling(item);
						void pollJob(item);
					}
				} catch {
					item.output += raw + '\n';
				}
			},
			() => {
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
		pollTimers.set(item.id, window.setInterval(() => pollJob(item), 3000));
	}

	async function pollJob(item: JobItem) {
		try {
			const job = await get<ProcessingJob>(`/jobs/${item.id}`);
			const backendTerminal = TERMINAL_STATUSES.has(job.status.toLowerCase());
			// A cancel request made here keeps the optimistic 'cancelled' badge
			// until the backend row itself is terminal; otherwise the badge would
			// flip back to 'running' while the worker reaps the killed process.
			if (!(item.cancelRequested && !backendTerminal)) {
				if (job.status !== item.status || job.error !== item.error || (job.output && job.output !== item.output)) {
					item.status = job.status;
					item.error = job.error;
					if (job.output) item.output = job.output;
				}
			}
			item.startedAt = job.started_at;
			item.completedAt = job.completed_at;
			item.createdAt = job.created_at ?? null;
			if (!backendTerminal) {
				// Still running (e.g. a cancelled process the worker has not reaped
				// yet): keep the poll loop so the terminal row lands eventually.
				return;
			}
			item.cancelRequested = false;
			stopPolling(item.id);
			refreshAfterTerminal(item.id);
		} catch {
			/* The next scheduled poll can restore the live job state. */
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
		if (selectedReadyCount === 0) return;
		starting = true;
		dl.setError(null);
		try {
			const selected = matchedBooks.filter((b) => selectedIds.has(b.id));
			const payload: { book_ids: number[]; src_dirs: string[]; output_profile_id?: number } = {
				book_ids: selected.map((b) => b.id),
				src_dirs: selected.map((b) => b.src_path),
			};
			if (selectedProfileId !== null) payload.output_profile_id = selectedProfileId;
			// The resolved destination of record for this batch: once the
			// selector resets, this line is what tells two back-to-back
			// batches (profile then global) apart.
			const destination = selectedProfile
				? `${selectedProfile.output_dir || 'Uses the global output directory'} · ${selectedProfile.output_scheme || 'Uses the global path format'}`
				: undefined;
			const response = await post<ProcessingResponse>('/process', payload);
			for (const job of response.jobs) {
				const item = $state<JobItem>({
					id: job.id,
					bookId: job.book_id,
					bookTitle: selected.find((book) => book.id === job.book_id)?.title,
					status: 'queued',
					output: '',
					error: null,
					startedAt: null,
					completedAt: null,
					createdAt: null,
					cancelRequested: false,
					destination,
				});
				jobs = [item, ...jobs];
				connectSSE(item);
				void pollJob(item);
			}
			selectedIds = new Set();
			selectedProfileId = null; // Batch queued with this choice; back to Global settings.
			await loadBooks();
		} catch (e) {
			dl.setError(toCleanedError(e));
		} finally {
			starting = false;
		}
	}

	function isActiveJob(job: JobItem): boolean {
		const status = job.status.toLowerCase();
		return status === 'running' || status === 'queued';
	}

	const matchedBooks = $derived(books.filter((b) => b.status === 'matched'));
	const selectedProfile = $derived(
		selectedProfileId === null ? null : profiles.find((p) => p.id === selectedProfileId) ?? null
	);
	const selectedReadyCount = $derived(
		matchedBooks.reduce((count, book) => count + Number(selectedIds.has(book.id)), 0)
	);
	$effect(() => {
		if (selectAllControl) {
			selectAllControl.indeterminate = selectedReadyCount > 0 && selectedReadyCount < matchedBooks.length;
		}
	});
	const pendingBooks = $derived(books.filter((b) => b.status === 'pending'));
	const activeJobs = $derived(jobs.filter(isActiveJob));
	const pastJobs = $derived(jobs.filter((job) => !isActiveJob(job)));
	const failedJobs = $derived(pastJobs.filter((job) => job.status === 'error' || job.status === 'failed').length);
	const runningJobs = $derived(jobs.filter((j) => j.status.toLowerCase() === 'running').length);
	const queuedJobs = $derived(jobs.filter((j) => j.status.toLowerCase() === 'queued').length);

	function jobStatusLabel(status: string): string {
		switch (status.toLowerCase()) {
			case 'queued': return 'Waiting for a processing slot';
			case 'running': return 'm4b-merge is processing this book';
			case 'done':
			case 'completed': return 'Merged successfully';
			case 'cancelled': return 'Stopped by the operator';
			case 'error':
			case 'failed': return 'Processing failed';
			default: return status;
		}
	}

	async function cancelJob(job: JobItem) {
		if (cancelling.has(job.id)) return;
		const next = new Set(cancelling);
		next.add(job.id);
		cancelling = next;
		try {
			await post(`/jobs/${job.id}/cancel`, {});
			// Optimistic badge only: the terminal row (and its completed_at) is
			// written by the API for queued jobs and by the worker for running
			// ones, so keep refreshing until that canonical row arrives.
			job.status = 'cancelled';
			job.error = null;
			job.cancelRequested = true;
			const existing = cleanupMap.get(job.id);
			if (existing) {
				existing();
				cleanupMap.delete(job.id);
			}
			jobs = [...jobs];
			await pollJob(job);
			// pollJob clears cancelRequested once the canonical row (with
			// completed_at) is in hand and refreshes the book list itself;
			// until then keep polling for the worker's terminal write.
			if (job.cancelRequested) {
				startPolling(job);
				await loadBooks();
			}
		} catch (e) {
			job.error = e instanceof Error ? e.message : 'Failed to cancel job';
		} finally {
			const done = new Set(cancelling);
			done.delete(job.id);
			cancelling = done;
		}
	}
</script>

<svelte:head><title>Queue — Bragi Books</title></svelte:head>

<div class="queue-page">
	<header class="queue-heading">
		<div><h1>Processing queue</h1><p>Select ready books to process. Review job output and outcomes here.</p></div>
		<p class="queue-live" role="status" aria-live="polite">
			<span><span class:live-dot--running={runningJobs > 0} class="live-dot" aria-hidden="true"></span>{runningJobs} running</span>
			<span>{queuedJobs} queued</span>
			{#if activeJobs.length > 0}<a href="#active-jobs">View jobs <ArrowRight size={14} aria-hidden="true" /></a>{/if}
		</p>
	</header>

	{#if dl.error}
		<Alert variant="error" onretry={load}>{dl.error.message}<ErrorDetail raw={dl.error.raw} needsSettings={dl.error.needsSettings} /></Alert>
	{/if}

	{#if dl.showSkeleton}
		<div class="queue-loading" aria-label="Loading queue">{#each Array(5) as _}<Skeleton height="4.5rem" />{/each}</div>
	{:else if !loading && (!dl.error || books.length > 0 || jobs.length > 0)}
		<div class="queue-columns" class:queue-columns--single={matchedBooks.length === 0 && activeJobs.length > 0}>
			<div class="queue-work">
				{#if matchedBooks.length > 0}
					<section class="ready-list" aria-labelledby="ready-title">
						<div class="section-heading"><div><h2 id="ready-title">Ready to process <span>{matchedBooks.length}</span></h2><p>Metadata matched. Select the books to include in this batch.</p></div></div>
						<div class="selection-toolbar">
							<label class="select-all"><input bind:this={selectAllControl} type="checkbox" checked={matchedBooks.length > 0 && selectedReadyCount === matchedBooks.length} onchange={(event) => {
								const checked = event.currentTarget.checked;
								const next = new Set(selectedIds);
								matchedBooks.forEach((book) => { if (checked) next.add(book.id); else next.delete(book.id); });
								selectedIds = next;
							}} aria-label="Select all matched books" /><span>{selectedReadyCount} selected of {matchedBooks.length}</span></label>
							{#if selectedReadyCount > 0}
								<div class="profile-pick">
									<label class="profile-label" for="output-profile-select">Output profile</label>
									<select
										id="output-profile-select"
										class="profile-select"
										value={selectedProfileId === null ? '' : String(selectedProfileId)}
										onchange={(event) => {
											const value = event.currentTarget.value;
											selectedProfileId = value === '' ? null : Number(value);
										}}
										aria-describedby={selectedProfile || profilesError ? 'profile-summary' : undefined}
									>
										<option value="">Global settings</option>
										{#each profiles as profile (profile.id)}
											<option value={profile.id}>{profile.name}</option>
										{/each}
									</select>
									{#if selectedProfile}
										<p id="profile-summary" class="profile-summary" aria-live="polite">
											{selectedProfile.output_dir || 'Uses the global output directory'} · {selectedProfile.output_scheme || 'Uses the global path format'} — applies only to these {selectedReadyCount} books in this batch.
										</p>
									{:else if profilesError}
										<p id="profile-summary" class="profile-summary profile-summary--warn" aria-live="polite">{profilesError}</p>
									{/if}
								</div>
							{/if}
							<Button loading={starting} disabled={selectedReadyCount === 0} onclick={start}>Start processing <ArrowRight size={16} aria-hidden="true" /></Button>
						</div>
						<div class="ready-rows">
							{#each matchedBooks as book (book.id)}
								<div class="queue-row" class:queue-row--selected={selectedIds.has(book.id)}>
									<label class="queue-check"><input type="checkbox" checked={selectedIds.has(book.id)} onchange={() => toggleBook(book.id)} aria-label="Select {book.title} for processing" /></label>
									<CoverArt url={book.cover_image_url} title={book.title} size="sm" />
									<div class="book-identity"><a href="/books/{book.id}">{book.title}</a><p>{book.authors.map((author) => author.name).join(', ') || 'Unknown author'}</p></div>
									<span class="ready-state"><CheckCircle size={14} aria-hidden="true" />Ready</span>
								</div>
							{/each}
						</div>
					</section>
				{:else if activeJobs.length === 0 && !dl.error}
					<div class="queue-empty"><EmptyState title="No books ready to process" description={pendingBooks.length > 0 ? 'Match the remaining sources before adding them to a processing batch.' : 'Import sources and match their metadata to build your next batch.'} actionLabel={pendingBooks.length > 0 ? 'Match metadata' : 'Import sources'} actionHref={pendingBooks.length > 0 ? '/match' : '/import'} /></div>
				{/if}

				{#if pendingBooks.length > 0}
					<details class="pending-list">
						<summary><span>Needs a metadata match <span class="count">{pendingBooks.length}</span></span></summary>
						<div class="pending-intro"><p>These sources cannot be processed until their metadata is matched.</p><a href="/match">Match metadata <ArrowRight size={14} aria-hidden="true" /></a></div>
						{#each pendingBooks as book (book.id)}
							<div class="pending-row"><CoverArt url={book.cover_image_url} title={book.title} size="sm" /><div class="book-identity"><a href="/books/{book.id}">{book.title}</a><p>{book.authors.map((author) => author.name).join(', ') || 'Unknown author'}</p></div><span class="pending-state">Needs match</span></div>
						{/each}
					</details>
				{/if}
			</div>

			<aside class="queue-activity" id="active-jobs" aria-labelledby="activity-title">
				{#if activeJobs.length > 0}
					<section class="active-jobs"><div class="section-heading"><h2 id="activity-title">Processing now <span>{activeJobs.length}</span></h2><p>Open a job to follow its live output.</p></div>{#each activeJobs as job (job.id)}{@render JobRecord(job, true)}{/each}</section>
				{:else}
					<section class="queue-idle"><Clock size={21} aria-hidden="true" /><h2 id="activity-title">Nothing processing</h2><p>Ready books stay in the list until you start a batch.</p></section>
				{/if}
				{#if pastJobs.length > 0}
					<details class="job-history"><summary><span>Recent job history <span class="count">{pastJobs.length}</span></span>{#if failedJobs > 0}<span class="failure-count">{failedJobs} failed</span>{/if}</summary><p class="history-hint">Recent completed, cancelled, and failed jobs. Open a job for output and technical details.</p>{#each pastJobs as job (job.id)}{@render JobRecord(job, false)}{/each}</details>
				{/if}
			</aside>
		</div>
	{/if}
</div>

{#snippet JobRecord(job: JobItem, active: boolean)}
	<article class="job-record">
		<div class="job-record__heading"><div class="job-identity">{#if job.bookId}<a href="/books/{job.bookId}">{job.bookTitle || `Book #${job.bookId}`}</a>{:else}<strong>Unlinked job</strong>{/if}<code title={job.id}>{job.id.slice(0, 8)}</code></div><StatusBadge status={job.status} /></div>
		{#if job.destination}
			<p class="job-destination">Destination: {job.destination}</p>
		{/if}
		<p class="job-description">{jobStatusLabel(job.status)}</p>
		<dl class="job-timestamps">
			{#if job.startedAt}
				<div class="job-timestamp"><dt>Started</dt><dd>{formatJobTime(job.startedAt)}</dd></div>
			{/if}
			{#if job.completedAt}
				<div class="job-timestamp"><dt>Done</dt><dd>{formatJobTime(job.completedAt)}</dd></div>
			{/if}
			{#if !job.startedAt && !job.completedAt && job.createdAt}
				<div class="job-timestamp"><dt>Created</dt><dd>{formatJobTime(job.createdAt)}</dd></div>
			{/if}
		</dl>
		{#if job.error}
			{@const error = cleanAdbError(job.error)}
			<div class="job-message" class:job-message--error={job.status !== 'cancelled'} role={job.status === 'cancelled' ? 'note' : 'alert'}>{error.message}</div>
			<ErrorDetail raw={job.error} needsSettings={error.needsSettings} />
		{/if}
		<details class="job-output" open={active && job.status === 'running'}><summary><FileText size={14} aria-hidden="true" />{active ? 'Live output' : 'Job output'}</summary>{#if job.output}<pre role="log" aria-label="Job {job.id.slice(0, 8)} output" aria-live={active ? 'polite' : 'off'}>{job.output}</pre>{:else}<p>No output recorded yet.</p>{/if}</details>
		{#if active}<Button variant="ghost" class="cancel-job" loading={cancelling.has(job.id)} disabled={cancelling.has(job.id)} aria-label="Cancel job {job.id}" onclick={() => cancelJob(job)}>{cancelling.has(job.id) ? 'Cancelling…' : 'Cancel job'}</Button>{/if}
	</article>
{/snippet}

<style>
	.queue-page { padding-bottom: 2rem; }
	.queue-heading { display: flex; justify-content: space-between; align-items: center; gap: 1.5rem; margin-bottom: 1.75rem; }
	.queue-heading h1 { margin: 0 0 0.5rem; font-size: 1.75rem; letter-spacing: -0.025em; }
	.queue-heading p { font-size: 0.9375rem; color: var(--text-muted); }
	.queue-live { display: flex; align-items: center; flex-wrap: wrap; gap: 1rem; font-variant-numeric: tabular-nums; white-space: nowrap; }
	.queue-live > span, .queue-live a { display: inline-flex; align-items: center; gap: 0.5rem; }
	.queue-live a { color: var(--workspace-action); min-height: 2.75rem; }
	.live-dot { width: 0.45rem; height: 0.45rem; border-radius: 50%; background: var(--text-muted); }
	.live-dot--running { background: var(--state-green); }
	.queue-loading { display: grid; gap: 0.75rem; }
	.queue-columns { display: grid; grid-template-columns: minmax(0, 1fr) 21rem; align-items: start; gap: 1.5rem; margin-top: 1.5rem; }
	.queue-columns--single { grid-template-columns: minmax(0, 1fr); }
	.queue-columns--single .queue-work:empty { display: none; }
	.queue-work, .queue-activity { min-width: 0; }
	.ready-list, .active-jobs { overflow: hidden; background: var(--bg-panel); border-radius: var(--radius-md); }
	.section-heading { padding: 1.25rem 1.5rem; }
	.section-heading h2 { display: flex; align-items: center; gap: 0.6rem; margin: 0; font-size: 1.0625rem; font-weight: 650; }
	.section-heading h2 span, .count { color: var(--text-muted); font-size: 0.8125rem; font-weight: 500; font-variant-numeric: tabular-nums; }
	.section-heading p { color: var(--text-muted); font-size: 0.8125rem; margin-top: 0.35rem; }
	.selection-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.75rem 1.5rem; background: var(--surface); border-top: 1px solid var(--border-subtle); border-bottom: 1px solid var(--border-subtle); }
	.select-all { display: flex; align-items: center; gap: 0.75rem; min-height: 2.75rem; font-size: 0.8125rem; color: var(--text-secondary); font-variant-numeric: tabular-nums; }
	.profile-pick { display: flex; align-items: center; gap: 0.75rem; min-width: 0; flex-wrap: wrap; }
	.profile-label { font-size: 0.8125rem; color: var(--text-secondary); white-space: nowrap; }
	.profile-select { min-height: 2.75rem; padding: 0 0.5rem; font-size: 0.8125rem; color: var(--text); background: var(--bg-panel); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); }
	.profile-summary { margin: 0; width: 100%; font-size: 0.75rem; color: var(--text-muted); overflow-wrap: anywhere; }
	.profile-summary--warn { color: var(--state-amber, var(--text-secondary)); }
	.queue-row { display: grid; grid-template-columns: 2.75rem 3rem minmax(0, 1fr) auto; align-items: center; gap: 0.75rem; padding: 0.875rem 1.5rem 0.875rem 0.75rem; border-bottom: 1px solid var(--border-subtle); }
	.queue-row:last-child { border-bottom: 0; }
	.queue-row:hover { background: var(--surface); }
	.queue-row--selected, .queue-row--selected:hover { background: var(--workspace-selected); }
	.queue-check { display: flex; align-items: center; justify-content: center; min-height: 2.75rem; min-width: 2.75rem; cursor: pointer; }
	.book-identity { min-width: 0; }
	.book-identity a { display: inline-block; color: var(--text); font-size: 0.9375rem; line-height: 1.4; font-weight: 600; overflow-wrap: anywhere; }
	.book-identity p { margin-top: 0.25rem; color: var(--text-muted); font-size: 0.8125rem; overflow-wrap: anywhere; }
	.ready-state { display: inline-flex; align-items: center; gap: 0.35rem; color: var(--state-green); font-size: 0.75rem; white-space: nowrap; }
	.pending-list { margin-top: 1.25rem; border-top: 1px solid var(--border); }
	.pending-list > summary, .job-history > summary { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; cursor: pointer; padding: 1rem 0; font-size: 0.875rem; font-weight: 600; min-height: 2.75rem; list-style: none; }
	.pending-list > summary::after, .job-history > summary::after { content: ''; width: 0.45rem; height: 0.45rem; border-right: 1.5px solid currentColor; border-bottom: 1.5px solid currentColor; transform: rotate(45deg); margin-right: 0.25rem; flex-shrink: 0; }
	.pending-list[open] > summary::after, .job-history[open] > summary::after { transform: rotate(225deg); }
	.pending-list > summary > span { display: inline-flex; align-items: baseline; gap: 0.6rem; }
	.pending-intro { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding-bottom: 1rem; }
	.pending-intro p, .history-hint { color: var(--text-muted); font-size: 0.8125rem; line-height: 1.6; }
	.pending-intro a { display: inline-flex; align-items: center; gap: 0.35rem; color: var(--workspace-action); font-size: 0.8125rem; white-space: nowrap; min-height: 2.75rem; }
	.pending-row { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 0; border-top: 1px solid var(--border-subtle); }
	.pending-row .book-identity { flex: 1; }
	.pending-state { color: var(--text-muted); font-size: 0.75rem; white-space: nowrap; }
	.queue-activity { scroll-margin-top: 7rem; }
	.queue-idle { padding: 1.5rem; background: var(--surface); border-radius: var(--radius-md); }
	.queue-idle > :global(svg) { color: var(--text-muted); margin-bottom: 0.75rem; }
	.queue-idle h2 { font-size: 1rem; margin-bottom: 0.5rem; }
	.queue-idle p { color: var(--text-muted); font-size: 0.875rem; line-height: 1.6; }
	.job-history { margin-top: 1.25rem; border-top: 1px solid var(--border); }
	.job-history > summary { flex-wrap: wrap; }
	.job-history > summary > span:first-child { display: inline-flex; align-items: baseline; gap: 0.5rem; }
	.failure-count { font-size: 0.75rem; color: var(--state-red); font-weight: 500; margin-left: auto; }
	.history-hint { margin: 0 0 1rem; }
	.job-record { min-width: 0; padding: 1.25rem 1.5rem; border-top: 1px solid var(--border-subtle); }
	.job-history .job-record { padding: 1rem 0; }
	.job-record__heading { display: flex; justify-content: space-between; align-items: flex-start; flex-wrap: wrap; gap: 0.5rem; }
	.job-identity { display: grid; gap: 0.35rem; min-width: 0; }
	.job-identity a, .job-identity strong { font-size: 0.875rem; font-weight: 600; overflow-wrap: anywhere; }
	.job-identity code { font-size: 0.75rem; color: var(--text-muted); }
	.job-destination { color: var(--text-secondary); font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace; font-size: 0.75rem; margin-top: 0.65rem; overflow-wrap: anywhere; }
	.job-description { color: var(--text-muted); font-size: 0.75rem; margin-top: 0.65rem; }
	.job-timestamps { display: flex; flex-wrap: wrap; gap: 0.25rem 1rem; margin-top: 0.35rem; font-size: 0.75rem; color: var(--text-secondary); }
	.job-timestamp { display: flex; align-items: baseline; gap: 0.35rem; }
	.job-timestamp dt { font-size: 0.65rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: var(--text-muted); }
	.job-timestamp dd { margin: 0; font-variant-numeric: tabular-nums; }
	.job-message { overflow-wrap: anywhere; font-size: 0.8125rem; line-height: 1.55; margin: 0.75rem 0; color: var(--text-secondary); }
	.job-message--error { color: var(--state-red); }
	.job-output { margin-top: 0.5rem; }
	.job-output > summary { display: flex; align-items: center; gap: 0.4rem; cursor: pointer; min-height: 2.75rem; font-size: 0.75rem; color: var(--text-secondary); }
	.job-output pre { max-height: 16rem; overflow: auto; border-radius: var(--radius-sm); padding: 0.875rem; background: var(--surface); font-size: 0.75rem; line-height: 1.65; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--text-secondary); }
	.job-output > p { font-size: 0.8125rem; color: var(--text-muted); padding: 0 0 0.75rem; }
	:global(.app-shell--workspace .cancel-job) { color: var(--state-red); margin-top: 0.25rem; }
	@media (max-width: 1050px) { .queue-columns { grid-template-columns: minmax(0, 1fr); } .queue-heading { align-items: flex-start; } .queue-live { justify-content: flex-end; } }
	@media (max-width: 640px) {
		.queue-heading { flex-direction: column; gap: 0.75rem; margin-bottom: 1.25rem; }
		.queue-heading h1 { font-size: 1.5rem; }
		.queue-heading p { font-size: 0.875rem; }
		.queue-live { justify-content: flex-start; font-size: 0.8125rem; }
		.queue-columns { gap: 1.25rem; margin-top: 1.25rem; }
		.section-heading { padding: 1.125rem; }
		.section-heading h2 { font-size: 1rem; }
		.selection-toolbar { padding: 0.75rem 1.125rem; gap: 0.5rem; flex-wrap: wrap; }
		.selection-toolbar :global(.btn) { flex: 1; min-width: 10rem; }
		.select-all { flex: 1; white-space: nowrap; }
		.queue-row { grid-template-columns: 2.75rem 3rem minmax(0, 1fr); padding: 0.875rem 1.125rem 0.875rem 0.25rem; gap: 0.5rem; }
		.ready-state { grid-column: 3; margin-top: -0.25rem; }
		.pending-intro { align-items: flex-start; flex-direction: column; gap: 0; }

		.queue-idle { padding: 1.125rem; }
	}
</style>
