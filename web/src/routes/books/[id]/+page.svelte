<script lang="ts">
	import { goto, invalidateAll } from '$app/navigation';
	import { get, put, post } from '$lib/api';
	import type { PageData } from './$types';
	import type { BookWithPeople, ProcessingJob, JobsListResponse, AudiobookDBBook, AudiobookDBRelease } from '$lib/types';
	import { formatRuntime, peopleByRole, bookCoverUrl } from '$lib/types';
	import { cleanAdbError, type CleanedAdbError } from '$lib/adbError';
	import { SpliceMeter, StatusBadge, AbdTag, EmptyState, Button, Alert, Skeleton } from '$lib/components';
	import { ExternalLink, ArrowLeft } from '@lucide/svelte';

	let { data }: { data: PageData } = $props();

	let book: BookWithPeople | null = $derived(data.book);

	const authorNames = $derived(book ? book.authors.map((a) => a.name).join(', ') : '');
	const narratorNames = $derived(book ? book.narrators.map((n) => n.name).join(', ') : '');

	const abdHref = $derived(book?.audiobookdb_book_id ? `https://audiobookdb.org/book/${book.audiobookdb_book_id}` : null);

	/* ── Job history ─────────────────────────────────────────────
	 * Newest-first from the queue API, filtered to this book client-side
	 * (the API's book_id filter is not wired up in this build). Live
	 * updating for running jobs is out of scope — reload the page.  */
	let jobs = $state<ProcessingJob[]>([]);

	$effect(() => {
		const id = book?.id;
		if (!id) return;
		get<JobsListResponse>('/jobs?limit=100')
			.then((resp) => {
				if (book?.id === id) jobs = (resp.jobs || []).filter((j) => j.book_id === id);
			})
			.catch(() => {
				if (book?.id === id) jobs = [];
			});
	});

	/* ── Actions ───────────────────────────────────────────────── */
	let actionError = $state<CleanedAdbError | null>(null);
	let confirming: 'reprocess' | 'rematch' | null = $state(null);
	let acting = $state<'reprocess' | 'rematch' | null>(null);

	// File-backed heuristic: a single audio file (by extension) is not a
	// mergeable source directory — re-process it from the Queue instead.
	// Anything else (including an empty path) is treated as a directory;
	// the processor surfaces the real error if that turns out to be wrong.
	const AUDIO_FILE_EXTS = /\.(m4b|m4a|mp3|mp4|aac|flac|wav|ogg|oga|opus|mka)$/i;
	const isFileBacked = $derived(Boolean(book) && AUDIO_FILE_EXTS.test(book?.src_path ?? ''));

	// While any job for this book is queued or running, both actions
	// are locked out — the queue owns the book until the job finishes.
	const inFlightJob = $derived(
		book ? jobs.some((j) => j.status === 'queued' || j.status === 'running') : false
	);

	function beginAction(action: 'reprocess' | 'rematch') {
		if (acting || inFlightJob || (action === 'reprocess' && isFileBacked)) return;
		actionError = null;
		confirming = action;
	}

	function cancelAction() {
		confirming = null;
	}

	async function doReprocess() {
		if (!book) return;
		acting = 'reprocess';
		try {
			await post(`/process`, { book_ids: [book.id], src_dirs: [book.src_path] });
			confirming = null;
			await invalidateAll(); // refresh book status + job list for the new job
			await goto('/process');
		} catch (e) {
			actionError = cleanApiError(e);
			confirming = null;
		} finally {
			acting = null;
		}
	}

	async function doRematch() {
		if (!book) return;
		acting = 'rematch';
		try {
			await put(`/books/${book.id}`, { status: 'matched' });
			confirming = null;
			await invalidateAll();
			await goto('/match');
		} catch (e) {
			actionError = cleanApiError(e);
			confirming = null;
		} finally {
			acting = null;
		}
	}

	/* ── ADB diff ──────────────────────────────────────────────── */
	let abdExpanded = $state(false);
	let abdBook = $state<AudiobookDBBook | null>(null);
	let abdRelease = $state<AudiobookDBRelease | null>(null);
	let abdLoading = $state(false);
	let abdError = $state<CleanedAdbError | null>(null);

	async function loadAbd() {
		const id = book?.audiobookdb_book_id;
		if (!id) return;
		abdLoading = true;
		abdError = null;
		abdBook = null;
		abdRelease = null;
		try {
			const b = await get<AudiobookDBBook>(`/search/books/${id}`);
			// Runtime, release date and publisher live on the release.
			const rid = b.releases?.[0]?.id;
			const r = rid ? await get<AudiobookDBRelease>(`/search/releases/${rid}`) : null;
			if (book?.audiobookdb_book_id === id) {
				abdBook = b;
				abdRelease = r;
			}
		} catch (e) {
			abdError = cleanApiError(e);
		} finally {
			abdLoading = false;
		}
	}

	/** Seed data stores the literal word 'Unknown'; present it as the
	 *  honest muted label it is, not as if it were a real publisher. */
	function isUnknownLiteral(v: string): boolean {
		return v.trim().toLowerCase() === 'unknown';
	}

	// Errors walk through the central ADB cleaner — human sentences out,
	// raw payload available for the details expander.
	function cleanApiError(e: unknown): CleanedAdbError {
		const msg = e instanceof Error ? e.message : String(e ?? 'Unknown error');
		return cleanAdbError(msg);
	}

	$effect(() => {
		// Fetch once per expansion; retry re-invokes the same loader.
		if (abdExpanded && book?.audiobookdb_book_id) loadAbd();
	});

	interface DiffRow {
		label: string;
		local: string;
		adb: string;
		differs: boolean;
	}

	const adbRows = $derived.by<DiffRow[]>(() => {
		if (!book || !abdBook) return [];
		const localAuthors = authorNames;
		const adbAuthors = peopleByRole(abdBook.people, 'author').map((p) => p.name).join(', ');
		const localNarrators = narratorNames;
		const adbNarrators = peopleByRole(abdBook.people, 'narrator').map((p) => p.name).join(', ');
		// ADB's series relation is id/position only — compare presence, not names.
		const localSeries = book.series || '';
		const adbSeries = (abdBook.series?.length || 0) > 0 ? 'Yes' : '';
		const localRuntime = book.runtime_length_minutes ? formatRuntime(book.runtime_length_minutes) : '';
		const adbRuntime = abdRelease?.runtimeLengthSec
			? formatRuntime(Math.round(abdRelease.runtimeLengthSec / 60))
			: '';
		const localDate = book.release_date || '';
		const adbDate = (abdRelease?.releaseDate || '').slice(0, 10);
		const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();
		const row = (label: string, l: string, a: string): DiffRow => ({
			label,
			local: l,
			adb: a,
			differs: l !== '' || a !== '' ? !same(l, a) : false
		});
		return [
			row('Title', book.title, abdBook.title),
			row('Authors', localAuthors || 'Unknown', adbAuthors),
			row('Narrators', localNarrators || 'Unknown', adbNarrators),
			row('Series', localSeries, adbSeries),
			row('Publisher', book.publisher, abdRelease?.publisher?.name || ''),
			row('Runtime', localRuntime, adbRuntime),
			row('Release date', localDate, adbDate)
		];
	});

	function fmtTs(ts: string | null): string {
		if (!ts) return '—';
		const d = new Date(ts.replace(' ', 'T'));
		return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
	}
