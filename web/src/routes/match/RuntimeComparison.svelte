<script lang="ts">
	import type { RuntimeComparisonResult } from '#lib/runtimeComparison.js';

	let { result }: { result: RuntimeComparisonResult } = $props();

	const severityLabels: Partial<Record<RuntimeComparisonResult['severity'], string>> = {
		small: 'Small difference',
		noticeable: 'Noticeable difference',
		moderate: 'Moderate difference',
		large: 'Large difference',
		'very-large': 'Very large difference'
	};
</script>

<span class="rc-cmp" data-severity={result.severity}>
	<span class="rc-cmp__row">
		<span class="rc-cmp__label">Source</span>
		<span class="rc-cmp__value">
			{result.sourceLabel}
			{#if result.sourceRounded}<span class="rc-cmp__approx" aria-label="Shown to the minute">≈</span>{/if}
		</span>
		<span class="rc-cmp__direction">
			{#if result.direction === 'source-longer'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5m-6 6 6-6 6 6" /></svg><span>longer</span>
			{:else if result.direction === 'catalog-longer'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14m6-6-6 6-6-6" /></svg><span>shorter</span>
			{:else if result.direction === 'equal'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 9h14M5 15h14" /></svg>
			{:else}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 13c5-5 11 5 16 0" /></svg>
			{/if}
		</span>
	</span>
	<span class="rc-cmp__row">
		<span class="rc-cmp__label">Catalog</span>
		<span class="rc-cmp__value">
			{result.catalogLabel}
			{#if result.catalogRounded}<span class="rc-cmp__approx" aria-label="Shown to the minute">≈</span>{/if}
		</span>
		<span class="rc-cmp__direction">
			{#if result.direction === 'catalog-longer'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5m-6 6 6-6 6 6" /></svg><span>longer</span>
			{:else if result.direction === 'source-longer'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14m6-6-6 6-6-6" /></svg><span>shorter</span>
			{:else if result.direction === 'equal'}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 9h14M5 15h14" /></svg>
			{:else}
				<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 13c5-5 11 5 16 0" /></svg>
			{/if}
		</span>
	</span>
	<span class="rc-cmp__diff">
		<span class="rc-cmp__diff-label">Difference</span>
		<span class="rc-cmp__diff-value">
			{#if result.direction === 'equal'}
				0 min
			{:else if result.direction === 'uncertain'}
				Rounding overlap
			{:else if result.underMinute}
				Under 1 min
			{:else if result.exactWholeMinutes}
				{result.lowerMinutes} min
			{:else}
				{result.lowerMinutes}–{result.upperMinutes} min
			{/if}
		</span>
		{#if result.direction === 'equal'}
			<span class="rc-cmp__note">Same duration.</span>
		{:else if result.direction === 'uncertain'}
			<span class="rc-cmp__note">Minute rounding may explain the gap.</span>
		{:else if severityLabels[result.severity]}
			<span class="rc-cmp__severity">{severityLabels[result.severity]}</span>
		{/if}
	</span>
	{#if result.sourceRounded || result.catalogRounded}
		<span class="rc-cmp__note">Durations shown to the minute.</span>
	{/if}
</span>

<style>
	.rc-cmp { display: grid; gap: 3px; margin-top: 7px; }
	.rc-cmp__row { display: grid; grid-template-columns: 4.5em 1fr auto; align-items: baseline; gap: 8px; }
	.rc-cmp__label { color: var(--m-muted); }
	.rc-cmp__value { font-variant-numeric: tabular-nums; color: var(--m-ink); white-space: nowrap; }
	.rc-cmp__approx { color: var(--m-muted); font-size: 11px; }
	.rc-cmp__direction { display: inline-flex; align-items: center; gap: 3px; font-size: 11px; color: var(--m-ink-2); white-space: nowrap; }
	.rc-cmp__direction svg { width: 12px; height: 12px; fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; flex-shrink: 0; }
	.rc-cmp[data-severity='noticeable'] .rc-cmp__direction,
	.rc-cmp[data-severity='moderate'] .rc-cmp__direction { color: var(--state-amber); }
	.rc-cmp[data-severity='large'] .rc-cmp__direction,
	.rc-cmp[data-severity='very-large'] .rc-cmp__direction { color: var(--state-red); }
	.rc-cmp__diff { display: flex; flex-wrap: wrap; align-items: baseline; gap: 3px 7px; margin-top: 5px; padding-top: 5px; border-top: 1px solid var(--m-border); }
	.rc-cmp__diff-label { color: var(--m-muted); }
	.rc-cmp__diff-value { font-weight: 600; font-variant-numeric: tabular-nums; color: var(--m-ink); white-space: nowrap; }
	.rc-cmp[data-severity='noticeable'] .rc-cmp__diff-value,
	.rc-cmp[data-severity='moderate'] .rc-cmp__diff-value { color: var(--state-amber); }
	.rc-cmp[data-severity='large'] .rc-cmp__diff-value,
	.rc-cmp[data-severity='very-large'] .rc-cmp__diff-value { color: var(--state-red); }
	.rc-cmp__severity { font-weight: 500; color: var(--m-ink-2); }
	.rc-cmp[data-severity='noticeable'] .rc-cmp__severity,
	.rc-cmp[data-severity='moderate'] .rc-cmp__severity { color: var(--state-amber); }
	.rc-cmp[data-severity='large'] .rc-cmp__severity,
	.rc-cmp[data-severity='very-large'] .rc-cmp__severity { color: var(--state-red); }
	.rc-cmp[data-severity='very-large'] .rc-cmp__severity { font-weight: 700; }
	.rc-cmp__note { display: block; font-size: 11px; line-height: 1.4; color: var(--m-muted); }
</style>
