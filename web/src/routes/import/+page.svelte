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
			await loadDirectory(settings.input_dir || '/');
		} catch {
			await loadDirectory('/');
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
			selectedPaths = new Set();
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

	async function start() {
		if (selectedPaths.size === 0) return;
		creating = true;
		dl.setError(null);
		try {
			const payload = { books: Array.from(selectedPaths).map((src_path) => ({ src_path })) };
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
	const canSelect = $derived(filtered.length > 0);
</script>

<PageHeader title="Import" description="Choose source directories or files to match and process." />

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={() => loadDirectory(currentPath)}>{dl.error}</Alert>
	</div>
{/if}

<div class="card">
	<div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
			{#if currentPath !== '/'}
				<button type="button" class="hover:text-[var(--accent)]" onclick={() => loadDirectory(parentPath(currentPath))}>
					← Parent
				</button>
				<span class="text-[var(--border)]">/</span>
			{/if}
			<span class="truncate font-mono text-xs">{currentPath || '/'}</span>
		</div>
		<div class="flex items-center gap-2">
			<div class="flex items-center rounded-md border border-[var(--border-subtle)] text-xs">
				{#each ['name', 'date', 'size'] as field}
					<button
						type="button"
						class={["px-2 py-1.5 transition-colors", sortBy === field ? 'bg-[var(--accent-wash-strong)] text-[var(--accent)] font-medium' : 'text-[var(--text-muted)] hover:text-[var(--text)]'].join(' ')}
						onclick={() => toggleSort(field as 'name' | 'date' | 'size')}
					>
						{field}
						{#if sortBy === field}
							<span class="ml-0.5">{sortDir === 'asc' ? '↑' : '↓'}</span>
						{/if}
					</button>
				{/each}
			</div>
			<input bind:value={search} type="text" placeholder="Filter…" class="w-44" />
		</div>
	</div>

	{#if entries.length === 0 && !loading}
		{#if dl.showSkeleton}
			<div class="space-y-2">
				{#each Array(8) as _}
					<Skeleton height="2.5rem" />
				{/each}
			</div>
		{:else}
			<EmptyState
				title="No source directories found"
				description="The configured input directory is empty. Bragi Books imports whole folders, so place your audiobook directories there first."
				actionLabel="Check input settings"
				actionHref="/settings"
			/>
		{/if}
	{:else}
		<div class="relative rounded-lg border border-[var(--border-subtle)]">
			{#if loading}
				<div class="absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-[var(--surface)]/60 backdrop-blur-[1px]">
					<div class="text-sm text-[var(--text-muted)]">Loading…</div>
				</div>
			{/if}

			<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] bg-[var(--elevated)] px-4 py-2.5 text-sm">
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

			<div class="max-h-[55vh] overflow-auto">
				<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-1.5 text-xs text-[var(--text-muted)]">
					<div class="w-4"></div>
					<div class="flex-1">Name</div>
					<div class="w-24 text-right">Date</div>
					<div class="w-20 text-right">Size</div>
				</div>

				{#each filtered as entry (entry.path)}
					{#if entry.type === 'dir'}
						<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2.5 last:border-b-0 hover:bg-[var(--surface-hover)]">
							<input
								type="checkbox"
								class="h-4 w-4 flex-shrink-0"
								checked={selectedPaths.has(entry.path)}
								onchange={() => togglePath(entry.path)}
								aria-label="Select {entry.name}"
							/>
							<button
								type="button"
								class="flex min-w-0 flex-1 items-center truncate text-left text-sm font-medium text-[var(--text)] hover:text-[var(--accent)]"
								onclick={() => loadDirectory(entry.path)}
							>
								<span class="mr-1 flex-shrink-0 text-[var(--accent)]">▸</span> {entry.name}/
							</button>
							<div class="w-24 text-right text-xs text-[var(--text-muted)]">{formatDate(entry.mod_time)}</div>
							<div class="w-20 text-right text-xs text-[var(--text-muted)]">—</div>
						</div>
					{:else}
						{@const supported = isSupportedType(entry)}
						<div class={["flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2.5 last:border-b-0", supported ? 'hover:bg-[var(--surface-hover)]' : 'opacity-40'].join(' ')}>
							<input
								type="checkbox"
								class="h-4 w-4 flex-shrink-0"
								checked={selectedPaths.has(entry.path)}
								disabled={!supported}
								onchange={() => supported && togglePath(entry.path)}
								aria-label="Select {entry.name}"
							/>
							<span class={["flex min-w-0 flex-1 items-center truncate text-sm", supported ? 'text-[var(--text-secondary)]' : 'text-[var(--text-muted)] line-through'].join(' ')}>
								<span class="mr-1 flex-shrink-0 text-[var(--text-muted)]">·</span> {entry.name}
							</span>
							<div class="w-24 text-right text-xs text-[var(--text-muted)]">{formatDate(entry.mod_time)}</div>
							<div class="w-20 text-right text-xs text-[var(--text-muted)]">{formatSize(entry.size)}</div>
						</div>
					{/if}
				{:else}
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
		</div>
	{/if}

	{#if entries.length > 0}
		<div class="mt-6 flex flex-col-reverse items-center justify-end gap-3 sm:flex-row">
			<p class="text-sm text-[var(--text-muted)]">
				{selectedPaths.size} selected
			</p>
			<Button variant="primary" disabled={selectedPaths.size === 0 || creating} loading={creating} onclick={start}>
				Next: Match metadata
			</Button>
		</div>
	{/if}
</div>
