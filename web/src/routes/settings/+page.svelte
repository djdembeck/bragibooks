<script lang="ts">
	import { get, put } from '$lib/api';
	import type { Settings } from '$lib/types';

	let settings = $state<Settings | null>(null);
	let saving = $state(false);
	let saved = $state(false);
	let error = $state<string | null>(null);

	async function loadSettings() {
		try {
			settings = await get<Settings>('/settings', {});
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load settings';
		}
	}

	async function saveSettings() {
		if (!settings) return;
		try {
			saving = true;
			saved = false;
			error = null;
			await put<Settings>('/settings', settings);
			saved = true;
			setTimeout(() => saved = false, 2000);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to save settings';
		} finally {
			saving = false;
		}
	}

	$effect(() => {
		loadSettings();
	});
</script>

<div class="p-6 max-w-6xl mx-auto">
	<nav class="flex gap-4 mb-8">
		<a href="/" class="text-[var(--text)] hover:text-[var(--accent)]">Dashboard</a>
		<a href="/books" class="text-[var(--text)] hover:text-[var(--accent)]">Books</a>
		<a href="/import" class="text-[var(--text)] hover:text-[var(--accent)]">Import</a>
		<a href="/process" class="text-[var(--text)] hover:text-[var(--accent)]">Processing</a>
		<a href="/settings" class="text-[var(--accent)] font-semibold">Settings</a>
	</nav>

	<h1 class="text-3xl font-bold mb-2">Settings</h1>
	<p class="text-[var(--text-muted)] mb-6">Configure directories and processing options.</p>

	{#if error}
		<p class="text-red-400 mb-4">{error}</p>
	{/if}

	{#if saved}
		<p class="text-green-400 mb-4">Settings saved!</p>
	{/if}

	{#if settings}
		<form class="space-y-4 max-w-lg" onsubmit={(e) => { e.preventDefault(); saveSettings(); }}>
			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">Input Directory</label>
				<input
					type="text"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.inputDir}
				/>
			</div>

			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">Output Directory</label>
				<input
					type="text"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.outputDir}
				/>
			</div>

			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">Completed Directory</label>
				<input
					type="text"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.completedDir}
				/>
			</div>

			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">m4b-merge Binary</label>
				<input
					type="text"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.m4bMergeBinary}
				/>
			</div>

			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">CPU Count</label>
				<input
					type="number"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.numCpus}
				/>
			</div>

			<div>
				<label class="block text-[var(--text-muted)] text-sm mb-1">Output Path Format</label>
				<input
					type="text"
					class="w-full bg-[var(--surface)] border border-[var(--border)] rounded p-2 text-[var(--text)]"
					bind:value={settings.outputScheme}
				/>
			</div>

			<button
				type="submit"
				class="px-4 py-2 bg-[var(--accent)] text-[var(--bg)] rounded font-semibold hover:bg-[var(--accent-hover)] disabled:opacity-50"
				disabled={saving}
			>
				{saving ? 'Saving...' : 'Save Changes'}
			</button>
		</form>
	{:else}
		<p class="text-[var(--text-muted)]">Loading settings...</p>
	{/if}
</div>