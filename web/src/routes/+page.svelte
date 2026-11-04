<script lang="ts">
	import { get } from '#lib/api.js';
	import type { BookWithPeople, BooksListResponse, JobsListResponse, ProcessingJob, Settings, StatusCounts } from '#lib/types.js';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, ErrorDetail, Button, CoverArt } from '#lib/components/index.js';
	import { formatRuntime } from '#lib/types.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { nextAction as nextActionFor, libraryEmpty as libraryEmptyFor } from '#lib/nextAction.js';

	// One-time notice pointing at the (default-off) external API key.
	// localStorage here is the DISMISSAL marker only — never the key.
	const API_NOTICE_KEY = 'bragibooks_api_notice_dismissed';
	// "unknown" must never read as "off": a failed settings fetch is not
	// evidence that remote access is disabled. A deployment-managed flag
	// is also not actionable from Settings, so it gets no nudge.
	let apiNotice = $state<'unknown' | 'off' | 'on' | 'managed'>('unknown');
	let noticeDismissed = $state(false);
	// Import's pipeline value reports source configuration, not work in the
	// stage. A failed settings fetch is "unknown", never "not configured".
	let sourceState = $state<'checking' | 'ready' | 'missing' | 'unavailable'>('checking');
	const showApiNotice = $derived(apiNotice === 'off' && !noticeDismissed);
	$effect(() => {
		// Storage can be unavailable (private mode, blocked storage): a
		// throwing read must not sink the settings fetch below it.
		try {
			noticeDismissed = localStorage.getItem(API_NOTICE_KEY) === '1';
		} catch {
			noticeDismissed = false;
		}
		get<Settings>('/settings')
			.then((s) => {
				apiNotice = s.api_endpoint_env_managed
					? 'managed'
					: s.api_endpoint_enabled === true
						? 'on'
						: 'off';
				sourceState = s.input_dir?.trim() ? 'ready' : 'missing';
			})
			.catch(() => {
				// Decorative notice: an unknown state hides the banner
				// instead of claiming the API is off.
				apiNotice = 'unknown';
				sourceState = 'unavailable';
			});
	});

	function dismissApiNotice() {
		try {
			localStorage.setItem(API_NOTICE_KEY, '1');
		} catch {
			// storage unavailable — the banner stays for this page only
		}
		noticeDismissed = true;
	}

	let totals = $state({ pending: 0, matched: 0, processing: 0, done: 0, error: 0 });
	let recent: BookWithPeople[] = $state([]);
	let jobs: ProcessingJob[] = $state([]);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	async function load() {
		await dl.run(async () => {
			const [countsResp, recentResp, jobsResp] = await Promise.all([
				get<StatusCounts>('/books/counts'),
				get<BooksListResponse>('/books?limit=5'),
				get<JobsListResponse>('/jobs?limit=20'),
			]);
			totals = countsResp.counts;
			recent = recentResp.books || [];
			jobs = jobsResp.jobs || [];
		});
	}

	$effect(() => {
		load();
	});

	// Workflow counts
	// Safe numeric defaults — never display NaN if API data is missing
	const safePending = $derived(typeof totals.pending === 'number' ? totals.pending : 0);
	const safeMatched = $derived(typeof totals.matched === 'number' ? totals.matched : 0);
	const safeProcessing = $derived(typeof totals.processing === 'number' ? totals.processing : 0);
	const safeDone = $derived(typeof totals.done === 'number' ? totals.done : 0);
	const safeErrors = $derived(typeof totals.error === 'number' ? totals.error : 0);

	// The strip reports state, not work: Import shows source configuration,
	// Match, Queue, and Done show counts. Job failures stay on Queue's
	// Recent activity, so the gold CTA is the page's single failure statement.
	const queueRunning = $derived(jobs.some((j) => j.status === 'running' || j.status === 'queued'));

	const totalBooks = $derived(
		safePending + safeMatched + safeProcessing + safeDone + safeErrors
	);
	// An empty library gets one call to action: the Recent books empty state.
	// Before the fetch settles, and after a failed one, it is not empty — it is
	// unknown.
	const libraryEmpty = $derived(
		libraryEmptyFor({ hasLoaded: dl.hasLoaded, error: dl.error, totalBooks })
	);

	const SOURCE_STATE_LABEL = {
		checking: 'Checking…',
		ready: 'Configured',
		missing: 'Not configured',
		unavailable: 'Unknown',
	} as const;

	// The pipeline is a state report, not a second menu: destinations live in
	// the header, and repeating them here made three sets of controls for the
	// same five routes.
	const pipeline = $derived([
		{ id: 'import', label: 'Import', value: SOURCE_STATE_LABEL[sourceState] },
		{
			id: 'match',
			label: 'Match',
			value: safePending > 0 ? `${safePending} waiting` : 'Nothing waiting',
		},
		{
			id: 'queue',
			label: 'Queue',
			value:
				safeProcessing > 0 && safeMatched > 0
					? `${safeProcessing} processing · ${safeMatched} ready`
					: safeProcessing > 0
						? `${safeProcessing} processing`
						: safeMatched > 0
							? `${safeMatched} ready`
							: queueRunning
								? 'Active'
								: 'Empty',
		},
		{
			id: 'done',
			label: 'Done',
			value: safeDone > 0 ? `${safeDone} finished` : 'Nothing finished yet',
		},
	]);

	// Exactly one move is offered, chosen by urgency: failures first, then
	// matching, then queued work, then intake.
	const nextAction = $derived(
		nextActionFor({
			errors: safeErrors,
			pending: safePending,
			matched: safeMatched,
			processing: safeProcessing,
			queueRunning,
		})
	);
