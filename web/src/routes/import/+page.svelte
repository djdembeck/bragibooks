<script lang="ts">
	import { get, post } from '$lib/api';
	import { goto } from '$app/navigation';
	import type { DirectoryEntry, DirectoriesResponse, Settings, CreateBooksResponse } from '$lib/types';
	import { PageHeader, Alert, Button, Skeleton, EmptyState } from '$lib/components';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';

	let currentPath = $state('');
	let entries: DirectoryEntry[] = $state([]);
	let filtered: DirectoryEntry[] = $state([]);
	let selectedPaths = $state<Set<string>>(new Set());
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let creating = $state(false);
	let search = $state('');
	let sortBy = $state<'name' | 'date' | 'size'>('name');
	let sortDir = $state<'asc' | 'desc'>('asc');
	let loading = $state(false);
	let settingsError = $state<string | null>(null);

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
		try {
			const settings = await get<Settings>('/settings', {});
			if (!settings?.input_dir) {
				settingsError = 'Source directory is not configured.';
				return;
			}
			await loadDirectory(settings.input_dir);
		} catch (e) {
			settingsError = e instanceof Error ? e.message : 'Could not load settings';
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
			dl.setError(e instanceof Error ? e.message : 'Failed to create books');
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
	<title>Intake — Bragi Books</title>
</svelte:head>

<PageHeader
	title="Intake"
	station="#01 · INTAKE"
	description="Select source directories or files to route into the match bay."
/>

{#if settingsError}
	<div class="mb-6">
		<Alert variant="error" onretry={() => (settingsError = null, initPath())}>
			<div class="flex flex-col gap-1">
				<span>{settingsError}</span>
				<span class="text-xs opacity-80">Open <a href="/settings" class="inline-flex min-h-11 items-center underline font-medium">Settings</a> to configure the source directory.</span>
			</div>
		</Alert>
	</div>
{:else if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={initPath}>{dl.error}</Alert>
	</div>
{/if}

<!-- Manifest panel -->
{#if !settingsError}
<div class="panel">
	<!-- Toolbar: path navigation + controls -->
	<div class="border-b border-[var(--border)] p-3 sm:p-4">
		<!-- Breadcrumb / path bar -->
		<div class="mb-3 flex items-center gap-2 text-sm">
			{#if currentPath !== '/'}
				<button
					type="button"
					class="min-h-[44px] rounded-md px-3 py-2.5 text-sm text-[var(--text-muted)] transition-colors hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
					onclick={() => loadDirectory(parentPath(currentPath))}
					aria-label="Go to parent directory"
				>
					← Parent
				</button>
				<span class="text-[var(--border)]" aria-hidden="true">├</span>
			{/if}
			<span class="truncate font-mono text-xs text-[var(--text-secondary)]" title={currentPath || '/'}>{currentPath || '/'}</span>
		</div>

		<!-- Controls row -->
		<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
			<div class="flex items-center gap-1 rounded-md border border-[var(--border)]">
				{#each ['name', 'date', 'size'] as field}
					<button
						type="button"
						class={["min-h-[44px] rounded-md px-3 py-2.5 text-xs font-medium transition-colors", sortBy === field ? 'bg-[var(--accent-wash-strong)] text-[var(--accent)]' : 'text-[var(--text-muted)] hover:text-[var(--text)]'].join(' ')}
						onclick={() => toggleSort(field as 'name' | 'date' | 'size')}
						aria-label="Sort by {field}{sortBy === field ? (sortDir === 'asc' ? ', ascending' : ', descending') : ''}"
					>
						{field}
						{#if sortBy === field}
							<span class="ml-0.5" aria-hidden="true">{sortDir === 'asc' ? '↑' : '↓'}</span>
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
	{#if entries.length === 0 && !loading}
		{#if dl.showSkeleton}
			<div class="p-3 space-y-2">
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
		<div class="flex items-center gap-3 border-b border-[var(--border)] px-4 py-2 text-sm">
			<input
				type="checkbox"
				class="h-4 w-4 flex-shrink-0"
				checked={allSelected}
				disabled={!canSelect}
				onchange={toggleSelectAll}
				aria-label="Select all visible"
			/>
			<span class="text-[var(--text-muted)]">Select all visible</span>
		</div>

		<!-- Entry manifest table -->
		<div class="max-h-[55vh] overflow-auto">
			<!-- Column headers -->
			<div class="flex items-center gap-3 border-b border-[var(--border)] border-t border-[var(--border)] px-4 py-1.5 text-xs text-[var(--text-muted)] bg-[var(--elevated)]">
				<div class="w-4" aria-hidden="true"></div>
				<div class="flex-1">Source</div>
				<div class="w-24 text-right">Date</div>
				<div class="w-20 text-right">Size</div>
			</div>

			{#each filtered as entry (entry.path)}
				{#if entry.type === 'dir'}
					<!-- Directory row — navigable -->
					<div class="group flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2 last:border-b-0 hover:bg-[var(--surface-hover)] transition-colors">
						<input
							type="checkbox"
							class="h-4 w-4 flex-shrink-0"
							checked={selectedPaths.has(entry.path)}
							onchange={() => togglePath(entry.path)}
							aria-label="Select directory {entry.name}"
						/>
						<button
							type="button"
							class="flex min-w-0 flex-1 items-center truncate text-left text-sm font-medium text-[var(--accent)] hover:underline"
							onclick={() => loadDirectory(entry.path)}
						>
							<span class="mr-1 flex-shrink-0" aria-hidden="true">▸</span>
							{entry.name}
							<span class="text-[var(--text-muted)] font-normal">/</span>
						</button>
						<div class="w-24 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatDate(entry.mod_time)}</div>
						<div class="w-20 text-right text-xs text-[var(--text-muted)]" aria-label="directory">—</div>
					</div>
				{:else}
					{@const supported = isSupportedType(entry)}
					<!-- File row — selectable with signal indicator -->
					<div class={["flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2 last:border-b-0 transition-colors", supported ? 'hover:bg-[var(--surface-hover)]' : 'opacity-40'].join(' ')}>
						<input
							type="checkbox"
							class="h-4 w-4 flex-shrink-0"
							checked={selectedPaths.has(entry.path)}
							disabled={!supported}
							onchange={() => supported && togglePath(entry.path)}
							aria-label="Select file {entry.name}"
						/>
						<!-- Signal dot for file type -->
						<div class="flex min-w-0 flex-1 items-center gap-2 truncate" aria-label={supported ? 'supported file' : 'unsupported file'}>
							<span
								class="flex h-2 w-2 flex-shrink-0 rounded-full"
								class:bg-[var(--success)]={supported}
								class:bg-[var(--border)]={!supported}
								aria-hidden="true"
							></span>
							<span class={["truncate text-sm", supported ? 'text-[var(--text-secondary)]' : 'text-[var(--text-muted)] line-through'].join(' ')}>
								{entry.name}
							</span>
						</div>
						<div class="w-24 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatDate(entry.mod_time)}</div>
						<div class="w-20 text-right text-xs text-[var(--text-muted)]" aria-hidden="true">{formatSize(entry.size)}</div>
					</div>
				{/if}
			{:else}
				<!-- No entries match filter -->
				<div class="px-4 py-6 text-center text-sm text-[var(--text-muted)]">
					No entries match your filter.
					<button
						type="button"
						class="ml-1 text-[var(--accent)] hover:underline"
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
		<div class="border-t border-[var(--border)] flex flex-col-reverse items-center justify-end gap-3 px-4 py-3 sm:flex-row">
			<p class="text-sm text-[var(--text-muted)]">
				<span class="font-mono text-[var(--text-secondary)]">{selectedCount}</span>
				<span class="ml-1">route{selectedCount === 1 ? ' is' : 's are'} queued for match</span>
			</p>
			<Button
				variant="primary"
				disabled={selectedCount === 0 || creating}
				loading={creating}
				onclick={start}
			>
				{creating ? 'Routing…' : 'Next → Match metadata'}
			</Button>
		</div>
	{/if}
</div>
{/if}