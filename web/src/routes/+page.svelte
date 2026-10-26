<script lang="ts">
	import { get } from '#lib/api.js';
	import type { BookWithPeople, BooksListResponse, JobsListResponse, ProcessingJob, Settings, StatusCounts } from '#lib/types.js';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, ErrorDetail, Button, CoverArt } from '#lib/components/index.js';
	import { formatRuntime } from '#lib/types.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';

	// One-time notice pointing at the (default-off) external API key.
	// localStorage here is the DISMISSAL marker only — never the key.
	const API_NOTICE_KEY = 'bragibooks_api_notice_dismissed';
	// "unknown" must never read as "off": a failed settings fetch is not
	// evidence that remote access is disabled. A deployment-managed flag
	// is also not actionable from Settings, so it gets no nudge.
	let apiNotice = $state<'unknown' | 'off' | 'on' | 'managed'>('unknown');
	let noticeDismissed = $state(false);
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
			})
			.catch(() => {
				// Decorative notice: an unknown state hides the banner
				// instead of claiming the API is off.
				apiNotice = 'unknown';
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

	// idle = no work in step, active = ready or in-flight work, error = failures,
	// done = settled output
	const queueRunning = $derived(jobs.some((j) => j.status === 'running' || j.status === 'queued'));
	const queueError = $derived(jobs.some((j) => j.status === 'error'));

	const workflow = $derived([
		{
			id: 'import',
			label: 'Import',
			count: null,
			href: '/import',
			description: 'Add source folders',
			state: 'idle' as const,
		},
		{
			id: 'match',
			label: 'Match',
			count: safePending,
			href: '/match',
			description: 'Metadata review',
			// A step with nothing in it is idle — red `error` is reserved
			// for actual match failures; book-level errors stay on the
			// "N errors" summary link.
			state: safePending > 0 ? ('active' as const) : ('idle' as const),
		},
		{
			id: 'queue',
			label: 'Queue',
			count: safeMatched + safeProcessing,
			href: '/process',
			description: `${safeMatched} ready · ${safeProcessing} processing`,
			state:
				queueRunning || safeMatched > 0 || safeProcessing > 0
					? ('active' as const)
					: queueError
						? ('error' as const)
						: ('idle' as const),
		},
		{
			id: 'done',
			label: 'Done',
			count: safeDone,
			href: '/books?status=done',
			description: 'Completed library',
			state: safeDone > 0 ? ('done' as const) : ('idle' as const),
		},
	]);
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

<!-- ── Workflow summary ──────────────────────────────────────── -->
{#if dl.showSkeleton}
	<div class="mb-6" aria-label="Workflow loading">
		<div class="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]">
			{#each Array(4) as _}
				<div class="flex items-center gap-4 border-b border-[var(--border-subtle)] px-4 py-3 last:border-b-0">
					<Skeleton height="1rem" width="4rem" />
					<Skeleton class="hidden flex-1 max-w-[11rem] sm:block" height="0.75rem" width="9rem" />
					<Skeleton class="ml-auto" height="0.75rem" width="2rem" />
				</div>
			{/each}
		</div>
	</div>
{:else}
	<nav aria-label="Library workflow" class="mb-6">
		<ol class="divide-y divide-[var(--border-subtle)] overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]">
			{#each workflow as step (step.id)}
				<li>
					<a
						href={step.href}
						class="flex min-h-[48px] items-center gap-3 px-4 py-2 no-underline transition-colors hover:bg-[var(--surface-hover)]"
						aria-label={step.count === null ? `${step.label}: ${step.description}` : `${step.label}: ${step.count} books, ${step.state}. ${step.description}`}
					>
						<span class="w-16 flex-shrink-0 text-sm font-semibold text-[var(--text)]">{step.label}</span>
						<span class="hidden min-w-0 flex-1 truncate text-xs text-[var(--text-muted)] sm:block">{step.description}</span>
						{#if step.count === null}
							<span class="ml-auto text-sm text-[var(--text-secondary)]">Browse</span>
						{:else}
							<span class="ml-auto flex flex-shrink-0 items-center gap-2">
								{#if step.state === 'active'}
									<span class="rounded-sm border border-[var(--state-amber-border)] bg-[var(--state-amber-bg)] px-1.5 py-0.5 text-xs font-medium text-[var(--state-amber)]">active</span>
								{:else if step.state === 'error'}
									<span class="rounded-sm border border-[var(--state-red-border)] bg-[var(--state-red-bg)] px-1.5 py-0.5 text-xs font-medium text-[var(--state-red)]">error</span>
								{:else if step.state === 'done'}
									<span class="rounded-sm border border-[var(--state-green-border)] bg-[var(--state-green-bg)] px-1.5 py-0.5 text-xs font-medium text-[var(--state-green)]">done</span>
								{/if}
								<span class="font-mono text-sm tabular-nums text-[var(--text-secondary)]">{step.count}</span>
							</span>
						{/if}
					</a>
				</li>
			{/each}
		</ol>
	</nav>
{/if}

<!-- ── Primary next action ───────────────────────────────────── -->
<div class="mb-6 flex flex-wrap items-center gap-2">
	{#if safeErrors > 0}
		<Button variant="primary" href="/books?status=error">Review {safeErrors} error{#if safeErrors !== 1}s{/if}</Button>
	{/if}
	{#if safePending > 0}
		<Button variant={safeErrors > 0 ? 'secondary' : 'primary'} href="/match">Match metadata</Button>
	{/if}
	{#if safeMatched > 0 || safeProcessing > 0 || queueRunning}
		<Button variant={safeErrors > 0 || safePending > 0 ? 'secondary' : 'primary'} href="/process">View queue</Button>
	{/if}
	<Button variant={safePending > 0 || safeMatched > 0 || safeProcessing > 0 || queueRunning || safeErrors > 0 ? 'ghost' : 'primary'} href="/import">Import books</Button>
	<Button variant="ghost" href="/books">View all books</Button>
</div>

<!-- ── Recent books ──────────────────────────────────────────── -->
<div class="mb-3">
	<h2 class="text-lg font-semibold text-[var(--enamel)]">Recent books</h2>
</div>
{#if dl.showSkeleton}
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
