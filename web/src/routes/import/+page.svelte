<script lang="ts">
	import { get, post } from '#lib/api.js';
	import { goto } from '$app/navigation';
	import type { DirectoryEntry, DirectoriesResponse, Settings, CreateBooksResponse } from '#lib/types.js';
	import { PageHeader, Alert, ErrorDetail, Button, Skeleton, EmptyState } from '#lib/components/index.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { ChevronUp, ChevronDown, Folder, FileAudio, File } from '@lucide/svelte';

	let currentPath = $state('');
	let entries: DirectoryEntry[] = $state([]);
	let filtered: DirectoryEntry[] = $state([]);
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

	const SUPPORTED_EXTENSIONS = new Set(['.mp3', '.m4a', '.m4b', '.mp4', '.aac', '.ogg', '.flac', '.wma']);

	function isSupportedType(entry: DirectoryEntry): boolean {
		if (entry.type === 'dir') return true;
		const ext = entry.name.toLowerCase();
		for (const supported of SUPPORTED_EXTENSIONS) {
			if (ext.endsWith(supported)) return true;
		}
		return false;
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

	async function initPath() {
		loading = true;
		try {
			const settings = await get<Settings>('/settings', {});
			if (!settings?.input_dir) {
				settingsError = toCleanedError('Source directory is not configured.');
				loading = false;
				return;
			}
			await loadDirectory(settings.input_dir);
		} catch (e) {
			settingsError = toCleanedError(e);
			loading = false;
		}
	}

	function validEntries(list: DirectoryEntry[]): DirectoryEntry[] {
		return list.filter((e) => e.name && e.path && e.name !== '.' && e.name !== '..');
	}

	async function loadDirectory(path: string) {
		loading = true;
		await dl.run(async () => {
			const response = await get<DirectoriesResponse>(`/directories?path=${encodeURIComponent(path)}`);
			entries = validEntries(response.entries || []);
			currentPath = response.path || path;
			// Keep selectedPaths alive across directory navigation so user selections are not lost
			filterAndSort();
		});
		loading = false;
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
		const allSelected = filtered.length > 0 && filtered.every((e) => selectedPaths.has(e.path));
		const next = new Set(selectedPaths);
		filtered.forEach((e) => {
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

	$effect(() => {
		initPath();
	});

	$effect(() => {
		filterAndSort();
	});

	const allSelected = $derived(filtered.length > 0 && filtered.every((e) => selectedPaths.has(e.path)));
	const selectedCount = $derived(selectedSourcePaths().length);
	const canSelect = $derived(filtered.length > 0);
</script>

<svelte:head>
	<title>Import — Bragi Books</title>
</svelte:head>

<PageHeader
	title="Import"
	description="Select the source directories or files to add to your library. They move to Match next."
/>

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
		{#if loading || !dl.hasLoaded || dl.showSkeleton}
			<div class="p-3 space-y-2" aria-label="Directory loading">
				{#each Array(8) as _}
					<Skeleton height="2.5rem" />
				{/each}
			</div>
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
				<div class="w-24 text-right">Date</div>
				<div class="w-20 text-right">Size</div>
			</div>

			{#each filtered as entry (entry.path)}
				{#if entry.type === 'dir'}
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
						<div class="import-entry__date w-24 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatDate(entry.mod_time)}</div>
						<div class="import-entry__size w-20 text-right text-xs text-[var(--text-muted)]">—</div>
					</div>
				{:else}
					{@const supported = isSupportedType(entry)}
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
		<div class="flex flex-col-reverse items-center justify-end gap-3 border-t border-[var(--border)] bg-[var(--surface)] px-4 py-3 sm:flex-row">
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
	{/if}
</div>
{/if}

<style>
	@media (max-width: 639px) {
		.import-column-header {
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
