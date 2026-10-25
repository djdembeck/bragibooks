<script lang="ts">
	import { tick } from 'svelte';
	import { MARTIAN, parseParts, resolveExample, serializeParts, TOKEN_META, TOKEN_ORDER, validateScheme, type Part, type TokenKey } from '$lib/scheme';

	let { value = $bindable(''), outputDir = '' }: { value?: string; outputDir?: string } = $props();

	let parts = $state<Part[]>(parseParts(value));
	let sel = $state<number | null>(null);
	const errors = $derived(validateScheme(parts).errors);
	const warnings = $derived(validateScheme(parts).warnings);
	const example = $derived(resolveExample(parts, MARTIAN.values, outputDir));

	// Single sync point: external writes to `value` (page load, other
	// programmatic updates) re-parse. Own mutations always write
	// value = serializeParts(parts) synchronously right after, so the
	// guard below makes the effect a no-op after own writes.
	$effect(() => {
		if (value !== serializeParts(parts)) {
			sel = null;
			parts = parseParts(value);
		}
	});

	function mutate(next: Part[], nextSel: number | null) {
		parts = next;
		sel = nextSel;
		value = serializeParts(parts);
	}

	function insertAfter(index: number, part: Part) {
		const next = [...parts.slice(0, index + 1), part, ...parts.slice(index + 1)];
		mutate(next, index + 1);
	}

	function insertToken(key: TokenKey) {
		insertPart({ kind: 'token', key });
	}

	function insertText() {
		insertPart({ kind: 'text', value: '' });
	}

	function insertSep() {
		insertPart({ kind: 'sep' });
	}

	/**
	 * Insert after the selected part (or append), select the new part, and
	 * move focus to it once the DOM has caught up.
	 */
	function insertPart(part: Part) {
		const insertedAt = (sel === null ? parts.length - 1 : sel) + 1;
		insertAfter(sel === null ? parts.length - 1 : sel, part);
		void tick().then(() => focusPart(insertedAt));
	}

	function removeAt(i: number) {
		mutate([...parts.slice(0, i), ...parts.slice(i + 1)], Math.min(i, parts.length - 2) >= 0 ? Math.min(i, parts.length - 2) : null);
	}

	function movePart(i: number, dir: -1 | 1) {
		const j = i + dir;
		if (j < 0 || j >= parts.length) return false;
		const next = [...parts];
		[next[i], next[j]] = [next[j], next[i]];
		mutate(next, j);
		return true;
	}

	function textInput(i: number) {
		return (e: Event) => {
			const el = e.currentTarget as HTMLInputElement;
			const clean = el.value.replace(/\//g, '');
			if (clean !== el.value) el.value = clean;
			const next = parts.map((p, k) => (k === i ? ({ kind: 'text', value: clean } as Part) : p));
			parts = next;
			value = serializeParts(parts);
		};
	}

	function onStripKeydown(e: KeyboardEvent) {
		if (
			e.target instanceof HTMLInputElement &&
			!e.altKey &&
			(e.key === 'ArrowLeft' || e.key === 'ArrowRight')
		) {
			return; // native caret movement
		}
		const target = e.target as HTMLElement;
		const inputIdx = target instanceof HTMLInputElement ? target.dataset.idx : undefined;
		const active = inputIdx !== undefined ? Number(inputIdx) : sel;
		if (active === null || active === undefined || Number.isNaN(active)) return;

		if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
			const dir = e.key === 'ArrowLeft' ? -1 : 1;
			if (e.altKey || e.metaKey) {
				if (movePart(active, dir as -1 | 1)) {
					e.preventDefault();
					// Defer: the keyed list swaps its nodes on re-render.
					void tick().then(() => focusPart(active + dir));
				}
			} else {
				e.preventDefault();
				const next = (active + dir + parts.length) % parts.length;
				sel = next;
				focusPart(next);
			}
		} else if (e.key === 'Delete' || e.key === 'Backspace') {
			const p = parts[active];
			if (p.kind === 'text' && p.value !== '') return; // native caret edit
			e.preventDefault();
			const nextSel = active - 1 >= 0 ? Math.min(active - 1, parts.length - 2) : null;
			removeAt(active);
			if (nextSel !== null) focusPart(nextSel);
		}
	}

	function focusPart(i: number) {
		if (i < 0 || i >= parts.length) return;
		const node = stripEl?.querySelector(`[data-idx="${i}"]`);
		if (node instanceof HTMLButtonElement || node instanceof HTMLInputElement) {
			node.focus();
		}
	}

	let stripEl: HTMLDivElement | undefined = $state();
</script>

