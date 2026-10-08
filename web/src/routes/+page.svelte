<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse, JobsListResponse, ProcessingJob, StatusCounts } from '$lib/types';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, Button, CoverArt } from '$lib/components';
	import { formatRuntime } from '$lib/types';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';

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

	// ── Workflow bay counts ──
	// Safe numeric defaults — never display NaN if API data is missing
	const safePending = $derived(typeof totals.pending === 'number' ? totals.pending : 0);
	const safeMatched = $derived(typeof totals.matched === 'number' ? totals.matched : 0);
	const safeProcessing = $derived(typeof totals.processing === 'number' ? totals.processing : 0);
	const safeDone = $derived(typeof totals.done === 'number' ? totals.done : 0);
	const safeErrors = $derived(typeof totals.error === 'number' ? totals.error : 0);

	// ── Bay status lights ──
	// idle = no work in bay, active = work in flight, error = failures, done = settled output
	const queueRunning = $derived(jobs.some((j) => j.status === 'running' || j.status === 'queued'));
	const queueError = $derived(jobs.some((j) => j.status === 'error'));

	const bays = $derived([
		{
			id: 'intake',
			label: 'INTAKE',
			station: '#01',
			count: safePending,
			href: '/import',
			description: 'Pending import',
			state: safePending > 0 ? ('active' as const) : ('idle' as const),
		},
		{
			id: 'match',
			label: 'MATCH',
			station: '#02',
			count: safeMatched,
			href: '/match',
			description: 'Metadata review & pairing',
			// Empty bay is idle — red `error` is reserved for actual match
			// failures; book-level errors stay on the "N errors" summary link.
			state: safeMatched > 0 ? ('active' as const) : ('idle' as const),
		},
		{
			id: 'queue',
			label: 'QUEUE',
			station: '#03',
			count: safeProcessing,
			href: '/process',
			description: 'Processing & conversion',
			state:
				queueRunning || safeProcessing > 0
					? ('active' as const)
					: queueError
						? ('error' as const)
						: ('idle' as const),
		},
		{
			id: 'finish',
			label: 'FINISH',
			station: '#04',
			count: safeDone,
			href: '/books?status=done',
			description: 'Completed library',
			state: safeDone > 0 ? ('done' as const) : ('idle' as const),
		},
	]);
</script>

<PageHeader title="Dashboard" description="Operations board — workflow stations and recent activity manifest." />

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>{dl.error}</Alert>
	</div>
{/if}

<!-- ── Workflow Bays ─────────────────────────────────────────── -->
{#if dl.showSkeleton}
	<div class="mb-8" aria-label="Workflow bays loading">
		<div class="grid grid-cols-2 gap-[3px] overflow-hidden rounded-sm border border-[var(--border-subtle)] bg-[var(--rail-line)] lg:grid-cols-4">
			{#each Array(4) as _}
				<div class="bg-[var(--bg-panel)] p-4 sm:p-5">
					<Skeleton height="0.75rem" width="2.5rem" />
					<Skeleton class="mt-3" height="2rem" width="3.5rem" />
					<Skeleton class="mt-3" height="0.75rem" width="60%" />
				</div>
			{/each}
		</div>
	</div>
{:else}
	<!--
		Operator Instrument — Workflow Bays
		Contract: four full-width bays on a continuous tape-path rail.
		Each bay: number (#NN), uppercase label, hero live count (mono),
		status light (fill + text, never color alone), description.
		Narrow screens: 2x2 grid; wide: 4-across.
	-->
	<div
		class="mb-8"
		role="navigation"
		aria-label="Workflow stations: {bays.map(s => `${s.station} ${s.label}`).join(', ')}"
	>
		<!-- Tape-path rail: the grid's rail-line background shows through the
		     3px gaps as a continuous groove connecting all four bays. -->
		<div
			class="grid grid-cols-2 gap-[3px] overflow-hidden rounded-sm border border-[var(--border-subtle)] bg-[var(--rail-line)] lg:grid-cols-4"
		>
			{#each bays as bay (bay.id)}
				<a
					href={bay.href}
					class={`group relative flex min-h-[7rem] flex-col justify-between gap-3 p-4 no-underline transition-colors sm:p-5 lg:min-h-[8rem] ${
						bay.state === 'active' ? 'bg-[var(--state-amber-bg)]' :
						bay.state === 'error' ? 'bg-[var(--state-red-bg)]' :
						bay.state === 'done' ? 'bg-[var(--state-green-bg)]' :
						'bg-[var(--bg-panel)] hover:bg-[var(--surface)]'
					}`}
					aria-label="{bay.station} {bay.label}: {bay.count} items, {bay.state}. {bay.description}"
				>
					<!-- Bay header: number + label -->
					<div class="flex items-center gap-2">
						<span class="font-mono text-xs tracking-widest text-[var(--text-muted)]">
							{bay.station}
						</span>
						<span class="text-xs font-semibold tracking-wide text-[var(--text)]">
							{bay.label}
						</span>
					</div>

					<!-- Hero live count -->
					<span
						class="font-mono text-3xl font-semibold leading-none tracking-tight text-[var(--enamel)] sm:text-4xl"
						aria-label="{bay.count} items"
					>
						{bay.count}
					</span>

					<!-- Footer: status light (fill + text, never color alone) + description -->
					<div class="space-y-1.5">
						<div class="flex items-center gap-2">
							<span
								class="h-2 w-2 flex-shrink-0 rounded-sm border border-[var(--border-strong)]"
								class:bg-[var(--border)]={bay.state === 'idle'}
								class:bg-[var(--state-amber)]={bay.state === 'active'}
								class:bg-[var(--state-red)]={bay.state === 'error'}
								class:bg-[var(--state-green)]={bay.state === 'done'}
								aria-hidden="true"
							></span>
							<span class="text-xs font-medium text-[var(--text-muted)]">
								{bay.state === 'idle'
									? 'idle'
									: bay.state === 'active'
										? 'active'
										: bay.state === 'error'
											? 'error'
											: 'done'}
							</span>
						</div>
						<span class="block text-xs text-[var(--text-muted)]">{bay.description}</span>
					</div>
				</a>
			{/each}
		</div>
	</div>
{/if}

<!-- ── Continuation Actions (subordinate to the bays) ─────────── -->
<div class="mb-6 flex flex-wrap items-center gap-2">
	<Button variant="ghost" href="/import">Route folders in</Button>
	<Button variant="secondary" href="/match">Match metadata</Button>
	{#if safeErrors > 0}
		<a href="/books?status=error" class="ml-auto inline-flex items-center gap-1.5 rounded-sm px-3 py-2.5 text-sm font-medium text-[var(--state-red)] no-underline hover:opacity-80 min-h-[44px] whitespace-nowrap">
			<span>{safeErrors} error{#if safeErrors !== 1}s{/if} — view errors</span>
			<span class="shrink-0" aria-hidden="true">→</span>
		</a>
	{:else if recent.length > 0}
		<Button variant="ghost" href="/books" class="ml-auto">View all →</Button>
	{/if}
</div>

<!-- ── Recent Manifest ───────────────────────────────────────── -->
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
	<div class="space-y-2" aria-label="Recent books manifest">
		{#each recent as book (book.id)}
			<a
				href="/books/{book.id}"
				class="card group flex items-center gap-4 no-underline transition-colors hover:bg-[var(--surface-hover)]"
			>
				<CoverArt url={book.cover_image_url} title={book.title} size="sm" />
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