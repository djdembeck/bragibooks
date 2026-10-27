<script lang="ts">
	import { get } from '#lib/api.js';
	import Modal from './Modal.svelte';
	import Button from './Button.svelte';
	import Skeleton from './Skeleton.svelte';
	import ErrorDetail from './ErrorDetail.svelte';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import type { DirectoryEntry, DirectoriesResponse } from '#lib/types.js';
	import { Folder, FolderUp, Check, AlertTriangle } from '@lucide/svelte';

	let {
		open = $bindable(false),
		startPath = '',
		title = 'Choose a directory',
		onselect
	}: {
		open?: boolean;
		startPath?: string;
		title?: string;
		onselect?: (path: string) => void;
	} = $props();

	// The path being shown. Set to the attempted target before load so the
	// breadcrumb and up-navigation still work when a starting path is bad.
	let currentPath = $state('/');
	let entries = $state<DirectoryEntry[]>([]);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	function normalizePath(p: string): string {
		let t = (p ?? '').trim();
		if (!t) return '/';
		if (!t.startsWith('/')) t = '/' + t;
		t = t.replace(/\/+$/, '');
		return t === '' ? '/' : t;
	}

	function formatSize(bytes: number): string {
		if (!bytes) return '';
		const units = ['B', 'KB', 'MB', 'GB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		return `${(bytes / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
	}

	let loadSeq = 0;

	async function load(path: string) {
		const seq = ++loadSeq;
		const target = normalizePath(path);
		currentPath = target;
		await dl.run(async () => {
			const res = await get<DirectoriesResponse>(
				`/directories?path=${encodeURIComponent(target)}`
			);
			// A newer navigation superseded this request — drop the stale
			// listing so a slow response can't overwrite the current folder.
			if (seq !== loadSeq) return;
			currentPath = res.path ? normalizePath(res.path) : target;
			entries = (res.entries || [])
				.filter((e) => e.name && e.path && e.name !== '.' && e.name !== '..')
				.sort((a, b) => {
					if (a.type === 'dir' && b.type !== 'dir') return -1;
					if (a.type !== 'dir' && b.type === 'dir') return 1;
					return a.name.localeCompare(b.name);
				});
		});
	}

	$effect(() => {
		if (open) {
			void load(normalizePath(startPath));
		}
	});

	const dirs = $derived(entries.filter((e) => e.type === 'dir'));
	const files = $derived(entries.filter((e) => e.type !== 'dir'));

	// Breadcrumb segments, each with the absolute path it points at.
	const segments = $derived.by(() => {
		const parts = currentPath.split('/').filter(Boolean);
		const out: { name: string; path: string }[] = [];
		let acc = '';
		for (const part of parts) {
			acc += '/' + part;
			out.push({ name: part, path: acc });
		}
		return out;
	});

	const parentPath = $derived.by(() => {
		if (currentPath === '/') return null;
		const parts = currentPath.split('/').filter(Boolean);
		parts.pop();
		return parts.length ? '/' + parts.join('/') : '/';
	});

	function choose(path: string) {
		onselect?.(path);
		open = false;
	}
</script>

<Modal bind:open {title}>
	<div class="dirmeta">
		<span class="dirmeta__label">Current path</span>
		<code class="dirmeta__path">{currentPath}</code>
	</div>

	<div class="crumb" aria-label="Breadcrumb">
		<button
			type="button"
			class="crumb__seg"
			class:crumb__seg--root={segments.length === 0}
			onclick={() => load('/')}
		>
			/
		</button>
		{#each segments as seg (seg.path)}
			<span class="crumb__sep" aria-hidden="true">/</span>
			<button
				type="button"
				class="crumb__seg"
				class:crumb__seg--current={seg.path === currentPath}
				aria-current={seg.path === currentPath ? 'location' : undefined}
				onclick={() => load(seg.path)}
			>
				{seg.name}
			</button>
		{/each}
	</div>

	<div class="nav-row">
		<Button
			variant="secondary"
			disabled={parentPath === null || dl.showSkeleton}
			onclick={() => parentPath && load(parentPath)}
		>
			<FolderUp class="h-4 w-4" aria-hidden="true" />
			Up
		</Button>
	</div>

	<div class="listing">
		{#if !dl.hasLoaded && !dl.showSkeleton}
			<!-- First load, before the 200 ms skeleton delay: announce a
			     pending state without claiming the folder is empty. -->
			<div class="listing__pending" role="status" aria-live="polite">
				<span class="sr-only">Loading directory…</span>
			</div>
		{:else if dl.showSkeleton}
			{#each Array(6) as _}
				<Skeleton height="2.25rem" />
			{/each}
		{:else if dl.error}
			<div class="state state--error" role="alert">
				<AlertTriangle class="h-5 w-5 shrink-0 text-[var(--state-red)]" aria-hidden="true" />
				<div class="state__body">
					<p class="state__title">{dl.error.message}</p>
					<ErrorDetail raw={dl.error.raw} />
					<button type="button" class="state__retry" onclick={() => load(currentPath)}>
						Retry
					</button>
				</div>
			</div>
		{:else if dirs.length === 0 && files.length === 0}
			<div class="state">
				<p class="state__empty">This folder is empty.</p>
			</div>
		{:else}
			<ul class="rows">
				{#each dirs as entry (entry.path)}
					<li>
						<button type="button" class="row row--dir" onclick={() => load(entry.path)}>
							<Folder class="row__icon" aria-hidden="true" />
							<span class="row__name">{entry.name}</span>
							<span class="row__meta">folder</span>
						</button>
					</li>
				{/each}
				{#each files as entry (entry.path)}
					<li>
						<div class="row row--file" aria-disabled="true">
							<span class="row__name">{entry.name}</span>
							{#if formatSize(entry.size)}
								<span class="row__meta">{formatSize(entry.size)}</span>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</div>

	{#snippet footer()}
		<div class="picker-footer">
			<p class="picker-footer__hint">
				Selecting a folder applies it to the field. Files cannot be selected.
			</p>
			<div class="picker-footer__actions">
				<Button variant="ghost" onclick={() => (open = false)}>Cancel</Button>
				<Button
					variant="primary"
					disabled={!!dl.error || dl.showSkeleton}
					onclick={() => choose(currentPath)}
				>
					<Check class="h-4 w-4" aria-hidden="true" />
					Select this folder
				</Button>
			</div>
		</div>
	{/snippet}
</Modal>

<style>
	.dirmeta {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		margin-bottom: 0.5rem;
	}

	.dirmeta__label {
		font-size: 0.6875rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.dirmeta__path {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.8125rem;
		color: var(--text);
		word-break: break-all;
	}

	.crumb {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.125rem;
		padding: 0.375rem 0.5rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.crumb__seg {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.8125rem;
		padding: 0.125rem 0.375rem;
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
	}

	.crumb__seg:hover {
		background: var(--surface-hover);
		color: var(--text);
	}

	.crumb__seg--current {
		color: var(--text);
		font-weight: 600;
	}

	.crumb__sep {
		color: var(--text-muted);
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.8125rem;
	}

	.nav-row {
		display: flex;
		justify-content: flex-start;
		margin: 0.5rem 0;
	}

	.listing {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		min-height: 8rem;
		max-height: 44vh;
		overflow-y: auto;
		background: var(--surface);
	}

	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		min-height: 2.25rem;
		padding: 0.375rem 0.625rem;
		text-align: left;
		border-bottom: 1px solid var(--border-subtle);
	}

	.rows li:last-child .row {
		border-bottom: none;
	}

	.row--dir {
		color: var(--text);
	}

	.row--dir:hover {
		background: var(--surface-hover);
	}

	.row--file {
		color: var(--text-muted);
		cursor: not-allowed;
	}

	:global(.row__icon) {
		width: 1rem;
		height: 1rem;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.row__name {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 0.8125rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.row__meta {
		margin-left: auto;
		font-size: 0.6875rem;
		color: var(--text-muted);
		flex-shrink: 0;
	}

	.listing__pending {
		/* Match the six-row skeleton height so the settled listing does not
		   shift the modal layout when the first response arrives. */
		min-height: 13rem;
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.state {
		display: flex;
		gap: 0.625rem;
		padding: 1rem;
	}

	.state--error {
		align-items: flex-start;
	}

	.state__body {
		flex: 1;
	}

	.state__title {
		font-size: 0.8125rem;
		color: var(--text);
	}

	.state__empty {
		margin: 0;
		font-size: 0.8125rem;
		font-style: italic;
		color: var(--text-muted);
	}

	.state__retry {
		margin-top: 0.375rem;
		font-size: 0.8125rem;
		font-weight: 600;
		color: var(--workspace-action);
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.picker-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
	}

	.picker-footer__hint {
		margin: 0;
		font-size: 0.75rem;
		color: var(--text-muted);
		flex: 1 1 12rem;
	}

	.picker-footer__actions {
		display: flex;
		gap: 0.5rem;
		margin-left: auto;
	}
</style>
