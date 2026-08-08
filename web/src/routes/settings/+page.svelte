<script lang="ts">
	import { get, put } from '$lib/api';
	import { PageHeader, Button, Alert, Skeleton } from '$lib/components';
	import type { Settings } from '$lib/types';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';
	import { CheckCircle, AlertTriangle, Save } from '@lucide/svelte';

	let settings = $state<Settings | null>(null);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let saving = $state(false);
	let saved = $state(false);
	let saveError = $state<string | null>(null);

	async function load() {
		await dl.run(async () => {
			settings = await get<Settings>('/settings', {});
		});
	}

	$effect(() => {
		load();
	});

	// Client-side validation helpers
	function validateUrl(value: string): string | null {
		if (!value) return null; // optional field
		try {
			new URL(value);
			return null;
		} catch {
			return 'Must be a valid URL (e.g. https://audiobookdb.org/api)';
		}
	}

	function validateNonNegativeInt(value: number): string | null {
		if (value < 0) return 'Must be zero or greater';
		return null;
	}

	function validateRequiredPath(value: string): string | null {
		if (!value || !value.trim()) return 'This path is required';
		return null;
	}

	let fieldErrors = $state<Record<string, string | null>>({});

	// Derived aria-describedby IDs (Svelte 5 does not allow {#if} inside attributes)
	const inputDirDescId = $derived(fieldErrors.input_dir ? 'input_dir_desc_err' : 'input_dir_desc');
	const outputDirDescId = $derived(fieldErrors.output_dir ? 'output_dir_desc_err' : 'output_dir_desc');
	const numCpusDescId = $derived(fieldErrors.num_cpus ? 'num_cpus_desc_err' : 'num_cpus_desc');
	const apiBaseDescId = $derived(fieldErrors.audiobookdb_base_url ? 'audiobookdb_base_url_desc_err' : 'audiobookdb_base_url_desc');

	function runValidation(): boolean {
		if (!settings) return false;
		fieldErrors = {};
		let valid = true;

		const inputDirErr = validateRequiredPath(settings.input_dir);
		if (inputDirErr) { fieldErrors.input_dir = inputDirErr; valid = false; }

		const outputDirErr = validateRequiredPath(settings.output_dir);
		if (outputDirErr) { fieldErrors.output_dir = outputDirErr; valid = false; }

		const cpusErr = validateNonNegativeInt(settings.num_cpus);
		if (cpusErr) { fieldErrors.num_cpus = cpusErr; valid = false; }

		const urlErr = validateUrl(settings.audiobookdb_base_url ?? '');
		if (urlErr) { fieldErrors.audiobookdb_base_url = urlErr; valid = false; }

		return valid;
	}

	async function save(e: Event) {
		e.preventDefault();
		if (!settings) return;
		if (!runValidation()) return;

		saving = true;
		saved = false;
		saveError = null;
		try {
			await put<Settings>('/settings', settings);
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e) {
			saveError = e instanceof Error ? e.message : 'Failed to save settings';
		} finally {
			saving = false;
		}
	}
</script>

<PageHeader
	title="Settings"
	station="CONTROL CABINET"
	description="Configure directories, processing, and API options."
/>

