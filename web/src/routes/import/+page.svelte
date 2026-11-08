<script lang="ts">
	import { get, post, put } from '#lib/api.js';
	import { goto } from '$app/navigation';
	import { AUDIO_FILE_EXTS } from '#lib/types.js';
	import type {
		DirectoryEntry, DirectoriesResponse, Settings, CreateBooksResponse,
		IndexedDirectoryEntry, IndexedDirectoriesResponse, IndexEvidence,
		IndexState
	} from '#lib/types.js';
	import { PageHeader, Alert, ErrorDetail, Button, Skeleton, EmptyState } from '#lib/components/index.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { ChevronUp, ChevronDown, Folder, FileAudio, File } from '@lucide/svelte';

	let currentPath = $state('');
	let entries: IndexedDirectoryEntry[] = $state([]);
	let filtered: IndexedDirectoryEntry[] = $state([]);
	let settings: Settings | null = $state(null);
	// First-run index prompt. localStorage is the DISMISSAL marker only
	// (mirrors the overview api_notice), so a dismissed prompt stays gone.
	const INDEX_PROMPT_KEY = 'bragibooks_index_prompt_dismissed';
	let indexPromptDismissed = $state(false);
	// "checking" until the settings fetch settles: a failed fetch is not
	// evidence that the index is off, so the banner hides on "unknown".
	let indexState = $state<IndexState | null>(null);
	let indexBusy = $state(false);
	let selectedPaths = $state<Set<string>>(new Set());
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let creating = $state(false);
	let search = $state('');
	let sortBy = $state<'name' | 'date' | 'size'>('name');
	let sortDir = $state<'asc' | 'desc'>('asc');
	// Start in the loading state: initPath() awaits /settings before any
	// directory request starts, so a false initial value renders the empty state.
	let loading = $state(true);
	let settingsError = $state<CleanedAdbError | null>(null);

	// Shared AUDIO_FILE_EXTS is the single source of truth for supported
	// audio containers (covers the same set as the backend audioExtensions,
	// including .wav/.opus/.oga/.mka).
	function isSupportedType(entry: DirectoryEntry): boolean {
		if (entry.type === 'dir') return true;
		return AUDIO_FILE_EXTS.test(entry.name);
	}

	function formatSize(bytes: number): string {
		if (bytes === 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		const value = bytes / Math.pow(1024, i);
		return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
	}

	function formatDate(iso: string): string {
		if (!iso) return '—';
		try {
			const d = new Date(iso);
			if (isNaN(d.getTime())) return '—';
			const now = new Date();
			const diff = now.getTime() - d.getTime();
			const days = Math.floor(diff / 86400000);
			if (days === 0) return 'today';
			if (days === 1) return 'yesterday';
			if (days < 7) return `${days}d ago`;
			if (days < 30) return `${Math.floor(days / 7)}w ago`;
			return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
		} catch {
			return '—';
		}
	}

	// One fetch for the listing, used by initPath() and loadDirectory().
	// With indexing on, the server answers from the index (fast, with
	// evidence); the plain filesystem shape is the fallback. Both normalize
	// into the same entry state, so selection/sort/columns work unchanged.
	async function fetchListing(path: string): Promise<void> {
		const useIndex = settings?.indexing_enabled === true;
		if (useIndex) {
			const response = await get<IndexedDirectoriesResponse>(
				`/directories?path=${encodeURIComponent(path)}&index=1`
			);
			entries = validEntries(response.entries || []);
			currentPath = response.path || path;
			indexState = response.index ?? null;
			return;
		}
		const response = await get<DirectoriesResponse>(`/directories?path=${encodeURIComponent(path)}`);
		entries = validEntries(response.entries || []);
		currentPath = response.path || path;
		indexState = null;
	}

	async function initPath() {
		loading = true;
		// One delayed-load window spans the settings fetch and the first
		// directory fetch, so the 200 ms skeleton delay covers both requests.
		await dl.run(async () => {
			let cfg: Settings;
			try {
				cfg = await get<Settings>('/settings', {});
			} catch (e) {
				// Settings failures keep their own alert (with the Settings
				// link); directory failures surface through dl.error.
				settingsError = toCleanedError(e);
				return;
			}
			if (!cfg?.input_dir) {
				settingsError = toCleanedError('Source directory is not configured.');
				return;
			}
			settings = cfg;
			await fetchListing(cfg.input_dir);
			// Append-while-you-browse: if the index is on but stale for this
			// visit (never run, old, or pointing at a different root), kick a
			// non-blocking refresh and re-list when it lands.
			void maybeBackgroundRefresh();
		});
		loading = false;
	}

	// After the first listing paints, refresh the index when it is on and
	// any of: no index state (server fell back to the filesystem for a
	// path outside the indexed root), never indexed, > 60 s old, or the
	// indexed root differs from the configured input root. The refresh is
	// fire-and-forget; the UI re-lists when its captured generation lands.
	async function maybeBackgroundRefresh() {
		if (settings?.indexing_enabled !== true) return;
		const st = indexState;
		const stale =
			!st ||
			!st.last_run_at ||
			(Date.now() - Date.parse(st.last_run_at)) / 1000 > 60 ||
			(settings.input_dir && st.root && st.root !== settings.input_dir.replace(/\/+$/, ''));
		if (!stale) return;
		indexBusy = true;
		try {
			const res = await post<{ state: IndexState }>('/index/refresh', {});
			const gen = res?.state?.generation ?? -1;
			for (let i = 0; i < 60; i++) {
				await new Promise((r) => setTimeout(r, 2000));
				const cur = await get<{ state: IndexState }>('/index');
				const s = cur?.state;
				if (s && s.generation === gen &&
					['done', 'truncated', 'interrupted', 'error'].includes(s.status)) {
					indexState = s;
					await fetchListing(currentPath);
					return;
				}
			}
		} catch {
			// Decorative: a failed background refresh just leaves the stale
			// listing in place; the manual path is the Refresh control.
		} finally {
			indexBusy = false;
		}
	}

	function validEntries(list: IndexedDirectoryEntry[]): IndexedDirectoryEntry[] {
		return list.filter((e) => e.name && e.path && e.name !== '.' && e.name !== '..');
	}

	async function loadDirectory(path: string) {
		loading = true;
		await dl.run(async () => {
			await fetchListing(path);
			// Keep selectedPaths alive across directory navigation so user selections are not lost
			filterAndSort();
		});
		loading = false;
	}

	function showIndexPrompt(): boolean {
		return settings?.indexing_enabled === false && !indexPromptDismissed;
	}

	function dismissIndexPrompt() {
		try {
			localStorage.setItem(INDEX_PROMPT_KEY, '1');
		} catch {
			// storage unavailable — the banner stays for this page only
		}
		indexPromptDismissed = true;
	}

	async function enableIndex() {
		try {
			await put<Settings>('/settings', { indexing_enabled: true });
		} catch {
			// A failed enable is reported by the settings alert on the next
			// load; the banner simply stays.
			return;
		}
		settings = { ...(settings as Settings), indexing_enabled: true };
		await initPath();
	}

	// ASIN evidence uses the books-list badge grammar: one valid unambiguous
	// ASIN gets the monospace badge; everything else is an honest "—".
	// ASIN is source evidence, not AudiobookDB provenance, so it is a neutral
	// chip, not the magenta AbDB mark.
	function asinCell(evidence: IndexEvidence | undefined): { kind: 'value' | 'many' | 'dash'; text: string } {
		const a = evidence?.asin;
		if (!a) return { kind: 'dash', text: '—' };
		if (a.status === 'present' && a.value) return { kind: 'value', text: a.value };
		if (a.status === 'ambiguous') return { kind: 'many', text: `${a.count ?? 2} ASINs` };
		return { kind: 'dash', text: '—' };
	}

	function titleCell(evidence: IndexEvidence | undefined): string {
		return evidence?.title || '—';
	}

	function filterAndSort() {
		const q = search.trim().toLowerCase();
		let result: DirectoryEntry[];

		if (!q) {
			result = entries;
		} else {
			result = entries.filter((e) => e.name.toLowerCase().includes(q));
		}

		result = [...result].sort((a, b) => {
			// Directories always first
			if (a.type === 'dir' && b.type !== 'dir') return -1;
			if (a.type !== 'dir' && b.type === 'dir') return 1;

			let cmp = 0;
			if (sortBy === 'name') {
				cmp = a.name.localeCompare(b.name);
			} else if (sortBy === 'date') {
				cmp = (a.mod_time || '').localeCompare(b.mod_time || '');
			} else {
				cmp = (a.size || 0) - (b.size || 0);
			}
			return sortDir === 'desc' ? -cmp : cmp;
		});

		filtered = result;
	}

	function togglePath(path: string) {
		const next = new Set(selectedPaths);
		if (next.has(path)) next.delete(path);
		else next.add(path);
		selectedPaths = next;
	}

	function toggleSelectAll() {
		// Unsupported files have disabled checkboxes, so bulk selection only
		// applies to entries the supported-type predicate accepts; with none
		// selectable the click is a no-op, matching the derived allSelected.
		const selectable = filtered.filter((e) => isSupportedType(e));
		if (selectable.length === 0) return;
		const allSelected = selectable.every((e) => selectedPaths.has(e.path));
		const next = new Set(selectedPaths);
		selectable.forEach((e) => {
			if (allSelected) next.delete(e.path);
			else next.add(e.path);
		});
		selectedPaths = next;
	}

	function toggleSort(field: 'name' | 'date' | 'size') {
		if (sortBy === field) {
			sortDir = sortDir === 'asc' ? 'desc' : 'asc';
		} else {
			sortBy = field;
			sortDir = 'asc';
		}
	}

	function parentPath(path: string) {
		if (!path) return '/';
		const idx = path.lastIndexOf('/');
		if (idx <= 0) return '/';
		return path.slice(0, idx);
	}

	function selectedSourcePaths(): string[] {
		const paths = Array.from(selectedPaths).sort((a, b) => a.length - b.length || a.localeCompare(b));
		const kept: string[] = [];
		for (const path of paths) {
			const covered = kept.some((parent) => path === parent || path.startsWith(`${parent.replace(/\/$/, '')}/`));
			if (!covered) kept.push(path);
		}
		return kept;
	}

	async function start() {
		const sourcePaths = selectedSourcePaths();
		if (sourcePaths.length === 0) return;
		creating = true;
		dl.setError(null);
		try {
			const payload = { books: sourcePaths.map((src_path) => ({ src_path })) };
			await post<CreateBooksResponse>('/books', payload);
			await goto('/match');
		} catch (e) {
			dl.setError(toCleanedError(e));
			creating = false;
		}
	}

	// Read the dismissal once on load so a dismissed prompt stays gone across
	// visits; a failed read just leaves the prompt for this page.
	$effect(() => {
		try {
			indexPromptDismissed = localStorage.getItem(INDEX_PROMPT_KEY) === '1';
		} catch {
			indexPromptDismissed = false;
		}
	});

	$effect(() => {
		initPath();
	});

	$effect(() => {
		filterAndSort();
	});

	// Checkbox state tracks the same selectable set toggleSelectAll uses.
	// An empty selectable set (a filter that matches only unsupported files)
	// must not read as "everything is selected": Array.prototype.every on an
	// empty list is true, which would render the checkbox checked and make
	// clicking it a no-op.
	const selectableCount = $derived(filtered.filter((e) => isSupportedType(e)).length);
	const allSelected = $derived(
		selectableCount > 0 &&
			filtered.filter((e) => isSupportedType(e)).every((e) => selectedPaths.has(e.path))
	);
	const selectedCount = $derived(selectedSourcePaths().length);
	const canSelect = $derived(selectableCount > 0);
</script>

<svelte:head>
	<title>Import — Bragi Books</title>
</svelte:head>

<PageHeader
	title="Import"
	description="Select the source directories or files to add to your library. They move to Match next."
/>

{#if showIndexPrompt()}
	<div class="mb-6">
		<Alert role="status">
			{#snippet actions()}
				<Button variant="primary" onclick={enableIndex}>Enable index</Button>
				<Button variant="ghost" onclick={dismissIndexPrompt}>Dismiss</Button>
			{/snippet}
			<span>
				Speed up Import and matching — enable the source index.
			</span>
		</Alert>
	</div>
{/if}

{#if settingsError}
	<div class="mb-6">
		<Alert variant="error" onretry={() => (settingsError = null, initPath())}>
			<div class="flex flex-col gap-1">
				<span>{settingsError.message}</span>
				<ErrorDetail raw={settingsError.raw} />
				<span class="text-xs opacity-80">Open <a href="/settings" class="inline-flex min-h-11 items-center underline font-medium">Settings</a> to configure the source directory.</span>
			</div>
		</Alert>
	</div>
{:else if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={initPath}>
			{dl.error.message}
			<ErrorDetail raw={dl.error.raw} needsSettings={dl.error.needsSettings} />
		</Alert>
	</div>
{/if}

<!-- Source browser -->
{#if !settingsError}
<div class="relative overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]">
	<!-- Toolbar: path navigation + controls -->
	<div class="border-b border-[var(--border-subtle)] p-3 sm:p-4">
		<!-- Path bar -->
		<div class="mb-3 flex items-center gap-2 text-sm">
			{#if currentPath !== '/'}
				<button
					type="button"
					class="inline-flex min-h-[44px] items-center gap-1 rounded-sm border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm font-medium text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
					onclick={() => loadDirectory(parentPath(currentPath))}
					aria-label="Go to parent directory"
				>
					<ChevronUp class="h-4 w-4" aria-hidden="true" />
					Parent
				</button>
			{/if}
			<span class="truncate font-mono text-xs text-[var(--text-secondary)]" title={currentPath || '/'}>{currentPath || '/'}</span>
		</div>

		{#if settings?.indexing_enabled === true && indexState}
			<div class="mb-2 flex items-center gap-2 text-xs text-[var(--text-muted)]" role="status" aria-live="polite">
				{#if indexBusy || ['queued', 'running'].includes(indexState.status)}
					<span>Checking for new sources…</span>
				{:else if indexState.last_run_at}
					<span><span class="font-mono text-[var(--text-secondary)]">{indexState.entries}</span> indexed · {formatDate(indexState.last_run_at)}</span>
				{:else}
					<span>Not indexed yet</span>
				{/if}
			</div>
		{/if}

		<!-- Controls row -->
		<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
			<div class="flex items-center gap-0.5 self-start rounded-sm border border-[var(--border)] bg-[var(--surface)] p-0.5">
				{#each ['name', 'date', 'size'] as field}
					<button
						type="button"
						class={["inline-flex min-h-[44px] items-center gap-1 rounded-sm px-3 py-2 text-xs font-medium capitalize transition-colors", sortBy === field ? 'bg-[var(--bg-panel)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'].join(' ')}
						onclick={() => toggleSort(field as 'name' | 'date' | 'size')}
						aria-label="Sort by {field}{sortBy === field ? (sortDir === 'asc' ? ', ascending' : ', descending') : ''}"
					>
						{field}
						{#if sortBy === field}
							{#if sortDir === 'asc'}
								<ChevronUp class="h-3.5 w-3.5" aria-hidden="true" />
							{:else}
								<ChevronDown class="h-3.5 w-3.5" aria-hidden="true" />
							{/if}
						{/if}
					</button>
				{/each}
			</div>
			<input
				bind:value={search}
				type="search"
				placeholder="Filter entries…"
				class="w-full sm:w-52 min-h-[44px]"
				aria-label="Filter entries"
			/>
		</div>
	</div>

	<!-- Content area -->
	{#if entries.length === 0}
		{#if !dl.hasLoaded && !dl.showSkeleton}
			<!-- 200 ms delay window spanning the settings + first directory
			     fetch: announce pending; no skeleton, no empty state. -->
			<div class="flex h-[25rem] items-center justify-center" role="status" aria-live="polite">
				<span class="sr-only">Loading source directory…</span>
			</div>
		{:else if loading || dl.showSkeleton}
			<div class="p-3 space-y-2" aria-label="Directory loading">
				{#each Array(8) as _}
					<Skeleton height="2.5rem" />
				{/each}
			</div>
		{:else if dl.error}
			<!-- The failure alert above owns this space; do not also claim "empty". -->
		{:else}
			<EmptyState
				title="No source directories found"
				description="The configured input directory is empty. Bragi Books imports whole audiobook folders, so place your source directories there first."
				actionLabel="Check input settings"
				actionHref="/settings"
			/>
		{/if}
	{:else}
		<!-- Loading overlay -->
		{#if loading}
			<div class="absolute inset-0 z-10 flex items-center justify-center rounded-sm bg-[var(--surface)]/60 backdrop-blur-[1px]">
				<div class="text-sm text-[var(--text-muted)]">Loading…</div>
			</div>
		{/if}

		<!-- Selection header -->
		<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2 text-sm">
			<input
				type="checkbox"
				class="h-4 w-4 flex-shrink-0"
				checked={allSelected}
				disabled={!canSelect}
				onchange={toggleSelectAll}
				aria-label="Select all visible"
			/>
			<span class="text-[var(--text-muted)]">Select all visible</span>
			{#if selectedCount > 0}
				<span class="ml-auto font-mono text-xs tabular-nums text-[var(--text-secondary)]">{selectedCount} selected</span>
			{/if}
		</div>

		<!-- Entry table -->
		<div class="max-h-[55vh] overflow-auto">
			<!-- Column headers -->
			<div class="import-column-header flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-1.5 text-xs font-medium text-[var(--text-muted)] bg-[var(--surface)]">
				<div class="w-4" aria-hidden="true"></div>
				<div class="flex-1">Source</div>
				<div class="import-evidence-cell w-28">ASIN</div>
				<div class="import-evidence-cell w-40">Title</div>
				<div class="w-24 text-right">Date</div>
				<div class="w-20 text-right">Size</div>
			</div>

			{#each filtered as entry (entry.path)}
				{#if entry.type === 'dir'}
					{@const asin = asinCell(entry.evidence)}
					<!-- Directory row — navigable -->
					<div class="import-entry group flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2 last:border-b-0 hover:bg-[var(--surface-hover)] transition-colors">
						<input
							type="checkbox"
							class="h-4 w-4 flex-shrink-0"
							checked={selectedPaths.has(entry.path)}
							onchange={() => togglePath(entry.path)}
							aria-label="Select directory {entry.name}"
						/>
						<button
							type="button"
							class="import-entry__name flex min-w-0 flex-1 items-center gap-1.5 truncate text-left text-sm font-medium text-[var(--text)] hover:text-[var(--workspace-action)]"
							title={entry.name}
							onclick={() => loadDirectory(entry.path)}
						>
							<Folder class="h-4 w-4 flex-shrink-0 text-[var(--workspace-action)]" aria-hidden="true" />
							<span class="truncate">{entry.name}</span>
						</button>
						<div class="import-evidence-cell w-28 text-xs">
							{#if asin.kind === 'value'}
								<span class="import-asin-badge">{asin.text}</span>
							{:else if asin.kind === 'many'}
								<span class="text-[var(--text-muted)]">{asin.text}</span>
							{:else}
								<span class="text-[var(--text-muted)]">—</span>
							{/if}
						</div>
						<div class="import-evidence-cell w-40 truncate text-xs text-[var(--text-muted)]" title={titleCell(entry.evidence) !== '—' ? titleCell(entry.evidence) : undefined}>{titleCell(entry.evidence)}</div>
						<div class="import-entry__date w-24 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatDate(entry.mod_time)}</div>
						<div class="import-entry__size w-20 text-right text-xs text-[var(--text-muted)]">—</div>
					</div>
				{:else}
					{@const supported = isSupportedType(entry)}
					{@const asin = asinCell(entry.evidence)}
					<!-- File row — selectable; unsupported files are muted -->
					<div class={["import-entry flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2 last:border-b-0 transition-colors", supported ? 'hover:bg-[var(--surface-hover)]' : 'opacity-50'].join(' ')}>
						<input
							type="checkbox"
							class="h-4 w-4 flex-shrink-0"
							checked={selectedPaths.has(entry.path)}
							disabled={!supported}
							onchange={() => supported && togglePath(entry.path)}
							aria-label="Select file {entry.name}"
						/>
						<div class="import-entry__name flex min-w-0 flex-1 items-center gap-1.5">
							{#if supported}
								<FileAudio class="h-4 w-4 flex-shrink-0 text-[var(--text-muted)]" aria-hidden="true" />
							{:else}
								<File class="h-4 w-4 flex-shrink-0 text-[var(--border-strong)]" aria-hidden="true" />
							{/if}
							<span class={["truncate text-sm", supported ? 'text-[var(--text)]' : 'text-[var(--text-muted)] line-through'].join(' ')} title={entry.name}>
								{entry.name}
							</span>
						</div>
						<div class="import-evidence-cell w-28 text-xs">
							{#if asin.kind === 'value'}
								<span class="import-asin-badge">{asin.text}</span>
							{:else if asin.kind === 'many'}
								<span class="text-[var(--text-muted)]">{asin.text}</span>
							{:else}
								<span class="text-[var(--text-muted)]">—</span>
							{/if}
						</div>
						<div class="import-evidence-cell w-40 truncate text-xs text-[var(--text-muted)]" title={titleCell(entry.evidence) !== '—' ? titleCell(entry.evidence) : undefined}>{titleCell(entry.evidence)}</div>
						<div class="import-entry__date w-24 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatDate(entry.mod_time)}</div>
						<div class="import-entry__size w-20 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatSize(entry.size)}</div>
					</div>
				{/if}
			{:else}
				<!-- No entries match filter -->
				<div class="px-4 py-6 text-center text-sm text-[var(--text-muted)]">
					No entries match your filter.
					<button
						type="button"
						class="ml-1 text-[var(--text-secondary)] hover:text-[var(--text)] hover:underline"
						onclick={() => (search = '')}
					>
						Clear filter
					</button>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Footer: selection count + dispatch action -->
	{#if entries.length > 0}
		<div class="border-t border-[var(--border)] bg-[var(--surface)] px-4 py-3">
			<div class="flex flex-col-reverse items-center justify-end gap-3 sm:flex-row">
				<p class="text-sm text-[var(--text-muted)]">
					<span class="font-mono text-[var(--text-secondary)]">{selectedCount}</span>
					<span class="ml-1">{selectedCount === 1 ? 'item selected' : 'items selected'} for match</span>
				</p>
				<Button
					variant="primary"
					disabled={selectedCount === 0 || creating}
					loading={creating}
					onclick={start}
				>
					{creating ? 'Importing…' : 'Import and match'}
				</Button>
			</div>
		</div>
	{/if}
</div>
{/if}

<style>
	/* Neutral monospace chip: ASIN is source evidence, not AudiobookDB
	   provenance, so it never takes the magenta AbDB mark. */
	.import-asin-badge {
		display: inline-block;
		max-width: 100%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--font-mono, ui-monospace, monospace);
		font-size: 0.72rem;
		padding: 0.125rem 0.375rem;
		border: 1px solid var(--border);
		border-radius: 0.125rem;
		background: var(--surface);
		color: var(--text-secondary);
	}

	@media (max-width: 639px) {
		.import-column-header {
			display: none;
		}

		.import-evidence-cell {
			display: none;
		}

		.import-entry {
			display: grid;
			grid-template-columns: 1.5rem minmax(0, 1fr) auto;
			column-gap: 0.75rem;
			row-gap: 0.125rem;
			padding-block: 0.375rem;
		}

		.import-entry > input {
			grid-column: 1;
			grid-row: 1 / 3;
		}

		.import-entry__name {
			grid-column: 2 / -1;
			grid-row: 1;
			min-height: 2.75rem;
		}

		.import-entry__date {
			grid-column: 2;
			grid-row: 2;
			width: auto;
			text-align: left;
		}

		.import-entry__size {
			grid-column: 3;
			grid-row: 2;
			width: auto;
		}
	}
</style>