</script>

<PageHeader title="Overview" description="What needs attention in your library, and where each book is right now." />

{#if showApiNotice}
	<div class="mb-6">
		<Alert role="status">
			{#snippet actions()}
				<Button variant="ghost" onclick={dismissApiNotice}>Dismiss</Button>
			{/snippet}
			<span>
				External API is off. Enable it in
				<a href="/settings" class="underline underline-offset-2">Settings</a> to use the
				API from other machines.
			</span>
		</Alert>
	</div>
{/if}

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>
			{dl.error.message}
			<ErrorDetail raw={dl.error.raw} needsSettings={dl.error.needsSettings} />
		</Alert>
	</div>
{/if}

<!-- ── Pipeline summary ──────────────────────────────────────── -->
{#if !dl.hasLoaded && !dl.showSkeleton}
	<!-- 200 ms delay window: announce pending, no skeleton, no zeroed values. -->
	<div class="mb-3" aria-label="Library pipeline loading" role="status" aria-live="polite">
		<div class="flex h-[8.5rem] items-center justify-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)] sm:h-[4.25rem]">
			<span class="sr-only">Loading the library pipeline…</span>
		</div>
	</div>
{:else if dl.showSkeleton}
	<div class="mb-3" aria-label="Library pipeline loading">
		<div class="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]">
			<div class="grid grid-cols-2 gap-px bg-[var(--border-subtle)] sm:grid-cols-4">
				{#each Array(4) as _}
					<div class="min-h-[4.25rem] bg-[var(--bg-panel)] px-4 py-3">
						<Skeleton height="0.75rem" width="3rem" />
						<Skeleton class="mt-2" height="0.875rem" width="5rem" />
					</div>
				{/each}
			</div>
		</div>
	</div>
{:else if !dl.error && !libraryEmpty}
	<section aria-label="Library pipeline" class="mb-3">
		<dl class="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-[var(--border)] bg-[var(--border-subtle)] sm:grid-cols-4">
			{#each pipeline as step (step.id)}
				<div class="bg-[var(--bg-panel)] px-4 py-3">
					<dt class="text-xs font-semibold text-[var(--text-muted)]">{step.label}</dt>
					<dd class="mt-1 text-sm font-semibold tabular-nums text-[var(--text)]">{step.value}</dd>
				</div>
			{/each}
		</dl>
	</section>
{/if}

<!-- ── Primary next action ───────────────────────────────────── -->
<!-- One move, chosen from library state. Navigation belongs to the header. -->
{#if !libraryEmpty && dl.error === null}
	<div class="mb-6" class:invisible={!dl.hasLoaded}>
		<Button variant="primary" href={nextAction.href}>{nextAction.label}</Button>
	</div>
{/if}

<!-- ── Recent books ──────────────────────────────────────────── -->
{#if !dl.hasLoaded || recent.length > 0}
	<div class="mb-3">
		<h2 class="text-lg font-semibold text-[var(--enamel)]">Recent books</h2>
	</div>
{/if}
{#if !dl.hasLoaded && !dl.showSkeleton}
	<!-- 200 ms delay window: announce pending; sized like the settled rows. -->
	<div class="space-y-2" aria-label="Recent books loading" role="status" aria-live="polite">
		{#each Array(3) as _}
			<div class="card h-[4.25rem]">
				<span class="sr-only">Loading recent books…</span>
			</div>
		{/each}
	</div>
{:else if dl.showSkeleton}
	<div class="space-y-2" aria-label="Recent books loading">
		{#each Array(3) as _}
			<div class="card flex items-center gap-4">
				<Skeleton variant="rect" width="48px" height="48px" />
				<div class="flex-1">
					<Skeleton height="1rem" width="40%" />
					<Skeleton class="mt-2" height="0.875rem" width="25%" />
				</div>
			</div>
		{/each}
	</div>
{:else if dl.error && recent.length === 0}
	<!-- The failure alert above owns this space; do not also claim "empty". -->
{:else if recent.length === 0}
	<EmptyState
		title="No books yet"
		description="Import source directories and match them to metadata to get started."
		actionLabel="Import books"
		actionHref="/import"
	/>
{:else}
	<div class="space-y-2" aria-label="Recent books">
		{#each recent as book (book.id)}
			<a
				href="/books/{book.id}"
				class="card group flex items-center gap-4 no-underline transition-colors hover:bg-[var(--surface-hover)]"
			>
				<CoverArt url={book.cover_image_url} title={book.title} size="sm" />
				<div class="min-w-0 flex-1">
					<h3 class="line-clamp-2 sm:truncate font-medium">{book.title}</h3>
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
