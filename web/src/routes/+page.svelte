<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, Button } from '$lib/components';
	import { formatRuntime } from '$lib/types';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';

	let totals = $state({ pending: 0, matched: 0, processing: 0, done: 0, error: 0 });
	let recent: BookWithPeople[] = $state([]);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	async function load() {
		await dl.run(async () => {
			const [pending, matched, processing, done, err, recentResp] = await Promise.all([
				get<BooksListResponse>('/books?status=pending&limit=1'),
				get<BooksListResponse>('/books?status=matched&limit=1'),
				get<BooksListResponse>('/books?status=processing&limit=1'),
				get<BooksListResponse>('/books?status=done&limit=1'),
				get<BooksListResponse>('/books?status=error&limit=1'),
				get<BooksListResponse>('/books?limit=5'),
			]);
			totals = {
				pending: pending.total,
				matched: matched.total,
				processing: processing.total,
				done: done.total,
				error: err.total,
			};
			recent = recentResp.books || [];
		});
	}

	$effect(() => {
		load();
	});

	// ── Workflow rail stations with live counts ──
	// Safe numeric defaults — never display NaN if API data is missing
	const safePending = $derived(typeof totals.pending === 'number' ? totals.pending : 0);
	const safeMatched = $derived(typeof totals.matched === 'number' ? totals.matched : 0);
	const safeProcessing = $derived(typeof totals.processing === 'number' ? totals.processing : 0);
	const safeDone = $derived(typeof totals.done === 'number' ? totals.done : 0);
	const safeErrors = $derived(typeof totals.error === 'number' ? totals.error : 0);

	const stations = $derived([
		{
			id: 'intake',
			label: 'INTAKE',
			station: '#01',
			count: safePending,
			href: '/import',
			description: 'Pending import',
		},
		{
			id: 'match',
			label: 'MATCH',
			station: '#02',
			count: safeMatched,
			href: '/match',
			description: 'Metadata review & pairing',
			active: safeMatched > 0,
		},
		{
			id: 'queue',
			label: 'QUEUE',
			station: '#03',
			count: safeProcessing,
			href: '/process',
			description: 'Processing & conversion',
			active: safeProcessing > 0,
		},
		{
			id: 'finish',
			label: 'FINISH',
			station: '#04',
			count: safeDone,
			href: '/books?status=done',
			description: 'Completed library',
			done: true,
		},
	]);
</script>

<PageHeader title="Dashboard" description="Operations board — workflow stations and recent activity manifest." />

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>{dl.error}</Alert>
	</div>
{/if}

<!-- ── Workflow Rail ─────────────────────────────────────────── -->
{#if dl.showSkeleton}
	<div class="mb-8" aria-label="Workflow rail loading">
		<div class="flex items-center gap-0 overflow-x-auto pb-1">
			{#each Array(4) as _}
				<div class="flex-shrink-0 flex items-center gap-0">
					{#if _ > 0}
						<div class="h-0.5 w-4 sm:w-8 bg-[var(--border)]" aria-hidden="true"></div>
					{/if}
					<div class="card px-3 py-2 sm:px-4 sm:py-3">
						<Skeleton height="0.5rem" width="2.5rem" />
						<Skeleton class="mt-2" height="1.25rem" width="2rem" />
					</div>
				</div>
			{/each}
		</div>
	</div>
{:else}
	<!--
		Signal Interlocking Panel — Workflow Rail
		Contract: stations render as orthogonal panels linked by rails.
		Each station has: number (#NN), label, live count, link target.
		Narrow screens: horizontal scroll is permitted; station order is fixed.
	-->
	<div
		class="mb-8"
		role="navigation"
		aria-label="Workflow stations: {stations.map(s => `${s.station} ${s.label}`).join(', ')}"
	>
		<!-- Rail track line -->
		<div class="relative flex items-stretch overflow-x-auto pb-1">
			<!-- Background rail line (full width behind stations) -->
			<div class="absolute left-0 top-1/2 -z-10 h-0.5 w-full bg-[var(--border-subtle)] -translate-y-1/2" aria-hidden="true"></div>

			{#each stations as station (station.id)}
				<div class="flex items-center flex-shrink-0">
					<!-- Rail segment connector -->
					{#if station.id !== 'intake'}
						<div
							class="h-0.5 w-4 sm:w-8 flex-shrink-0"
							class:bg-[var(--border)]={!station.active}
							class:bg-[var(--state-amber)]={station.active}
							aria-hidden="true"
						></div>
					{/if}

					<!-- Station panel -->
					<a
						href={station.href}
						class={`card group inline-flex min-w-[5rem] sm:min-w-[7rem] flex-col items-center gap-1 px-3 py-2 text-center no-underline sm:px-4 sm:py-3 ${station.active ? 'border-[var(--state-amber)]/40' : ''} ${station.done ? 'border-[var(--state-green)]/40' : ''}`}
						aria-label="{station.station} {station.label}: {station.count} items. {station.description}"
					>
						<!-- Station number — monospace, always visible -->
						<span class="text-xs font-mono tracking-widest text-[var(--text-muted)]">
							{station.station}
						</span>

						<!-- Station label — uppercase, compact -->
						<span class="text-xs font-semibold tracking-wide text-[var(--text)] sm:text-sm">
							{station.label}
						</span>

						<!-- Live count — monospace, integrated into station -->
						<span
							class="text-lg font-mono font-semibold leading-none tracking-tight sm:text-xl"
							class:text-[var(--state-amber)]={station.active}
							class:text-[var(--state-green)]={station.done}
							class:text-[var(--text)]={!station.active && !station.done}
							aria-label="{station.count} items"
						>
							{station.count}
						</span>

						<!-- Description — visible on wider screens -->
						<span class="hidden text-[0.65rem] text-[var(--text-muted)] sm:block">
							{station.description}
						</span>

						<!-- Active station pulse indicator -->
						{#if station.active}
							<span
								class="absolute -right-0.5 -top-0.5 flex h-2 w-2"
								aria-hidden="true"
							>
								<span
									class="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--state-amber)]/50"
								></span>
								<span
									class="relative inline-flex h-2 w-2 rounded-full bg-[var(--state-amber)]"
								></span>
							</span>
						{/if}
					</a>
				</div>
			{/each}
		</div>
	</div>
{/if}

<!-- ── Continuation Actions ──────────────────────────────────── -->
<div class="mb-4 flex flex-wrap items-center gap-2">
	<Button variant="secondary" href="/import">Import</Button>
	<Button variant="primary" href="/match">Match</Button>
	{#if safeErrors > 0}
		<a href="/books?status=error" class="ml-auto inline-flex items-center gap-1.5 rounded-md px-3 py-2.5 text-sm font-medium text-[var(--error)] no-underline hover:opacity-80 min-h-[44px] whitespace-nowrap">
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
				<Skeleton variant="rect" width="48px" height="64px" />
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
				href="/books?status={book.status}"
				class="card group flex items-center gap-4 no-underline transition-colors hover:bg-[var(--surface-hover)]"
			>
				{#if book.cover_image_url}
					<img
						src={book.cover_image_url}
						alt=""
						loading="lazy"
						decoding="async"
						class="h-12 w-9 flex-shrink-0 rounded-sm object-cover sm:h-16 sm:w-12"
					/>
				{:else}
					<div class="flex h-12 w-9 flex-shrink-0 items-center justify-center rounded-sm bg-[var(--elevated)] text-xs text-[var(--text-muted)] sm:h-16 sm:w-12">
						—
					</div>
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