</script>

<svelte:head>
	<title>{book ? `${book.title} — Bragi Books` : 'Book — Bragi Books'}</title>
</svelte:head>

{#if !book}
	<EmptyState
		title="Reel not found"
		description="This book isn't on the bench — it may have been removed or the id is wrong."
		actionLabel="Back to catalog"
		actionHref="/books"
	/>
{:else}
	<div class="mb-5">
		<Button variant="ghost" href="/books">
			<ArrowLeft class="h-4 w-4" aria-hidden="true" />
			Back to catalog
		</Button>
	</div>

	<article class="detail">
		<!-- Left — the reel: cover + the splice, read fully -->
		<div class="detail__reel">
			{#if book.cover_image_url}
				<img src={bookCoverUrl(book.cover_image_url, 'large')} alt="Cover of {book.title}" class="detail__cover" />
			{:else}
				<div class="detail__cover detail__cover--placeholder" aria-label="No cover" role="img">
					<svg width="32%" height="40%" viewBox="0 0 16 20" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
						<rect x="1.5" y="1.5" width="13" height="17" rx="2" stroke="currentColor" stroke-width="1.5" />
						<circle cx="8" cy="8" r="3" stroke="currentColor" stroke-width="1.5" />
						<circle cx="8" cy="8" r="0.75" fill="currentColor" />
						<line x1="4" y1="15.5" x2="12" y2="15.5" stroke="currentColor" stroke-width="1.5" />
					</svg>
				</div>
			{/if}

			<div class="detail__status">
				<SpliceMeter
					status={book.status}
					runtimeMinutes={book.runtime_length_minutes}
					size="lg"
				/>
				<div class="detail__badges">
					<StatusBadge status={book.status} />
					{#if book.audiobookdb_book_id}
						<!-- Single provenance mark — clickable, links to the record -->
						<AbdTag withLabel href={abdHref ?? undefined} />
					{/if}
				</div>
				{#if book.status_message}
					<p class="detail__status-message" class:detail__status-message--error={book.status === 'error'}>
						{book.status_message}
					</p>
				{/if}
			</div>
		</div>

		<!-- Right — the record -->
		<div class="detail__record">
			<h1 class="detail__title">{book.title}</h1>
			<p class="detail__byline" class:detail__byline--unknown={!authorNames}>
				{authorNames || 'Unknown author'}{#if narratorNames} · narrated by {narratorNames}{/if}
			</p>

			<dl class="detail__meta">
				<!-- 'Unknown' is shown ONLY for authors/narrators; every other field is omitted when empty -->
				<div class="detail__meta-row">
					<dt class="detail__meta-label">Authors</dt>
					<dd class="detail__meta-value" class:detail__meta-value--unknown={isUnknownLiteral(authorNames || 'Unknown')}>{authorNames || 'Unknown'}</dd>
				</div>
				<div class="detail__meta-row">
					<dt class="detail__meta-label">Narrators</dt>
					<dd class="detail__meta-value" class:detail__meta-value--unknown={isUnknownLiteral(narratorNames || 'Unknown')}>{narratorNames || 'Unknown'}</dd>
				</div>
				{#if book.publisher}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Publisher</dt>
						<dd class="detail__meta-value" class:detail__meta-value--unknown={isUnknownLiteral(book.publisher)}>{book.publisher}</dd>
					</div>
				{/if}
				{#if book.release_date}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Release date</dt>
						<dd class="detail__meta-value">{book.release_date}</dd>
					</div>
				{/if}
				{#if book.series}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Series</dt>
						<dd class="detail__meta-value">{book.series}</dd>
					</div>
				{/if}
				{#if book.language}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Language</dt>
						<dd class="detail__meta-value">{book.language}</dd>
					</div>
				{/if}
				{#if book.runtime_length_minutes}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Runtime</dt>
						<dd class="detail__meta-value font-mono tabular-nums">{formatRuntime(book.runtime_length_minutes)}</dd>
					</div>
				{/if}
				{#if book.format_type}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">Format</dt>
						<dd class="detail__meta-value font-mono">{book.format_type}</dd>
					</div>
				{/if}
				{#if book.asin}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">ASIN</dt>
						<dd class="detail__meta-value font-mono">{book.asin}</dd>
					</div>
				{/if}
			</dl>

			{#if book.description}
				<section class="detail__desc">
					<h2 class="detail__meta-label">Description</h2>
					<p class="detail__desc-body">{book.description}</p>
				</section>
			{/if}
		</div>
	</article>

	<!-- Below the record — the workbench sections -->

	<!-- ACTIONS — the operator's two levers, always confirm-gated -->
	<section class="card workbench" aria-label="Actions">
		<h2 class="workbench__title">Actions</h2>
		<div class="workbench__actions">
			{#if confirming === 'reprocess'}
				<div class="workbench__confirm">
					<span class="workbench__confirm-text">Confirm re-process?</span>
					<Button variant="primary" loading={acting === 'reprocess'} onclick={doReprocess}>
						Yes, re-process
					</Button>
					<Button variant="ghost" onclick={cancelAction}>Cancel</Button>
				</div>
			{:else if confirming === 'rematch'}
				<div class="workbench__confirm">
					<span class="workbench__confirm-text">Send back to the Match station?</span>
					<Button variant="secondary" loading={acting === 'rematch'} onclick={doRematch}>
						Yes, re-match
					</Button>
					<Button variant="ghost" onclick={cancelAction}>Cancel</Button>
				</div>
			{:else}
				<div class="workbench__btns">
					{#if isFileBacked}
						<Button variant="primary" disabled>Re-process</Button>
						<p class="workbench__note">Source is a single file — re-process from the Queue.</p>
					{:else if inFlightJob}
						<Button variant="primary" disabled>Re-process</Button>
						<p class="workbench__note">
							{jobs.find((j) => j.status === 'queued' || j.status === 'running')?.status === 'queued'
								? 'Queued — wait or cancel in Queue.'
								: 'Running — wait or cancel in Queue.'}
						</p>
					{:else}
						<Button variant="primary" onclick={() => beginAction('reprocess')}>Re-process</Button>
					{/if}
					{#if inFlightJob}
						<Button variant="ghost" disabled>Re-match</Button>
					{:else}
						<Button variant="ghost" onclick={() => beginAction('rematch')}>Re-match</Button>
					{/if}
				</div>
			{/if}
		</div>
		{#if actionError}
			<div class="workbench__error">
				<Alert variant="error">
					{actionError.message}
					{#if actionError.rateLimited}
						<span class="block mt-1">
							<a class="workbench__inline-link" href="/settings">Add an API key in Settings</a>
							for higher limits.
						</span>
					{/if}
				</Alert>
			</div>
		{/if}
	</section>

	<!-- ADB DIFF — provenance is the only magenta on the bench -->
	<section class="card workbench" aria-label="Compare with AudiobookDB">
		<div class="workbench__head">
			<h2 class="workbench__title">Compare with AudiobookDB</h2>
			{#if abdHref}
				<a href={abdHref} target="_blank" rel="noopener noreferrer" class="workbench__link">
					Open on AudiobookDB
					<ExternalLink class="h-3.5 w-3.5" aria-hidden="true" />
				</a>
			{/if}
		</div>

		{#if !book.audiobookdb_book_id}
			<p class="workbench__note">Not matched to AudiobookDB. Use <a class="workbench__inline-link" href="/match">Re-match</a> below to link it.</p>
		{:else}
			{#if !abdExpanded}
				<Button variant="secondary" onclick={() => (abdExpanded = true)}>Show comparison</Button>
			{:else}
				{#if abdLoading}
					<div class="workbench__skeleton" aria-label="Loading AudiobookDB record">
						<Skeleton variant="line" height="1.25rem" />
						<Skeleton variant="line" height="1.25rem" width="80%" />
						<Skeleton variant="line" height="1.25rem" width="60%" />
					</div>
				{:else if abdError}
					<Alert variant="error" onretry={loadAbd}>
						<span class="block">
							Could not load the AudiobookDB record: {abdError.message}
							{#if abdError.rateLimited}
								<span class="block mt-1">
									<a class="workbench__inline-link" href="/settings">Add an API key in Settings</a>
									for higher limits.
								</span>
							{/if}
							<details class="mt-1">
								<summary class="cursor-pointer select-none">Technical detail</summary>
								<p class="mt-1 break-all font-mono text-xs text-[var(--text-muted)]">{abdError.raw}</p>
							</details>
						</span>
					</Alert>
				{:else if abdBook}
					<table class="diff">
						<thead>
							<tr>
								<th scope="col" class="diff__label">Field</th>
								<th scope="col" class="diff__col">This bench</th>
								<th scope="col" class="diff__col">
									<AbdTag withLabel />
								</th>
							</tr>
						</thead>
						<tbody>
							{#each adbRows as row}
								<tr class:diff__row--differs={row.differs}>
									<th scope="row" class="diff__label">{row.label}</th>
									<td class="diff__col">{row.local || '—'}</td>
									<td class="diff__col">
										{row.adb || '—'}
										{#if row.differs}
											<span class="diff__tag">differs</span>
										{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				{/if}
			{/if}
		{/if}
	</section>

	<!-- JOB HISTORY — the tape of every merge attempt, newest first -->
	<section class="card workbench" aria-label="Job history">
		<h2 class="workbench__title">Job history</h2>
		{#if jobs.length === 0}
			<p class="workbench__note">No processing jobs yet.</p>
		{:else}
			<ol class="joblist">
				{#each jobs as job}
					<li class="joblist__item">
						<div class="joblist__head">
							<StatusBadge status={job.status} />
							<code class="joblist__id">{job.id}</code>
							<span class="joblist__times">
								{#if job.started_at}started {fmtTs(job.started_at)}{/if}
								{#if job.started_at && job.completed_at} · {/if}
								{#if job.completed_at}done {fmtTs(job.completed_at)}{/if}
								{#if !job.started_at && !job.completed_at}created {fmtTs(job.created_at)}{/if}
							</span>
						</div>
						{#if job.error}
							<p class="joblist__error">{job.error}</p>
						{/if}
						{#if job.output_file}
							<p class="joblist__file">
								<span class="workbench__label">Output</span>
								<code class="joblist__path">{job.output_file}</code>
							</p>
						{/if}
						{#if job.output}
							<details class="joblist__log">
								<summary>Show log</summary>
								<pre class="joblist__log-pre">{job.output}</pre>
							</details>
						{/if}
					</li>
				{/each}
			</ol>
		{/if}
	</section>

	<!-- FILE FACTS — where the tape enters and exits -->
	<section class="card workbench" aria-label="File facts">
		<h2 class="workbench__title">File facts</h2>
		<dl class="workbench__facts">
			<div class="workbench__fact">
				<dt class="workbench__label">Source</dt>
				<dd class="workbench__fact-value">
					<code class="detail__path">{book.src_path || '—'}</code>
				</dd>
			</div>
			<div class="workbench__fact">
				<dt class="workbench__label">Dest</dt>
				<dd class="workbench__fact-value">
					{#if book.dest_path}
						<code class="detail__path">{book.dest_path}</code>
					{:else}
						<code class="detail__path detail__path--empty">Not generated</code>
					{/if}
				</dd>
			</div>
			<div class="workbench__fact">
				<dt class="workbench__label">Format</dt>
				<dd class="workbench__fact-value">
					{#if book.format_type}
						<code class="detail__path">{book.format_type}</code>
					{:else}
						<code class="detail__path detail__path--empty">Unknown</code>
					{/if}
				</dd>
			</div>
		</dl>
	</section>
{/if}

<style>
	/* Two columns on the bench — reel left, record right; stack on mobile */
	.detail {
		display: grid;
		grid-template-columns: minmax(0, 20rem) minmax(0, 1fr);
		gap: 1.5rem;
		align-items: start;
	}

	@media (max-width: 800px) {
		.detail {
			grid-template-columns: 1fr;
		}
	}

	/* The reel side — cover + the big splice */
	.detail__reel {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 1rem;
		padding: 1rem;
		background: var(--bg-panel);
		border: 1.5px solid var(--border-subtle);
		border-radius: var(--radius-md);
		position: sticky;
		top: 5.5rem;
	}

	@media (max-width: 800px) {
		.detail__reel {
			position: static;
		}
	}

	.detail__cover {
		max-height: 260px;
		max-width: 100%;
		width: auto;
		aspect-ratio: 1 / 1;
		object-fit: cover;
		border-radius: var(--radius-md);
		border: 1px solid var(--border);
	}

	.detail__cover--placeholder {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 100%;
		height: 240px;
		background: var(--elevated);
		color: var(--text-muted);
	}

	.detail__status {
		width: 100%;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.75rem;
	}

	.detail__badges {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.detail__status-message {
		margin: 0;
		font-size: 0.85rem;
		color: var(--text-secondary);
	}

	.detail__status-message--error {
		color: var(--state-red);
	}

	/* The record side */
	.detail__record {
		min-width: 0;
		padding: 1.5rem;
		background: var(--bg-panel);
		border: 1.5px solid var(--border-subtle);
		border-radius: var(--radius-md);
	}

	.detail__title {
		margin: 0;
		font-size: 1.75rem;
		font-weight: 700;
		letter-spacing: -0.02em;
		color: var(--enamel);
		text-wrap: balance;
	}

	.detail__byline {
		margin: 0.375rem 0 0;
		font-size: 0.9375rem;
		color: var(--text-secondary);
	}

	.detail__byline--unknown {
		color: var(--text-muted);
		font-style: italic;
	}

	/* Metadata grid — silkscreen labels, values are the data */
	.detail__meta {
		margin: 1.25rem 0 0;
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr));
		gap: 0.625rem 1.25rem;
	}

	.detail__meta-label {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.detail__meta-value {
		margin: 0.125rem 0 0;
		font-size: 0.9rem;
		color: var(--text);
		overflow-wrap: anywhere;
	}

	.detail__meta-value--unknown {
		color: var(--text-muted);
		font-style: italic;
	}

	/* Paths — mono readouts, long paths wrap */
	.detail__path {
		display: block;
		padding: 0.5rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.75rem;
		line-height: 1.5;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-all;
	}

	.detail__path--empty {
		color: var(--text-muted);
		font-style: italic;
	}

	/* Description — prose, kept to a readable measure */
	.detail__desc {
		margin-top: 1.5rem;
	}

	.detail__desc-body {
		margin: 0.5rem 0 0;
		max-width: 65ch;
		font-size: 0.9375rem;
		line-height: 1.65;
		color: var(--text-secondary);
		white-space: pre-line;
	}

	/* ── Workbench sections — stacked bays below the record ───── */
	.workbench {
		margin-top: 1rem;
	}

	.workbench__head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.workbench__title {
		margin: 0;
		font-size: 0.8rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.workbench__link {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--accent);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.workbench__link:hover {
		color: var(--accent-hover);
	}

	.workbench__label {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.workbench__note {
		margin: 0.5rem 0 0;
		font-size: 0.85rem;
		color: var(--text-muted);
	}

	.workbench__inline-link {
		color: var(--accent);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.workbench__actions {
		margin-top: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}

	.workbench__btns {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.workbench__confirm {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.workbench__confirm-text {
		font-size: 0.9rem;
		font-weight: 600;
		color: var(--text);
		margin-right: 0.5rem;
	}

	.workbench__error {
		margin-top: 0.75rem;
	}

	.workbench__skeleton {
		margin-top: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.workbench__facts {
		margin-top: 0.75rem;
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
		gap: 0.625rem 1.25rem;
	}

	.workbench__fact-value {
		margin: 0.25rem 0 0;
	}

	/* ADB diff table — quiet rows, washed rows are the findings */
	.diff {
		margin-top: 0.75rem;
		width: 100%;
		border-collapse: collapse;
		font-size: 0.875rem;
	}

	.diff th,
	.diff td {
		padding: 0.5rem 0.75rem;
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--border-subtle);
	}

	.diff thead th {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.diff__label {
		width: 9rem;
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.diff__col {
		min-width: 10rem;
		color: var(--text);
		overflow-wrap: anywhere;
	}

	.diff__row--differs .diff__col,
	.diff__row--differs .diff__label {
		background: var(--provenance-bg);
	}

	.diff__row--differs .diff__label {
		color: var(--text-secondary);
	}

	.diff__tag {
		display: inline-block;
		margin-left: 0.5rem;
		padding: 0.0625rem 0.375rem;
		font-size: 0.6875rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--provenance);
		background: var(--provenance-bg);
		border: 1px solid var(--provenance-border);
		border-radius: var(--radius-sm);
		white-space: nowrap;
	}

	/* Job history — one line per pass over the tape */
	.joblist {
		margin: 0.75rem 0 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.joblist__item {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		padding: 0.625rem 0.75rem;
		background: var(--bg);
	}

	.joblist__head {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.joblist__id {
		font-size: 0.75rem;
		color: var(--text-muted);
	}

	.joblist__times {
		font-size: 0.8rem;
		color: var(--text-secondary);
	}

	.joblist__error {
		margin: 0.375rem 0 0;
		font-size: 0.85rem;
		color: var(--state-red);
	}

	.joblist__file {
		margin: 0.375rem 0 0;
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.joblist__path {
		font-size: 0.75rem;
		color: var(--text-secondary);
		overflow-wrap: anywhere;
	}

	.joblist__log {
		margin-top: 0.5rem;
	}

	.joblist__log summary {
		cursor: pointer;
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--text-secondary);
		padding: 0.375rem 0;
		user-select: none;
	}

	.joblist__log summary:hover {
		color: var(--text);
	}

	.joblist__log-pre {
		margin: 0;
		padding: 0.625rem 0.75rem;
		background: var(--surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 0.75rem;
		line-height: 1.5;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-word;
		max-height: 18rem;
		overflow-y: auto;
	}
</style>
