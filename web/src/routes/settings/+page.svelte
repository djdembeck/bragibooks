<script lang="ts">
	import { get, post, put } from '$lib/api';
	import { PageHeader, Button, Alert, ErrorDetail, Skeleton, Spinner, DirPicker, Modal, PathFormatBuilder } from '$lib/components';
	import { parseParts, validateScheme } from '$lib/scheme';
	import type { Settings } from '$lib/types';
	import { delayedLoad, type DelayedLoadState } from '$lib/delayedLoad.svelte';
	import { toCleanedError, type CleanedAdbError } from '$lib/adbError';
	import { Save, FolderOpen, Check, Copy, Plus } from '@lucide/svelte';

	type PathField = 'input_dir' | 'output_dir' | 'completed_dir';

	let settings = $state<Settings | null>(null);
	let pickerOpen = $state(false);
	let pickerField = $state<PathField>('input_dir');

	const pickerTitles: Record<PathField, string> = {
		input_dir: 'Choose input directory',
		output_dir: 'Choose output directory',
		completed_dir: 'Choose completed directory'
	};

	function openPicker(field: PathField) {
		pickerField = field;
		pickerOpen = true;
	}

	function onPick(path: string) {
		if (!settings) return;
		settings[pickerField] = path;
		pickerOpen = false;
	}
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });
	let saving = $state(false);
	let saved = $state(false);
	let saveError = $state<CleanedAdbError | null>(null);

	async function load() {
		await dl.run(async () => {
			settings = await get<Settings>('/settings', {});
		});
	}

	$effect(() => {
		load();
	});

	// Client-side validation helpers
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

		const schemeErr = validateScheme(parseParts(settings.output_scheme)).errors[0];
		if (schemeErr) { fieldErrors.output_scheme = schemeErr; valid = false; }

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
			const updated = await put<Settings>('/settings', settings);
			if (updated) {
				settings.bragibooks_version = updated.bragibooks_version;
				settings.m4b_merge_version = updated.m4b_merge_version;
			}
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e) {
			saveError = toCleanedError(e);
		} finally {
			saving = false;
		}
	}

	// External API (remote key auth). The key is server-generated and
	// shown once in the modal; this page never stores it.
	type ApiAction = 'enable' | 'disable' | 'reissue';
	type ApiEndpointResponse = { enabled: boolean; api_key: string };

	let apiBusy = $state<ApiAction | null>(null);
	let apiError = $state<CleanedAdbError | null>(null);
	let lastApiAction = $state<ApiAction | null>(null);
	let pendingApiAction = $state<'reissue' | 'disable' | null>(null);
	let apiKeyModalKey = $state<string | null>(null);
	let apiKeyModalOpen = $state(false);
	let keyCopied = $state(false);
	let closeWarning = $state(false);
	let copyState = $state<'idle' | 'copied' | 'failed'>('idle');
	let copyTimer: ReturnType<typeof setTimeout> | undefined;
	let keyCodeEl = $state<HTMLElement | undefined>();
	let wasApiKeyModalOpen = false;
	// Same-origin API base; filled after mount because the origin is only
	// known in the browser.
	let apiBase = $state('/api');

	$effect(() => {
		apiBase = `${location.origin}/api`;
	});

	function applyApiEndpointState(enabled: boolean) {
		if (!settings) return;
		settings.api_endpoint_enabled = enabled;
	}

	async function runApiAction(action: ApiAction) {
		if (!settings) return;
		apiBusy = action;
		apiError = null;
		lastApiAction = action;
		try {
			if (action === 'disable') {
				const res = await post<{ enabled: boolean }>('/settings/api-endpoint/disable', {});
				applyApiEndpointState(res.enabled);
				requestAnimationFrame(() => {
					(document.getElementById('api-enable') ?? document.getElementById('settings-external-api'))?.focus();
				});
			} else {
				const path =
					action === 'enable'
						? '/settings/api-endpoint/enable'
						: '/settings/api-endpoint/regenerate';
				const res = await post<ApiEndpointResponse>(path, {});
				applyApiEndpointState(res.enabled);
				showApiKey(res.api_key);
			}
			pendingApiAction = null;
		} catch (e) {
			apiError = toCleanedError(e);
		} finally {
			apiBusy = null;
		}
	}

	function enableExternalAPI() {
		void runApiAction('enable');
	}

	function regenerateExternalAPI() {
		void runApiAction('reissue');
	}

	function disableExternalAPI() {
		void runApiAction('disable');
	}

	function retryApiAction() {
		if (lastApiAction) void runApiAction(lastApiAction);
	}

	function cancelPendingApiAction() {
		const previous = pendingApiAction;
		pendingApiAction = null;
		requestAnimationFrame(() => {
			document
				.getElementById(previous === 'disable' ? 'api-disable' : 'api-reissue')
				?.focus();
		});
	}

	// The clicked action button unmounts when the inline confirmation
	// replaces it; move focus to the confirmation's primary action so
	// keyboard users are not dropped back to the document.
	$effect(() => {
		if (!pendingApiAction) return;
		const id = pendingApiAction === 'reissue' ? 'api-confirm-reissue' : 'api-confirm-disable';
		requestAnimationFrame(() => document.getElementById(id)?.focus());
	});

	function showApiKey(key: string) {
		apiKeyModalKey = key;
		keyCopied = false;
		closeWarning = false;
		copyState = 'idle';
		apiKeyModalOpen = true;
	}

	function selectKey() {
		const el = keyCodeEl;
		if (!el) return;
		const range = document.createRange();
		range.selectNodeContents(el);
		const selection = window.getSelection();
		selection?.removeAllRanges();
		selection?.addRange(range);
		el.focus({ preventScroll: true });
	}

	async function copyApiKey() {
		const key = apiKeyModalKey;
		if (!key) return;
		clearTimeout(copyTimer);
		try {
			if (navigator.clipboard?.writeText) {
				await navigator.clipboard.writeText(key);
			} else {
				// Plain-HTTP LAN origins have no Clipboard API. Fall back to
				// a temporary textarea so the key is still captured.
				// showModal() makes everything OUTSIDE the dialog inert, so
				// the textarea must be created inside the open <dialog>
				// (Modal.svelte renders the dialog element) or it cannot be
				// focused and the execCommand selection silently fails;
				// fall back to document.body only when no dialog is open.
				const host =
					(document.querySelector('dialog[open]') as HTMLElement | null) ??
					document.body;
				const ta = document.createElement('textarea');
				ta.value = key;
				ta.setAttribute('readonly', '');
				ta.style.position = 'absolute';
				ta.style.opacity = '0';
				host.appendChild(ta);
				try {
					ta.select();
					ta.setSelectionRange(0, key.length);
					const ok = document.execCommand('copy');
					if (!ok) throw new Error('copy was rejected');
				} finally {
					ta.remove();
				}
			}
			keyCopied = true;
			copyState = 'copied';
		} catch {
			copyState = 'failed';
			selectKey();
		}
		copyTimer = setTimeout(() => {
			copyState = 'idle';
		}, 4000);
	}

	// Closing is the only way to lose the key, so it is gated until the
	// key has been copied (or the operator explicitly overrides).
	function confirmKeyClose(): boolean {
		if (keyCopied) return true;
		closeWarning = true;
		return false;
	}

	function closeKeyModal() {
		if (!keyCopied) {
			closeWarning = true;
			return;
		}
		apiKeyModalOpen = false;
	}

	function forceCloseKeyModal() {
		keyCopied = false;
		closeWarning = false;
		apiKeyModalOpen = false;
	}

	// Reveal-once: clear the on-screen key whenever the modal is not shown,
	// and return focus to the control that issued it.
	$effect(() => {
		if (apiKeyModalOpen) {
			wasApiKeyModalOpen = true;
			return;
		}
		apiKeyModalKey = null;
		copyState = 'idle';
		if (!wasApiKeyModalOpen) return;
		wasApiKeyModalOpen = false;
		requestAnimationFrame(() => {
			(document.getElementById('api-reissue') ??
				document.getElementById('api-enable') ??
				document.getElementById('settings-external-api'))?.focus();
		});
	});

	// Native dialog focuses its first focusable element (Close). Put focus
	// on the safe action instead once the dialog is on screen.
	$effect(() => {
		if (!apiKeyModalOpen) return;
		requestAnimationFrame(() => document.getElementById('api-key-copy')?.focus());
	});

	const apiEnabled = $derived(settings?.api_endpoint_enabled === true);
	const apiEnvManaged = $derived(settings?.api_endpoint_env_managed === true);