<div class="pbf">
	<div class="pbf-palette" role="group" aria-label="Insert path parts">
		{#each TOKEN_ORDER as key}
			<button type="button" class="pbf-chip pbf-chip--token" title={TOKEN_META[key].desc} aria-label={`Insert ${TOKEN_META[key].label}`} onclick={() => insertToken(key)}>
				{key}
			</button>
		{/each}
		<button type="button" class="pbf-chip" title="Insert literal path text" aria-label="Insert path text" onclick={insertText}>Text</button>
		<button type="button" class="pbf-chip" title="Insert a subdirectory separator" aria-label="Insert separator" onclick={insertSep}>/</button>
	</div>

	<!-- svelte-ignore a11y_no_noninteractive_element_interactions: the strip is a keyboard-operable container; keydown dispatches to focused children -->
	<div class="pbf-strip" bind:this={stripEl} role="group" aria-label="Output path format" data-invalid={errors.length > 0 ? 'true' : 'false'} onkeydown={onStripKeydown}>
		{#each parts as p, i (i)}
			{#if p.kind === 'token'}
				<button type="button" class="pbf-part pbf-token" data-idx={i} aria-pressed={sel === i} aria-label="{p.key} — Delete to remove, Alt+Arrows to move" onclick={() => (sel = i)}>
					{p.key}
					<span class="pbf-x" aria-hidden="true">×</span>
				</button>{:else if p.kind === 'sep'}
				<button type="button" class="pbf-part pbf-sep" data-idx={i} aria-pressed={sel === i} aria-label="Separator — Delete to remove, Alt+Arrows to move" onclick={() => (sel = i)}>
					/
				</button>{:else}
				<input
					class="pbf-text"
					type="text"
					data-idx={i}
					value={p.value}
					aria-label="Path text"
					oninput={textInput(i)}
					onfocus={() => (sel = i)}
				/>
			{/if}
		{/each}
		{#if parts.length === 0}
			<span class="pbf-empty">Click a part above to start.</span>
		{/if}
	</div>

	{#if errors.length > 0}
		<p class="pbf-errors" role="alert">{errors.join(' ')}</p>
	{/if}
	{#each warnings as w}
		<p class="pbf-warning">{w}</p>
	{/each}

	<p class="pbf-preview-label">
		Example — {MARTIAN.label} · <a href={MARTIAN.link} target="_blank" rel="noopener noreferrer">release page</a>{outputDir ? '' : ' — output folder not set'}:
	</p>
	<code class="pbf-preview">{example || '—'}</code>
</div>

<style>
	.pbf {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.pbf-palette {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
	}

	.pbf-chip {
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 12px;
		font-weight: 600;
		height: 1.75rem;
		padding: 0 0.5rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--surface);
		color: var(--text-secondary);
		cursor: pointer;
		transition:
			background var(--duration-fast) var(--ease-out),
			border-color var(--duration-fast) var(--ease-out);
	}

	.pbf-chip:hover {
		background: var(--surface-hover);
		border-color: var(--border-strong);
	}

	.pbf-chip--token {
		background: var(--accent-wash);
		color: var(--accent-text);
	}

	.pbf-strip {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
		align-items: center;
		background: var(--surface);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		min-height: 2.75rem;
		padding: 0.375rem;
	}

	.pbf-strip[data-invalid='true'] {
		border-color: var(--state-red);
	}

	.pbf-part {
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 12.5px;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		min-height: 1.75rem;
		padding: 0.25rem 0.5rem;
		border-radius: 4px;
		cursor: pointer;
		transition:
			border-color var(--duration-fast) var(--ease-out),
			box-shadow var(--duration-fast) var(--ease-out);
	}

	.pbf-token {
		background: var(--accent-wash);
		border: 1px solid var(--border);
		color: var(--accent-text);
	}

	.pbf-sep {
		background: transparent;
		border: 1px solid transparent;
		color: var(--text-muted);
		min-width: 1.5rem;
		justify-content: center;
	}

	.pbf-part[aria-pressed='true'] {
		box-shadow: inset 0 0 0 1px var(--workspace-action);
	}

	.pbf-part:hover {
		border-color: var(--workspace-action);
	}

	@media (prefers-reduced-motion: reduce) {
		.pbf-chip,
		.pbf-part {
			transition: none;
		}
	}

	.pbf-x {
		font-size: 12px;
		color: var(--text-muted);
		opacity: 0;
		line-height: 1;
	}

	.pbf-token:hover .pbf-x,
	.pbf-token:focus-within .pbf-x {
		opacity: 1;
	}

	.pbf-text {
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 12.5px;
		background: transparent;
		border: 1px dashed var(--border-strong);
		border-radius: 4px;
		padding: 0.25rem 0.4rem;
		min-width: 3ch;
		min-height: 1.75rem;
		color: var(--text);
	}

	.pbf-text:focus {
		outline: none;
		border: 1px solid var(--text);
		box-shadow: 0 0 0 3px var(--focus-ring);
	}

	.pbf-empty {
		font-size: 0.75rem;
		color: var(--text-muted);
		padding: 0.25rem;
	}

	.pbf-errors {
		margin-top: 0;
		font-size: 0.75rem;
		font-weight: 500;
		color: var(--state-red);
	}

	.pbf-warning {
		margin-top: 0;
		font-size: 0.75rem;
		font-weight: 500;
		color: var(--state-amber);
	}

	.pbf-preview-label {
		font-size: 12px;
		color: var(--text-muted);
	}

	.pbf-preview-label a {
		color: var(--workspace-action);
	}

	.pbf-preview {
		display: block;
		padding: 0.5rem 0.75rem;
		background: var(--bg);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 13px;
		color: var(--text);
		word-break: break-word;
	}
</style>
