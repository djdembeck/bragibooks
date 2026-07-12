<script lang="ts">
	import { get, put } from '$lib/api';
	import type { Settings } from '$lib/types';
	import { PageHeader, Button, Alert, Skeleton } from '$lib/components';

	let settings = $state<Settings | null>(null);
	let loading = $state(true);
	let saving = $state(false);
	let saved = $state(false);
	let error = $state<string | null>(null);

	async function load() {
		loading = true;
		error = null;
		try {
			settings = await get<Settings>('/settings', {});
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load settings';
		} finally {
			loading = false;
		}
	}

	async function save(e: Event) {
		e.preventDefault();
		if (!settings) return;
		saving = true;
		saved = false;
		error = null;
		try {
			await put<Settings>('/settings', settings);
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to save settings';
		} finally {
			saving = false;
		}
	}

	$effect(() => {
		load();
	});
</script>

<PageHeader title="Settings" description="Configure directories, processing, and API options." />

{#if error}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>{error}</Alert>
	</div>
{/if}

{#if saved}
	<div class="mb-6">
		<Alert variant="success">Settings saved.</Alert>
	</div>
{/if}

{#if loading || !settings}
	<div class="card space-y-4">
		{#each Array(6) as _}
			<Skeleton height="1rem" width="7rem" />
			<Skeleton height="2.75rem" />
		{/each}
	</div>
{:else}
	<form class="card space-y-5" onsubmit={save}>
		<div class="grid gap-5 sm:grid-cols-2">
			<div>
				<label for="input_dir" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">Input directory</label>
				<input id="input_dir" type="text" bind:value={settings.input_dir} />
				<p class="mt-1 text-xs text-[var(--text-muted)]">Where Bragi Books looks for source audiobook files and folders.</p>
			</div>

			<div>
				<label for="output_dir" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">Output directory</label>
				<input id="output_dir" type="text" bind:value={settings.output_dir} />
			</div>

			<div>
				<label for="completed_dir" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">Completed directory</label>
				<input id="completed_dir" type="text" bind:value={settings.completed_dir} />
				<p class="mt-1 text-xs text-[var(--text-muted)]">Leave blank to keep original input files in place.</p>
			</div>

			<div>
				<label for="m4b_merge_binary" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">m4b-merge binary</label>
				<input id="m4b_merge_binary" type="text" bind:value={settings.m4b_merge_binary} />
			</div>
		</div>

		<div class="grid gap-5 sm:grid-cols-2">
			<div>
				<label for="num_cpus" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">CPUs to use</label>
				<input id="num_cpus" type="number" min="0" bind:value={settings.num_cpus} />
				<p class="mt-1 text-xs text-[var(--text-muted)]">0 uses all available cores.</p>
			</div>

			<div>
				<label for="region" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">Region</label>
				<select id="region" bind:value={settings.region}>
					<option value="us">US</option>
					<option value="uk">UK</option>
					<option value="ca">CA</option>
					<option value="au">AU</option>
				</select>
			</div>
		</div>

		<div>
			<label for="output_scheme" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">Output path format</label>
			<input id="output_scheme" type="text" bind:value={settings.output_scheme} />
			<div class="mt-2 rounded-md border border-[var(--border-subtle)] bg-[var(--bg)] p-3 text-sm text-[var(--text-secondary)]">
				<p class="font-medium text-[var(--text)]">Use / for subdirectories.</p>
				<p class="mt-1 text-[var(--text-muted)]">Supported keywords:</p>
				<p class="font-mono text-xs text-[var(--text-muted)]">asin, author, narrator, series_name, series_position, subtitle, title, year</p>
			</div>
		</div>


		<div>
			<label for="audiobookdb_api_key" class="mb-1 block text-sm font-medium text-[var(--text-secondary)]">AudiobookDB API key</label>
			<input id="audiobookdb_api_key" type="password" bind:value={settings.audiobookdb_api_key} placeholder="Optional" />
			<p class="mt-1 text-xs text-[var(--text-muted)]">Optional key for better rate limits on audiobookdb.org.</p>
		</div>

		<div class="flex justify-end pt-2">
			<Button variant="primary" type="submit" loading={saving}>Save settings</Button>
		</div>
	</form>
{/if}