</script>

<PageHeader
	title="Settings"
	description="Configure directories, processing, and API options."
/>

{#if dl.error && !settings}
	<div class="mb-6">
		<Alert variant="error" onretry={load}>
			<span class="font-medium">Unable to load settings</span>
			<span class="block text-xs opacity-80 mt-1">{dl.error.message}</span>
			<ErrorDetail raw={dl.error.raw} />
		</Alert>
	</div>
{/if}

{#if saveError}
	<div class="mb-6">
		<Alert variant="error">
			{saveError.message}
			<ErrorDetail raw={saveError.raw} />
		</Alert>
	</div>
{/if}

{#if saved}
	<div class="mb-6" role="status" aria-live="polite">
		<Alert variant="success">Settings saved.</Alert>
	</div>
{/if}

{#if dl.showSkeleton}
	<div class="mx-auto max-w-3xl space-y-4" aria-label="Loading settings">
		{#each Array(6) as _}
			<Skeleton height="1rem" width="7rem" />
			<Skeleton height="2.75rem" />
		{/each}
	</div>
{:else if settings}
	<form class="settings-form mx-auto max-w-3xl space-y-6" onsubmit={save} novalidate>
		<!-- Paths & directories -->
		<section class="settings-section" aria-labelledby="settings-paths">
			<h2 class="settings-section__title" id="settings-paths">Paths &amp; directories</h2>
			<div class="settings-grid">
				<div class="settings-field">
					<label for="input_dir" class="settings-label">Input directory <span class="text-[var(--state-red)]">*</span></label>
					<div class="settings-input-row">
						<input
							id="input_dir"
							type="text"
							bind:value={settings.input_dir}
							aria-invalid={!!fieldErrors.input_dir}
							aria-describedby={inputDirDescId}
						/>
						<Button variant="secondary" class="shrink-0" onclick={() => openPicker('input_dir')}>
							<FolderOpen class="h-4 w-4" aria-hidden="true" />
							Browse
						</Button>
					</div>
					<p id="input_dir_desc" class="settings-help">Where Bragi Books looks for source audiobook files and folders.</p>
					{#if fieldErrors.input_dir}
						<p id="input_dir_desc_err" class="settings-field-error" role="alert">{fieldErrors.input_dir}</p>
					{/if}
				</div>

				<div class="settings-field">
					<label for="output_dir" class="settings-label">Output directory <span class="text-[var(--state-red)]">*</span></label>
					<div class="settings-input-row">
						<input
							id="output_dir"
							type="text"
							bind:value={settings.output_dir}
							aria-invalid={!!fieldErrors.output_dir}
							aria-describedby={outputDirDescId}
						/>
						<Button variant="secondary" class="shrink-0" onclick={() => openPicker('output_dir')}>
							<FolderOpen class="h-4 w-4" aria-hidden="true" />
							Browse
						</Button>
					</div>
					<p id="output_dir_desc" class="settings-help">Where Bragi Books will place finished audiobook files.</p>
					{#if fieldErrors.output_dir}
						<p id="output_dir_desc_err" class="settings-field-error" role="alert">{fieldErrors.output_dir}</p>
					{/if}
				</div>

				<div class="settings-field">
					<label for="completed_dir" class="settings-label">Completed directory</label>
					<div class="settings-input-row">
						<input id="completed_dir" type="text" bind:value={settings.completed_dir} />
						<Button variant="secondary" class="shrink-0" onclick={() => openPicker('completed_dir')}>
							<FolderOpen class="h-4 w-4" aria-hidden="true" />
							Browse
						</Button>
					</div>
					<p class="settings-help">Leave blank to keep original input files in place.</p>
				</div>

				<div class="settings-field" role="group" aria-labelledby="output_scheme_label">
					<!-- svelte-ignore a11y_label_has_associated_control: labels the composite builder group via aria-labelledby -->
					<label id="output_scheme_label" class="settings-label">Output path format <span class="text-[var(--state-red)]">*</span></label>
					<PathFormatBuilder bind:value={settings.output_scheme} outputDir={settings.output_dir} />
					<p class="settings-help">Tap a part above to add it. Within a part, Arrows move, Delete removes, Alt+Arrows reorders.</p>
					{#if fieldErrors.output_scheme}
						<p class="settings-field-error" role="alert">{fieldErrors.output_scheme}</p>
					{/if}
				</div>
			</div>
		</section>

		<!-- Processing -->
		<section class="settings-section" aria-labelledby="settings-processing">
			<h2 class="settings-section__title" id="settings-processing">Processing</h2>
			<div class="settings-grid">
				<div class="settings-field">
					<label for="m4b_merge_binary" class="settings-label">m4b-merge binary</label>
					<input id="m4b_merge_binary" type="text" bind:value={settings.m4b_merge_binary} />
				</div>

				<div class="settings-field">
					<label for="num_cpus" class="settings-label">CPUs to use</label>
					<input
						id="num_cpus"
						type="number"
						min="0"
						step="1"
						bind:value={settings.num_cpus}
						aria-invalid={!!fieldErrors.num_cpus}
						aria-describedby={numCpusDescId}
					/>
					<p id="num_cpus_desc" class="settings-help">Number of parallel processing threads. Use 0 to use all available cores.</p>
					{#if fieldErrors.num_cpus}
						<p id="num_cpus_desc_err" class="settings-field-error" role="alert">{fieldErrors.num_cpus}</p>
					{/if}
				</div>
			</div>
		</section>

		<!-- AudiobookDB -->
		<section class="settings-section" aria-labelledby="settings-audiobookdb">
			<h2 class="settings-section__title" id="settings-audiobookdb">AudiobookDB</h2>
			<div class="settings-field">
				<label for="audiobookdb_api_key" class="settings-label">API key</label>
				<input id="audiobookdb_api_key" type="password" bind:value={settings.audiobookdb_api_key} placeholder="Optional" />
				<p class="settings-help">Optional key for better rate limits on audiobookdb.org.</p>
				<details class="apikey-help">
					<summary class="apikey-help__summary">How to get an API key</summary>
					<ol class="apikey-help__steps">
						<li>
							Create a free account at
							<a href="https://audiobookdb.org/users/signup" target="_blank" rel="noopener noreferrer">audiobookdb.org</a>
							(Sign In → Register).
						</li>
						<li>Open your account settings once signed in.</li>
						<li>Generate an API key — it is shown once, so copy it immediately.</li>
						<li>Paste it into the field above and save.</li>
					</ol>
					<p class="apikey-help__note">
						Anonymous access (no key) is limited to about 5 requests/second and 500/day;
						a free key raises this 20×. If matching suddenly fails with “rate limit
						exceeded”, this is why.
					</p>
				</details>
			</div>
		</section>

		<!-- External API -->
		<section class="settings-section" aria-labelledby="settings-external-api">
			<h2 class="settings-section__title" id="settings-external-api" tabindex="-1">External API</h2>
			<div class="settings-field">
				<p class="settings-help settings-help--status" role="status">
					{#if apiEnvManaged}
						<strong>{apiEnabled ? 'On' : 'Off'}</strong> — fixed by the
						<code>SERVER_API_ENDPOINT_ENABLED</code> environment variable. Unset it to manage
						this setting here.
					{:else if apiEnabled}
						<strong>On</strong> — other machines can use the API with the key. The web interface
						keeps working on your local network.
					{:else}
						<strong>Off</strong> — remote access is disabled; the web interface still works on
						your local network.
					{/if}
				</p>
				{#if apiError}
					<div class="mb-3">
						<Alert variant="error" onretry={retryApiAction}>
							<span class="font-medium">Could not update the external API.</span>
							<span class="block text-xs opacity-80 mt-1">{apiError.message}</span>
						</Alert>
					</div>
				{/if}
				{#if pendingApiAction === 'reissue'}
					<div class="settings-confirm" role="alert">
						<p class="settings-confirm__text">
							Reissue the key? The current key stops working immediately; every external tool
							using it will need the new one.
						</p>
						<div class="settings-confirm__actions">
							<Button
								id="api-confirm-reissue"
								variant="danger"
								loading={apiBusy === 'reissue'}
								disabled={apiBusy !== null}
								onclick={regenerateExternalAPI}
							>
								Yes, reissue key
							</Button>
							<Button
								variant="ghost"
								disabled={apiBusy !== null}
								onclick={cancelPendingApiAction}
							>
								Cancel
							</Button>
						</div>
					</div>
				{:else if pendingApiAction === 'disable'}
					<div class="settings-confirm" role="alert">
						<p class="settings-confirm__text">
							Disable remote access and delete the stored key? External tools lose access
							until you enable the API and issue a new key.
						</p>
						<div class="settings-confirm__actions">
							<Button
								id="api-confirm-disable"
								variant="danger"
								loading={apiBusy === 'disable'}
								disabled={apiBusy !== null}
								onclick={disableExternalAPI}
							>
								Yes, disable
							</Button>
							<Button
								variant="ghost"
								disabled={apiBusy !== null}
								onclick={cancelPendingApiAction}
							>
								Cancel
							</Button>
						</div>
					</div>
				{:else}
					<div class="settings-input-row">
						{#if apiEnabled}
							<Button
								id="api-reissue"
								variant="secondary"
								loading={apiBusy === 'reissue'}
								disabled={apiBusy !== null}
								onclick={() => (pendingApiAction = 'reissue')}
							>
								Reissue key
							</Button>
							{#if !apiEnvManaged}
								<Button
									id="api-disable"
									variant="ghost"
									disabled={apiBusy !== null}
									onclick={() => (pendingApiAction = 'disable')}
								>
									Disable
								</Button>
							{/if}
						{:else if !apiEnvManaged}
							<Button
								id="api-enable"
								variant="secondary"
								loading={apiBusy === 'enable'}
								disabled={apiBusy !== null}
								onclick={enableExternalAPI}
							>
								<Plus class="h-4 w-4" aria-hidden="true" />
								Enable external API
							</Button>
						{/if}
					</div>
				{/if}
				<p class="settings-help">
					For scripts, automation, or tools on other machines: authenticate with
					<code>Authorization: Bearer &lt;key&gt;</code> against <code>{apiBase}</code>.
					The web interface never needs the key.
				</p>
				{#if !apiEnvManaged}
					<p class="settings-help">
						Changes here apply immediately; Save settings does not affect them.
					</p>
				{/if}
		</div>
		</section>
		<!-- Version -->
		<section class="settings-section" aria-labelledby="settings-version">
			<h2 class="settings-section__title" id="settings-version">Version</h2>
			<div class="settings-grid">
				<div class="settings-field">
					<dl class="version-pair">
						<dt class="settings-label">Bragi Books</dt>
						<dd class="settings-value font-mono">{settings.bragibooks_version || 'unknown'}</dd>
					</dl>
					<p class="settings-help">The version of the running Bragi Books build.</p>
				</div>
				<div class="settings-field">
					<dl class="version-pair">
						<dt class="settings-label">m4b-merge</dt>
						<dd class="settings-value font-mono">{settings.m4b_merge_version || 'unknown'}</dd>
					</dl>
					<p class="settings-help">
						{#if settings.m4b_merge_version === 'unknown'}
							The configured binary did not report a version — check the m4b-merge binary path in
							Processing.
						{:else}
							The version of the m4b-merge binary set in Processing; updates after you save.
						{/if}
					</p>
				</div>
			</div>
		</section>

		<!-- Save -->
		<div class="settings-save">
			{#if saving}
				<span class="settings-save__status" aria-live="polite">
					<Spinner size="1rem" />
					Saving…
				</span>
			{/if}
			<Button variant="primary" type="submit" loading={saving}>
				{#if !saving}
					<Save class="h-4 w-4" aria-hidden="true" />
				{/if}
				Save settings
			</Button>
		</div>
	</form>

	<DirPicker
		bind:open={pickerOpen}
		startPath={settings[pickerField]}
		title={pickerTitles[pickerField]}
		onselect={onPick}
	/>

	{#snippet apiKeyFooter()}
		<div class="flex justify-end">
			<Button variant="secondary" onclick={closeKeyModal}>Done</Button>
		</div>
	{/snippet}

	<Modal
		bind:open={apiKeyModalOpen}
		title="External API key"
		beforeClose={confirmKeyClose}
		footer={apiKeyFooter}
	>
		{#if apiKeyModalKey}
			<p class="text-sm text-[var(--text-secondary)] mb-3">
				Copy this key for your external tools. It is shown only once and cannot be
				retrieved later.
			</p>
			<div class="flex items-center gap-2">
				<code
					class="api-key-code"
					bind:this={keyCodeEl}
					tabindex="-1"
					oncopy={() => {
						keyCopied = true;
						copyState = 'copied';
					}}>{apiKeyModalKey}</code
				>
				<Button id="api-key-copy" variant="primary" class="shrink-0" onclick={copyApiKey}>
					{#if copyState === 'copied'}
						<Check class="h-4 w-4" aria-hidden="true" />
						Copied
					{:else}
						<Copy class="h-4 w-4" aria-hidden="true" />
						Copy key
					{/if}
				</Button>
			</div>
			<p class="api-key-feedback" role="status" aria-live="polite">
				{#if copyState === 'copied'}
					Key copied to the clipboard.
				{:else if copyState === 'failed'}
					Copy failed. The key is selected — press Control+C or Command+C.
				{/if}
			</p>
			{#if closeWarning}
				<div class="settings-confirm" role="alert">
					<p class="settings-confirm__text">
						This key has not been copied. Closing discards it; you would have to reissue a new
						key.
					</p>
					<div class="settings-confirm__actions">
						<Button variant="danger" onclick={forceCloseKeyModal}>Close anyway</Button>
						<Button variant="ghost" onclick={() => (closeWarning = false)}>Keep key</Button>
					</div>
				</div>
			{/if}
		{/if}
	</Modal>
{/if}

<svelte:head>
	<title>Settings — Bragi Books</title>
</svelte:head>

<style>
	/* API key help — steps to create an account and key */
	.apikey-help {
		margin-top: 0.5rem;
		padding: 0.625rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.apikey-help__summary {
		cursor: pointer;
		font-size: 0.8125rem;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.apikey-help__summary:hover {
		color: var(--text);
	}

	.apikey-help__steps {
		margin: 0.625rem 0 0;
		padding-left: 1.25rem;
		display: grid;
		gap: 0.375rem;
		font-size: 0.8125rem;
		color: var(--text-secondary);
	}

	.apikey-help__steps a {
		color: var(--workspace-action);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.apikey-help__steps a:hover {
		color: var(--text);
	}

	.apikey-help__note {
		margin: 0.625rem 0 0;
		font-size: 0.75rem;
		line-height: 1.5;
		color: var(--text-muted);
	}

	/* Section — one labeled white panel per group */
	.settings-section {
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		overflow: hidden;
		background: var(--bg-panel);
	}

	.settings-section__title {
		margin: 0;
		padding: 0.75rem 1.25rem;
		font-size: 0.875rem;
		font-weight: 600;
		color: var(--text);
		background: var(--surface);
		border-bottom: 1px solid var(--border-subtle);
	}

	/* Field — hairline dividers drawn as a 1px grid gap */
	.settings-grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 1px;
		background: var(--border-subtle);
	}

	@media (min-width: 640px) {
		.settings-grid {
			grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		}
	}

	.settings-field {
		padding: 1rem 1.25rem;
		background: var(--bg-panel);
		min-width: 0;
	}

	.settings-label {
		display: block;
		margin-bottom: 0.375rem;
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.settings-input-row {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0.5rem;
	}

	.settings-input-row input {
		flex: 1 1 auto;
		min-width: 0;
	}

	.settings-field-error {
		margin-top: 0.35rem;
		font-size: 0.75rem;
		color: var(--state-red);
		font-weight: 500;
	}

	.settings-help {
		margin-top: 0.35rem;
		font-size: 0.75rem;
		color: var(--text-muted);
		line-height: 1.45;
		text-wrap: pretty;
	}

	.settings-help code {
		overflow-wrap: anywhere;
	}

	.settings-help--status {
		margin-top: 0;
		font-size: 0.875rem;
		color: var(--text-secondary);
	}

	.api-key-code {
		display: block;
		flex: 1 1 auto;
		min-width: 0;
		overflow-wrap: anywhere;
		padding: 0.625rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-size: 0.8125rem;
		cursor: text;
		user-select: all;
	}

	.api-key-feedback {
		min-height: 1.25rem;
		margin-top: 0.5rem;
		font-size: 0.75rem;
		line-height: 1.45;
		color: var(--text-secondary);
	}

	/* Destructive confirmation — same two-step grammar as Match removal */
	.settings-confirm {
		margin-top: 0.75rem;
		padding: 0.75rem;
		background: var(--state-red-bg);
		border: 1px solid var(--state-red-border);
		border-radius: var(--radius-sm);
	}

	.settings-confirm__text {
		font-size: 0.8125rem;
		line-height: 1.5;
		color: var(--text);
		text-wrap: pretty;
	}

	.settings-confirm__actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		margin-top: 0.625rem;
	}

	.settings-value {
		margin: 0;
		font-size: 0.875rem;
		line-height: 1.5;
		color: var(--text);
		overflow-wrap: anywhere;
	}

	/* Term/definition pair — the dl resets are needed because <dd>
	   ships with a left margin by default */
	.version-pair {
		margin: 0;
	}

	.version-pair dd {
		margin: 0;
	}

	#input_dir_desc {
		text-wrap: balance;
	}

	/* Save bar — end of the form, reachable without scrolling */
	.settings-save {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.settings-save__status {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.8rem;
		color: var(--text-secondary);
		font-weight: 500;
	}

	/* Narrow screens */
	@media (max-width: 639px) {
		.settings-save {
			flex-direction: column;
			align-items: stretch;
		}

		.settings-save :global(.btn) {
			width: 100%;
		}
	}
</style>
