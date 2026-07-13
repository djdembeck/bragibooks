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
		await dl.run(async () => {
			entries = [];
			filtered = [];
			selectedPaths = new Set();
			const response = await get<DirectoriesResponse>(`/directories?path=${encodeURIComponent(path)}`);
			entries = validEntries(response.entries || []);
			currentPath = response.path || path;
			filterEntries();
		});
	}

	function filterEntries() {
		const q = search.trim().toLowerCase();
		if (!q) {
			filtered = entries;
			return;
		}
		filtered = entries.filter((e) => e.name.toLowerCase().includes(q));
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
		filterEntries();
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
	<div class="mb-4 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
		<div class="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
			{#if currentPath !== '/'}
				<button type="button" class="hover:text-[var(--accent)]" onclick={() => loadDirectory(parentPath(currentPath))}>
					← Parent
				</button>
				<span class="text-[var(--border)]">/</span>
			{/if}
			<span class="truncate font-mono text-xs">{currentPath || '/'}</span>
		</div>
		<div class="relative">
			<input bind:value={search} type="text" placeholder="Filter entries…" class="w-full sm:w-72" />
		</div>
	</div>

	{#if dl.showSkeleton}
		<div class="space-y-2">
			{#each Array(8) as _}
				<Skeleton height="2.5rem" />
			{/each}
		</div>
	{:else if entries.length === 0}
		<EmptyState
			title="No source directories found"
			description="The configured input directory is empty. Bragi Books imports whole folders, so place your audiobook directories there first."
			actionLabel="Check input settings"
			actionHref="/settings"
		/>
	{:else}
		<div class="rounded-lg border border-[var(--border-subtle)]">
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
				{#each filtered as entry (entry.path)}
					<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2.5 last:border-b-0 hover:bg-[var(--surface-hover)]">
						<input
							type="checkbox"
							class="h-4 w-4 flex-shrink-0"
							checked={selectedPaths.has(entry.path)}
							onchange={() => togglePath(entry.path)}
							aria-label="Select {entry.name}"
						/>
						{#if entry.type === 'dir'}
							<button
								type="button"
								class="w-full truncate text-left text-sm font-medium text-[var(--text)] hover:text-[var(--accent)]"
								onclick={() => loadDirectory(entry.path)}
							>
								<span class="mr-1 text-[var(--accent)]">▸</span> {entry.name}/
							</button>
						{:else}
							<span class="w-full truncate text-sm text-[var(--text-secondary)]">
								<span class="mr-1 text-[var(--text-muted)]">·</span> {entry.name}
							</span>
						{/if}
					</div>
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
