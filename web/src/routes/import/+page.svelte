<script lang="ts">
	import { get, post } from '$lib/api';
	import { goto } from '$app/navigation';
	import type { DirectoryEntry, DirectoriesResponse, Settings, CreateBooksResponse } from '$lib/types';
	import { PageHeader, Alert, Button, Skeleton, EmptyState } from '$lib/components';

	let currentPath = $state('');
	let entries: DirectoryEntry[] = $state([]);
	let filtered: DirectoryEntry[] = $state([]);
	let selectedPaths = $state<Set<string>>(new Set());
	let loading = $state(false);
	let creating = $state(false);
	let error = $state<string | null>(null);
	let search = $state('');

	async function initPath() {
		try {
			const settings = await get<Settings>('/settings', {});
			await loadDirectory(settings.input_dir || '/');
		} catch {
			await loadDirectory('/');
		}
	}

	async function loadDirectory(path: string) {
		loading = true;
		error = null;
		try {
			const response = await get<DirectoriesResponse>(`/directories?path=${encodeURIComponent(path)}`);
			entries = response.entries || [];
			currentPath = response.path || path;
			filterEntries();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to list directory';
			entries = [];
		} finally {
			loading = false;
		}
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
		error = null;
		try {
			const payload = { books: Array.from(selectedPaths).map((src_path) => ({ src_path })) };
			await post<CreateBooksResponse>('/books', payload);
			await goto('/match');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to create books';
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
</script>

<PageHeader title="Import" description="Choose source directories or files to match and process." />

{#if error}
	<div class="mb-6">
		<Alert variant="error" onretry={() => loadDirectory(currentPath)}>{error}</Alert>
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

	{#if loading}
		<div class="space-y-2">
			{#each Array(8) as _}
				<Skeleton height="2.5rem" />
			{/each}
		</div>
	{:else if entries.length === 0}
		<EmptyState
			title="Directory is empty"
			description="This directory has no files or folders that Bragi Books can see."
		/>
	{:else}
		<div class="rounded-lg border border-[var(--border-subtle)]">
			<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] bg-[var(--elevated)] px-4 py-2.5 text-sm">
				<input type="checkbox" checked={allSelected} onchange={toggleSelectAll} aria-label="Select all" />
				<span class="text-[var(--text-muted)]">Select all visible</span>
			</div>
			<div class="max-h-[55vh] overflow-auto">
				{#each filtered as entry (entry.path)}
					<div class="flex items-center gap-3 border-b border-[var(--border-subtle)] px-4 py-2.5 last:border-b-0 hover:bg-[var(--surface-hover)]">
						<input
							type="checkbox"
							checked={selectedPaths.has(entry.path)}
							onchange={() => togglePath(entry.path)}
							aria-label="Select {entry.name}"
						/>
						<div class="flex min-w-0 flex-1 items-center gap-2">
							{#if entry.type === 'dir'}
								<button
									type="button"
									class="truncate text-left text-sm font-medium text-[var(--text)] hover:text-[var(--accent)]"
									onclick={() => loadDirectory(entry.path)}
								>
									<span class="mr-1 text-[var(--accent)]">▸</span> {entry.name}/
								</button>
							{:else}
								<span class="truncate text-sm text-[var(--text-secondary)]">
									<span class="mr-1 text-[var(--text-muted)]">·</span> {entry.name}
								</span>
							{/if}
						</div>
					</div>
				{:else}
					<div class="px-4 py-6 text-center text-sm text-[var(--text-muted)]">No entries match your filter.</div>
				{/each}
			</div>
		</div>
	{/if}

	<div class="mt-6 flex flex-col-reverse items-center justify-end gap-3 sm:flex-row">
		<p class="text-sm text-[var(--text-muted)]">
			{selectedPaths.size} selected
		</p>
		<Button variant="primary" disabled={selectedPaths.size === 0 || creating} loading={creating} onclick={start}>
			Next: Match metadata
		</Button>
	</div>
</div>
