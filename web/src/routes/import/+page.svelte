<script lang="ts">
	import { get } from '$lib/api';
	import type { DirectoryEntry, DirectoriesResponse } from '$lib/types';

	let currentPath = $state('');
	let entries: DirectoryEntry[] = $state([]);
	let selectedPaths: string[] = $state([]);
	let loading = $state(false);
	let error = $state<string | null>(null);

	async function loadDirectory(path: string) {
		try {
			loading = true;
			error = null;
			const response = await get<DirectoriesResponse>(`/directories?path=${encodeURIComponent(path)}`, {});
			entries = response.entries || [];
			currentPath = path;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to list directory';
			entries = [];
		} finally {
			loading = false;
		}
	}

	function toggleSelect(path: string) {
		if (selectedPaths.includes(path)) {
			selectedPaths = selectedPaths.filter(p => p !== path);
		} else {
			selectedPaths.push(path);
		}
	}

	$effect(() => {
		// Load settings to get default input dir
		get('/settings', {}).then((settings: any) => {
			if (settings.inputDir) {
				loadDirectory(settings.inputDir);
			}
		}).catch(() => {
			loadDirectory('/');
		});
	});
</script>

<div class="p-6 max-w-6xl mx-auto">
	<nav class="flex gap-4 mb-8">
		<a href="/" class="text-[var(--text)] hover:text-[var(--accent)]">Dashboard</a>
		<a href="/books" class="text-[var(--text)] hover:text-[var(--accent)]">Books</a>
		<a href="/import" class="text-[var(--accent)] font-semibold">Import</a>
		<a href="/process" class="text-[var(--text)] hover:text-[var(--accent)]">Processing</a>
		<a href="/settings" class="text-[var(--text)] hover:text-[var(--accent)]">Settings</a>
	</nav>

	<h1 class="text-3xl font-bold mb-2">Import</h1>
	<p class="text-[var(--text-muted)] mb-6">Browse for audiobook source directories and match them with audiobookdb metadata.</p>

	<div class="mb-4">
		<div class="flex items-center gap-2">
			{#if currentPath}
				<button class="text-[var(--text)] hover:text-[var(--accent)]" onclick={() => loadDirectory(currentPath.substring(0, currentPath.lastIndexOf('/')))}>
					📁 Parent
				</button>
				<span class="text-[var(--text-muted)]">›</span>
			{/if}
			<span class="text-[var(--text-muted)]">{currentPath || '/'}</span>
		</div>
	</div>

	{#if loading}
		<p class="text-[var(--text-muted)]">Loading...</p>
	{:else if error}
		<p class="text-red-400">{error}</p>
	{:else}
		<div class="space-y-1">
			{#each entries as entry}
				<div class="flex items-center gap-2 p-2 rounded hover:bg-[var(--surface)]">
					{#if entry.type === 'dir'}
						<button class="text-[var(--text)]" onclick={() => loadDirectory(entry.path)}>📁 {entry.name}/</button>
						<input type="checkbox" checked={selectedPaths.includes(entry.path)} onchange={() => toggleSelect(entry.path)} />
					{:else}
						<span class="text-[var(--text-muted)]">📄 {entry.name}</span>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	{#if selectedPaths.length > 0}
		<div class="mt-6 p-4 bg-[var(--surface)] rounded">
			<p class="font-semibold mb-2">Selected: {selectedPaths.length} directories</p>
			<p class="text-[var(--text-muted)] text-sm">Next step: search audiobookdb for matching metadata.</p>
		</div>
	{/if}
</div>