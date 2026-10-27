<script lang="ts">
	import { goto, refreshAll } from '$app/navigation';
	import { get, put, post } from '#lib/api.js';
	import type { PageData } from './$types';
	import type { BookWithPeople, ProcessingJob, JobsListResponse, AudiobookDBBook, AudiobookDBRelease } from '#lib/types.js';
	import { formatRuntime, peopleByRole, normStr, decodeHtmlEntities, AUDIO_FILE_EXTS } from '#lib/types.js';
	import { cleanAdbError, toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { formatJobTime, formatElapsed, elapsedLabel } from '#lib/datetime.js';
	import { liveNow } from '#lib/liveNow.svelte.js';
	import { StatusBadge, AbdTag, EmptyState, Button, Alert, ErrorDetail, Skeleton, CoverArt, JobActionLabel } from '#lib/components/index.js';
	import { ExternalLink, ArrowLeft } from '@lucide/svelte';

	let { data }: { data: PageData } = $props();

	let book: BookWithPeople | null = $derived(data.book);

	const authorNames = $derived(book ? book.authors.map((a) => a.name).join(', ') : '');
	const narratorNames = $derived(book ? book.narrators.map((n) => n.name).join(', ') : '');

	const abdBookId = $derived(normStr(book?.audiobookdb_book_id));
	const abdHref = $derived(abdBookId ? `https://audiobookdb.org/books/${abdBookId}` : null);
	const asinStr = $derived(normStr(book?.asin));

	/* ── Recorded activity ───────────────────────────────────────
	 * Newest-first from the queue API, filtered server-side by book_id.
	 * While any job is queued/running the list re-fetches every 8s so
	 * the lock note and history stay live without a manual reload.  */
	let jobs = $state<ProcessingJob[]>([]);
	let jobsError = $state<CleanedAdbError | null>(null);
	// Bumped by the retry button to force the effect below to re-run.
	let jobsReload = $state(0);

	$effect(() => {
		jobsReload; // dependency — retry re-runs this effect
		const id = book?.id;
		if (!id) return;
		let cancelled = false;
		let t: number | null = null;
		const stop = () => {
			if (t) clearInterval(t);
			t = null;
		};
		const load = async () => {
			try {
				const resp = await get<JobsListResponse>(`/jobs?book_id=${id}&limit=20`);
				if (!cancelled && book?.id === id) {
					jobs = resp.jobs || [];
					jobsError = null;
				}
			} catch (e) {
				// Don't blank the history into a misleading "no jobs yet" —
				// surface the failure instead (polling retries it).
				if (!cancelled && book?.id === id) jobsError = toCleanedError(e);
			}
			// Poll only while this book has an in-flight job; otherwise stop.
			if (!cancelled && book?.id === id) {
				if (inFlightJob && !t) t = window.setInterval(load, 8000);
				else if (!inFlightJob) stop();
			}
		};
		load();
		return stop;
	});

	/* ── Actions ───────────────────────────────────────────────── */
	let actionError = $state<CleanedAdbError | null>(null);
	let confirming: 'reprocess' | 'rematch' | null = $state(null);
	let acting = $state<'reprocess' | 'rematch' | null>(null);
	let cancellingJob = $state(false);

	// File-backed heuristic: a single audio file (by extension) is not a
	// mergeable source directory — re-process it from the Queue instead.
	// Anything else (including an empty path) is treated as a directory;
	// the processor surfaces the real error if that turns out to be wrong.
	const isFileBacked = $derived(Boolean(book) && AUDIO_FILE_EXTS.test(book?.src_path ?? ''));

	// While any job for this book is queued or running, both actions
	// are locked out — the queue owns the book until the job finishes.
	const inFlightJob = $derived(
		book ? jobs.some((j) => j.status === 'queued' || j.status === 'running') : false
	);
	const clock = liveNow(() => inFlightJob);

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
			await refreshAll(); // refresh book status + job list for the new job
			await goto('/process');
		} catch (e) {
			actionError = toCleanedError(e);
			confirming = null;
		} finally {
			acting = null;
		}
	}

	async function doRematch() {
		if (!book) return;
		acting = 'rematch';
		try {
			await put(`/books/${book.id}`, { status: 'pending' });
			confirming = null;
			await refreshAll();
			await goto('/match');
		} catch (e) {
			actionError = toCleanedError(e);
			confirming = null;
		} finally {
			acting = null;
		}
	}

	async function cancelInFlight() {
		if (!book || cancellingJob) return;
		const job = jobs.find((j) => j.status === 'queued' || j.status === 'running');
		if (!job) return;
		cancellingJob = true;
		actionError = null;
		try {
			await post(`/jobs/${job.id}/cancel`, {});
			await refreshAll(); // book reverts to matched + job list shows cancelled
		} catch (e) {
			actionError = toCleanedError(e);
		} finally {
			cancellingJob = false;
		}
	}

	/* ── ADB diff ──────────────────────────────────────────────── */
	let abdExpanded = $state(false);
	let abdBook = $state<AudiobookDBBook | null>(null);
	let abdRelease = $state<AudiobookDBRelease | null>(null);
	let abdLoading = $state(false);
	let abdError = $state<CleanedAdbError | null>(null);

	async function loadAbd() {
		const id = normStr(book?.audiobookdb_book_id);
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
			if (normStr(book?.audiobookdb_book_id) === id) {
				abdBook = b;
				abdRelease = r;
			}
		} catch (e) {
			abdError = toCleanedError(e);
		} finally {
			abdLoading = false;
		}
	}

	/** Seed data stores the literal word 'Unknown'; present it as the
	 *  honest muted label it is, not as if it were a real publisher. */
	function isUnknownLiteral(v: string): boolean {
		return v.trim().toLowerCase() === 'unknown';
	}

	$effect(() => {
		// Fetch once per expansion; retry re-invokes the same loader.
		if (abdExpanded && normStr(book?.audiobookdb_book_id)) loadAbd();
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

	/* ── Display hygiene ───────────────────────────────────────
	 * Stored descriptions may carry HTML source (<p>/<b>/<br />);
	 * the record view shows clean plain text only, with the
	 * raw payload available in a Technical detail expander.       */
	const descriptionHasMarkup = $derived(Boolean(book?.description?.match(/<[a-zA-Z/][^>]*>/)));
	const cleanDescription = $derived(
		book?.description
			? decodeHtmlEntities(
					book.description
						.replace(/<br\s*\/?>/gi, '\n')
						.replace(/<\/p>/gi, '\n\n')
						.replace(/<[^>]+>/g, '')
						.replace(/\n{3,}/g, '\n\n')
						.trim()
				)
			: ''
	);

	/* Human release date (e.g. "Dec 12, 2025"); raw ISO kept in
	 * the Technical detail expander below the metadata grid. */
	const releaseDateHuman = $derived.by(() => {
		const raw = book?.release_date || '';
		if (!raw) return '';
		const m = raw.match(/^(\d{4})-(\d{2})-(\d{2})/);
		if (m) {
			const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
			return Number.isNaN(d.getTime()) ? raw : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' });
		}
		return raw;
	});
</script>

<svelte:head>
	<title>{book ? `${book.title} — Bragi Books` : 'Book — Bragi Books'}</title>
</svelte:head>

{#if !book}
	{#if data.loadError}
		<Alert variant="error" onretry={() => refreshAll()}>
			Could not load this book: {data.loadError.message}
			<ErrorDetail raw={data.loadError.raw} needsSettings={data.loadError.needsSettings} />
		</Alert>
	{:else}
		<EmptyState
			title="Book not found"
			description="This book isn't in your library — it may have been removed, or the id is wrong."
			actionLabel="Back to catalog"
			actionHref="/books"
		/>
	{/if}
{:else}
	<div class="mb-5">
		<Button variant="ghost" href="/books">
			<ArrowLeft class="h-4 w-4" aria-hidden="true" />
			Back to catalog
		</Button>
	</div>

	<article class="detail">
		<!-- Left — record identity: cover, status, provenance -->
		<div class="detail__identity">
			{#if book.cover_image_url}
				<CoverArt url={book.cover_image_url} title={book.title} size="xl" />
			{:else}
				<div class="detail__cover--placeholder" aria-label="No cover" role="img">
					<CoverArt size="xl" />
				</div>
			{/if}

			<div class="detail__status">
				<div class="detail__badges">
					<StatusBadge status={book.status} />
					{#if abdBookId}
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
						<dd class="detail__meta-value">{releaseDateHuman}</dd>
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
				{#if asinStr}
					<div class="detail__meta-row">
						<dt class="detail__meta-label">ASIN</dt>
						<dd class="detail__meta-value font-mono">{asinStr}</dd>
					</div>
				{/if}
			</dl>

			{#if book.description}
				<section class="detail__desc">
					<h2 class="detail__section-title">Description</h2>
					<p class="detail__desc-body">{cleanDescription}</p>
					{#if descriptionHasMarkup}
						<details class="detail__raw">
							<summary>Technical detail</summary>
							<pre class="detail__raw-pre">{book.description}</pre>
						</details>
					{/if}
				</section>
			{/if}
			{#if book.release_date}
				<details class="detail__raw detail__raw--release">
					<summary>Raw release date</summary>
					<pre class="detail__raw-pre">{book.release_date}</pre>
				</details>
			{/if}
		</div>
	</article>

	<!-- Below the record — grouped sections -->

	<!-- ── Actions — both confirm-gated ─────────────────────────── -->
	<section class="card record-section" aria-label="Actions">
		<h2 class="detail__section-title">Actions</h2>
		<div class="record-section__actions">
			{#if confirming === 'reprocess'}
				<div class="record-section__confirm">
					<span class="record-section__confirm-text">Confirm re-process?</span>
					<Button variant="primary" loading={acting === 'reprocess'} onclick={doReprocess}>
						Yes, re-process
					</Button>
					<Button variant="ghost" onclick={cancelAction}>Cancel</Button>
				</div>
			{:else if confirming === 'rematch'}
				<div class="record-section__confirm">
					<span class="record-section__confirm-text">Send back to Match for metadata review?</span>
					<Button variant="secondary" loading={acting === 'rematch'} onclick={doRematch}>
						Yes, re-match
					</Button>
					<Button variant="ghost" onclick={cancelAction}>Cancel</Button>
				</div>
			{:else}
				<div class="record-section__btns">
					{#if isFileBacked}
						<Button variant="primary" disabled>Re-process</Button>
						<p class="record-section__note">Source is a single file — re-process from the Queue.</p>
					{:else if inFlightJob}
						<Button variant="primary" disabled>Re-process</Button>
						<p class="record-section__note">
							{jobs.find((j) => j.status === 'queued' || j.status === 'running')?.status === 'queued'
								? 'Queued — wait or cancel below.'
								: 'Running — wait or cancel below.'}
						</p>
						<Button
							variant="ghost"
							class="text-[var(--state-red)] hover:bg-[var(--state-red-bg)]"
							disabled={cancellingJob}
							aria-label="Cancel job {jobs.find((j) => j.status === 'queued' || j.status === 'running')?.id}"
							onclick={cancelInFlight}
						>
							{cancellingJob ? 'Cancelling…' : 'Cancel job'}
						</Button>
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
			<div class="record-section__error">
				<Alert variant="error">
					{actionError.message}
					{#if actionError.rateLimited}
						<span class="block mt-1">
							<a class="record-section__inline-link" href="/settings">Add an API key in Settings</a>
							for higher limits.
						</span>
					{/if}
					<ErrorDetail raw={actionError.raw} needsSettings={actionError.needsSettings} />
				</Alert>
			</div>
		{/if}
	</section>

	<!-- ── Compare with AudiobookDB ────────────────────────────── -->
	<section class="card record-section" aria-label="Compare with AudiobookDB">
		<div class="record-section__head">
			<h2 class="detail__section-title">Compare with AudiobookDB</h2>
			{#if abdHref}
				<a href={abdHref} target="_blank" rel="noopener noreferrer" class="record-section__link">
					Open on AudiobookDB
					<ExternalLink class="h-3.5 w-3.5" aria-hidden="true" />
				</a>
			{/if}
		</div>

		{#if !abdBookId}
			<p class="record-section__note">Not matched to AudiobookDB. Use <a class="record-section__inline-link" href="/match">Re-match</a> below to link it.</p>
		{:else}
			{#if !abdExpanded}
				<Button variant="secondary" onclick={() => (abdExpanded = true)}>Show comparison</Button>
			{:else}
				{#if abdLoading}
					<div class="record-section__skeleton" aria-label="Loading AudiobookDB record">
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
									<a class="record-section__inline-link" href="/settings">Add an API key in Settings</a>
									for higher limits.
								</span>
							{/if}
							<ErrorDetail raw={abdError.raw} needsSettings={abdError.needsSettings} />
						</span>
					</Alert>
				{:else if abdBook}
					<table class="diff">
						<thead>
							<tr>
								<th scope="col" class="diff__label">Field</th>
								<th scope="col" class="diff__col">This record</th>
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

	<!-- ── Recorded activity, newest first ─────────────────────── -->
	<section class="card record-section" aria-label="Recorded activity">
		<h2 class="detail__section-title">Recorded activity</h2>
		{#if jobsError}
			<div class="mb-3">
				<Alert variant="error" onretry={() => (jobsReload += 1)}>
					Could not load recorded activity: {jobsError.message}
					<ErrorDetail raw={jobsError.raw} needsSettings={jobsError.needsSettings} />
				</Alert>
			</div>
		{/if}
		{#if jobs.length === 0 && !jobsError}
			<p class="record-section__note">No recorded activity yet.</p>
		{/if}
		{#if jobs.length > 0}
			<ol class="joblist">
				{#each jobs as job}
					<li class="joblist__item">
						<div class="joblist__head">
							<div class="joblist__state"><StatusBadge status={job.status} /><JobActionLabel action={job.action} /></div>
							<code class="joblist__id">{job.id}</code>
						</div>
						<dl class="joblist__times">
							{#if job.started_at}
								<div class="joblist__time">
									<dt>Started</dt>
									<dd>{formatJobTime(job.started_at)}</dd>
								</div>
							{/if}
							{#if job.completed_at}
								<div class="joblist__time">
									<dt>Done</dt>
									<dd>{formatJobTime(job.completed_at)}</dd>
								</div>
							{/if}
							{#if job.started_at || job.created_at}
								<div class="joblist__time">
									<dt>{elapsedLabel(job.started_at)}</dt>
									<dd>{formatElapsed(job.started_at ?? job.created_at, job.completed_at, clock.now)}</dd>
								</div>
							{/if}
							{#if !job.started_at && !job.completed_at && job.created_at}
								<div class="joblist__time">
									<dt>Created</dt>
									<dd>{formatJobTime(job.created_at)}</dd>
								</div>
							{/if}
						</dl>
						{#if job.detail}
							<p class="joblist__detail">{job.detail}</p>
						{/if}
						{#if job.error}
							{@const jerr = cleanAdbError(job.error)}
							<p class="joblist__error">{jerr.message}</p>
							<ErrorDetail raw={job.error} needsSettings={jerr.needsSettings} />
						{/if}
						{#if job.output_file}
							<p class="joblist__file">
								<span class="record-section__label">Output</span>
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

	<!-- ── File paths: source and output ───────────────────────── -->
	<section class="card record-section" aria-label="File paths">
		<h2 class="detail__section-title">File paths</h2>
		<dl class="record-section__facts">
			<div class="record-section__fact">
				<dt class="record-section__label">Source</dt>
				<dd class="record-section__fact-value">
					<code class="detail__path">{book.src_path || '—'}</code>
				</dd>
			</div>
			<div class="record-section__fact">
				<dt class="record-section__label">Dest</dt>
				<dd class="record-section__fact-value">
					{#if book.dest_path}
						<code class="detail__path">{book.dest_path}</code>
					{:else}
						<code class="detail__path detail__path--empty">Not generated</code>
					{/if}
				</dd>
			</div>
			<div class="record-section__fact">
				<dt class="record-section__label">Format</dt>
				<dd class="record-section__fact-value">
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
	/* Two columns — identity left, record right; stack on mobile */
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

	/* Record identity — cover, status, provenance */
	.detail__identity {
		display: flex;
	flex-direction: column;
	align-items: flex-start;
	gap: 1rem;
	padding: 1rem;
	background: var(--bg-panel);
	border: 1.5px solid var(--border-subtle);
	border-radius: var(--radius-md);
	position: sticky;
	top: calc(var(--nav-height) + 12px);
	}

	@media (max-width: 800px) {
		.detail__identity {
			position: static;
		}
	}

	.detail__cover--placeholder {
		width: 100%;
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
		font-size: clamp(1.5rem, 1.35rem + 0.6vw, 2rem);
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

	/* Raw payload expanders — Technical detail stays available, hidden by default */
	.detail__raw {
		margin-top: 0.75rem;
	}

	.detail__raw--release {
		margin-top: 1.25rem;
	}

	.detail__raw summary {
		cursor: pointer;
		font-size: 0.75rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
		user-select: none;
	}

	.detail__raw-pre {
		margin: 0.5rem 0 0;
		max-height: 14rem;
		overflow: auto;
		padding: 0.625rem 0.75rem;
		background: var(--surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.75rem;
		line-height: 1.5;
		color: var(--text-secondary);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	/* ── Record sections stacked below the identity/record grid ─── */
	.record-section {
		margin-top: 1rem;
	}

	.record-section__head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	/* One header class for every section title
	 * (Actions / Compare / Recorded activity / File paths / Description). */
	.detail__section-title {
		margin: 0;
		font-size: 0.8rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.record-section__link {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--workspace-action);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.record-section__link:hover {
		color: var(--text);
	}

	.record-section__label {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.record-section__note {
		margin: 0.5rem 0 0;
		font-size: 0.85rem;
		color: var(--text-muted);
	}

	.record-section__inline-link {
		color: var(--workspace-action);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.record-section__actions {
		margin-top: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}

	.record-section__btns {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.record-section__confirm {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.record-section__confirm-text {
		font-size: 0.9rem;
		font-weight: 600;
		color: var(--text);
		margin-right: 0.5rem;
	}

	.record-section__error {
		margin-top: 0.75rem;
	}

	.record-section__skeleton {
		margin-top: 0.75rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.record-section__facts {
		margin-top: 0.75rem;
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
		gap: 0.625rem 1.25rem;
	}

	.record-section__fact-value {
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

	/* Recorded activity — one entry per run or record change */
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

	.joblist__state {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	.joblist__id {
		font-size: 0.75rem;
		color: var(--text-muted);
	}

	.joblist__times {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem 1.25rem;
		margin: 0.5rem 0 0;
		font-size: 0.8rem;
		color: var(--text-secondary);
	}

	.joblist__time {
		display: flex;
		align-items: baseline;
		gap: 0.4rem;
	}

	.joblist__time dt {
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.joblist__time dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
	}

	.joblist__error {
		margin: 0.375rem 0 0;
		font-size: 0.85rem;
		color: var(--state-red);
	}

	.joblist__detail {
		margin: 0.375rem 0 0;
		font-size: 0.85rem;
		color: var(--text-secondary);
		overflow-wrap: anywhere;
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