{#if dl.error && !settings}
	<div class="mb-6 card p-6">
		<div class="flex items-start gap-3">
			<AlertTriangle class="shrink-0 text-[var(--state-red)] mt-0.5" aria-hidden="true" />
			<div class="flex-1">
				<p class="text-sm font-semibold">Unable to load settings</p>
				<p class="text-sm text-[var(--text-muted)] mt-1">We couldn't reach the control cabinet. Press Retry to try again.</p>
				<p class="text-xs text-[var(--text-muted)] mt-2">{dl.error}</p>
			</div>
			<button
				type="button"
				class="shrink-0 text-sm font-bold text-[var(--accent)] hover:underline focus-visible:outline-[var(--accent)]"
				onclick={load}
			>
				Retry
			</button>
		</div>
	</div>
{/if}

{#if saveError}
	<div class="mb-6">
		<Alert variant="error">{saveError}</Alert>
	</div>
{/if}

{#if saved}
	<div class="mb-6 cabinet-feedback cabinet-feedback--success" role="status" aria-live="polite">
		<CheckCircle class="shrink-0" aria-hidden="true" />
		<span>Settings saved.</span>
	</div>
{/if}

{#if dl.showSkeleton}
	<div class="card space-y-4">
		{#each Array(6) as _}
			<Skeleton height="1rem" width="7rem" />
			<Skeleton height="2.75rem" />
		{/each}
	</div>
{:else if settings}
	<form class="cabinet" onsubmit={save} novalidate>
		<!-- Panel A: Paths -->
		<div class="cabinet__panel">
			<div class="cabinet__panel-header">
				<span class="cabinet__panel-label">A</span>
				<span class="cabinet__panel-title">Paths &amp; Directories</span>
			</div>
			<div class="cabinet__grid">
				<div class="cabinet__field">
					<label for="input_dir" class="cabinet__label">Input directory <span class="text-[var(--error)]">*</span></label>
					<input
						id="input_dir"
						type="text"
						bind:value={settings.input_dir}
						aria-invalid={!!fieldErrors.input_dir}
						aria-describedby={inputDirDescId}
					/>
					<p id="input_dir_desc" class="cabinet__help">Where Bragi Books looks for source audiobook files and folders.</p>
					{#if fieldErrors.input_dir}
						<p id="input_dir_desc_err" class="cabinet__field-error" role="alert">{fieldErrors.input_dir}</p>
					{/if}
				</div>

				<div class="cabinet__field">
					<label for="output_dir" class="cabinet__label">Output directory <span class="text-[var(--error)]">*</span></label>
					<input
						id="output_dir"
						type="text"
						bind:value={settings.output_dir}
						aria-invalid={!!fieldErrors.output_dir}
						aria-describedby={outputDirDescId}
					/>
					<p id="output_dir_desc" class="cabinet__help">Where Bragi Books will place finished audiobook files.</p>
					{#if fieldErrors.output_dir}
						<p id="output_dir_desc_err" class="cabinet__field-error" role="alert">{fieldErrors.output_dir}</p>
					{/if}
				</div>

				<div class="cabinet__field">
					<label for="completed_dir" class="cabinet__label">Completed directory</label>
					<input id="completed_dir" type="text" bind:value={settings.completed_dir} />
					<p class="cabinet__help">Leave blank to keep original input files in place.</p>
				</div>

				<div class="cabinet__field">
					<label for="output_scheme" class="cabinet__label">Output path format</label>
					<input id="output_scheme" type="text" bind:value={settings.output_scheme} />
					<div class="cabinet__ref">
						<p class="cabinet__ref-title">Use <code>/</code> for subdirectories.</p>
						<p class="cabinet__ref-text">Supported keywords:</p>
						<p class="cabinet__ref-keywords">asin, author, narrator, series_name, series_position, subtitle, title, year</p>
					</div>
				</div>
			</div>
		</div>

		<!-- Panel B: Processing -->
		<div class="cabinet__panel">
			<div class="cabinet__panel-header">
				<span class="cabinet__panel-label">B</span>
				<span class="cabinet__panel-title">Processing</span>
			</div>
			<div class="cabinet__grid">
				<div class="cabinet__field">
					<label for="m4b_merge_binary" class="cabinet__label">m4b-merge binary</label>
					<input id="m4b_merge_binary" type="text" bind:value={settings.m4b_merge_binary} />
				</div>

				<div class="cabinet__field">
					<label for="num_cpus" class="cabinet__label">CPUs to use</label>
					<input
						id="num_cpus"
						type="number"
						min="0"
						step="1"
						bind:value={settings.num_cpus}
						aria-invalid={!!fieldErrors.num_cpus}
						aria-describedby={numCpusDescId}
					/>
					<p id="num_cpus_desc" class="cabinet__help">Number of parallel processing threads. Use 0 to use all available cores.</p>
					{#if fieldErrors.num_cpus}
						<p id="num_cpus_desc_err" class="cabinet__field-error" role="alert">{fieldErrors.num_cpus}</p>
					{/if}
				</div>
			</div>
		</div>

		<!-- Panel C: API & Region -->
		<div class="cabinet__panel">
			<div class="cabinet__panel-header">
				<span class="cabinet__panel-label">C</span>
				<span class="cabinet__panel-title">API &amp; Region</span>
			</div>
			<div class="cabinet__grid">
				<div class="cabinet__field">
					<label for="region" class="cabinet__label">Region</label>
					<select id="region" bind:value={settings.region}>
						<option value="us">US</option>
						<option value="uk">UK</option>
						<option value="ca">CA</option>
						<option value="au">AU</option>
					</select>
				</div>

				<div class="cabinet__field">
					<label for="audiobookdb_api_key" class="cabinet__label">AudiobookDB API key</label>
					<input id="audiobookdb_api_key" type="password" bind:value={settings.audiobookdb_api_key} placeholder="Optional" />
					<p class="cabinet__help">Optional key for better rate limits on audiobookdb.org.</p>
				</div>

				<div class="cabinet__field cabinet__field--wide">
					<label for="audiobookdb_base_url" class="cabinet__label">AudiobookDB API URL</label>
					<input
						id="audiobookdb_base_url"
						type="url"
						bind:value={settings.audiobookdb_base_url}
						placeholder="https://audiobookdb.org/api"
						aria-invalid={!!fieldErrors.audiobookdb_base_url}
						aria-describedby={apiBaseDescId}
					/>
					<p id="audiobookdb_base_url_desc" class="cabinet__help">Base URL for the AudiobookDB API endpoint.</p>
					{#if fieldErrors.audiobookdb_base_url}
						<p id="audiobookdb_base_url_desc_err" class="cabinet__field-error" role="alert">{fieldErrors.audiobookdb_base_url}</p>
					{/if}
				</div>
			</div>
		</div>

		<!-- Save rail -->
		<div class="cabinet__save-rail">
			{#if saving}
				<span class="cabinet__save-status" aria-live="polite">
					<span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true"></span>
					Saving…
				</span>
			{/if}
			{#if saveError}
				<div class="cabinet__save-error" role="alert">
					<AlertTriangle class="shrink-0 mt-0.5" aria-hidden="true" />
					{saveError}
				</div>
			{/if}
			<div class="cabinet__save-actions">
				<Button variant="primary" type="submit" loading={saving}>
					{#if !saving}
						<Save class="h-4 w-4" aria-hidden="true" />
					{/if}
					Save settings
				</Button>
			</div>
		</div>
	</form>
{/if}

<svelte:head>
	<title>Control Cabinet — Bragi Books</title>
</svelte:head>

<style>
	/* Cabinet — the settings form container */
	.cabinet {
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		overflow: hidden;
		background: var(--surface);
	}

	/* Cabinet panel — grouped section */
	.cabinet__panel {
		border-bottom: 1px solid var(--border-subtle);
	}

	.cabinet__panel:last-of-type {
		border-bottom: none;
	}

	.cabinet__panel-header {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		padding: 0.75rem 1rem;
		background: var(--surface-hover);
		border-bottom: 1px dashed var(--border-subtle);
	}

	.cabinet__panel-label {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.7rem;
		font-weight: 700;
		letter-spacing: 0.05em;
		color: var(--accent);
		background: var(--accent-wash);
		border: 1px solid var(--accent);
		padding: 0.1rem 0.4rem;
		border-radius: var(--radius-sm);
	}

	.cabinet__panel-title {
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--text);
	}

	.cabinet__grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0;
		padding: 0;
	}

	@media (min-width: 640px) {
		.cabinet__grid {
			grid-template-columns: 1fr 1fr;
		}
	}

	/* Cabinet field */
	.cabinet__field {
		padding: 0.875rem 1rem;
		border-bottom: 1px solid var(--border-subtle);
	}

	.cabinet__field:nth-child(even) {
		border-left: 1px solid var(--border-subtle);
	}

	.cabinet__field--wide {
		grid-column: 1 / -1;
	}

	@media (min-width: 640px) {
		.cabinet__field--wide {
			grid-column: 1 / -1;
			border-left: none;
		}
	}

	.cabinet__label {
		display: block;
		margin-bottom: 0.375rem;
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.cabinet__field-error {
		margin-top: 0.35rem;
		font-size: 0.72rem;
		color: var(--error);
		font-weight: 500;
	}

	.cabinet__help {
		margin-top: 0.35rem;
		font-size: 0.72rem;
		color: var(--text-muted);
		line-height: 1.45;
	}

	/* Reference box for output scheme keywords */
	.cabinet__ref {
		margin-top: 0.5rem;
		padding: 0.5rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.cabinet__ref-title {
		font-size: 0.78rem;
		font-weight: 500;
		color: var(--text);
	}

	.cabinet__ref-title code {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.75rem;
		padding: 0.05rem 0.3rem;
		background: var(--elevated);
		border-radius: 2px;
	}

	.cabinet__ref-text {
		margin-top: 0.25rem;
		font-size: 0.72rem;
		color: var(--text-muted);
	}

	.cabinet__ref-keywords {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.68rem;
		color: var(--text-muted);
		line-height: 1.6;
	}

	/* Save rail — bottom action bar */
	.cabinet__save-rail {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.75rem 1rem;
		background: var(--surface);
		border-top: 1px dashed var(--border-subtle);
		flex-wrap: wrap;
	}

	.cabinet__save-status {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.8rem;
		color: var(--info);
		font-weight: 500;
	}

	.cabinet__save-error {
		display: flex;
		align-items: flex-start;
		gap: 0.4rem;
		font-size: 0.8rem;
		color: var(--error);
	}

	.cabinet__save-actions {
		margin-left: auto;
	}

	/* Feedback banner (saved state) */
	.cabinet-feedback {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.6rem 1rem;
		background: var(--success-bg);
		border: 1px solid var(--success);
		border-radius: var(--radius-md);
		font-size: 0.85rem;
		color: var(--success);
		font-weight: 500;
	}

	/* Spinner */
	:global(.animate-spin) {
		animation: spin 1s linear infinite;
	}

	@keyframes spin {
		from { transform: rotate(0deg); }
		to { transform: rotate(360deg); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.animate-spin) {
			animation: none;
		}
	}

	/* Narrow screens */
	@media (max-width: 639px) {
		.cabinet__field {
			border-left: none !important;
		}

		.cabinet__save-rail {
			flex-direction: column;
			align-items: stretch;
		}

		.cabinet__save-actions {
			margin-left: 0;
		}
	}
</style>
