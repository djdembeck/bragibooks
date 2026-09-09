<script lang="ts">
	import { Check, X } from '@lucide/svelte';
	import { formatRuntime } from '$lib/types';

	interface Props {
		status: string;
		progress?: number;
		runtimeMinutes?: number;
		size?: 'sm' | 'lg';
	}

	let { status, progress, runtimeMinutes, size = 'sm' }: Props = $props();

	const kind = $derived.by(() => {
		const normalized = status.toLowerCase();
		if (normalized === 'done' || normalized === 'completed') return 'done';
		if (normalized === 'processing' || normalized === 'running' || normalized === 'queued') return 'processing';
		if (normalized === 'error' || normalized === 'failed') return 'error';
		if (normalized === 'ready' || normalized === 'matched') return 'matched';
		return 'pending';
	});

	const w = $derived(size === 'lg' ? 200 : 110);
	const h = $derived(size === 'lg' ? 22 : 14);
	const half = $derived(w / 2);
	const gap = $derived(size === 'lg' ? 14 : 8);
	const seamW = $derived(size === 'lg' ? 10 : 7);
	const fill = $derived(Math.max(0, Math.min(1, progress ?? 0)));

	const label = $derived(
		kind === 'done'
			? 'Spliced'
			: kind === 'error'
				? 'Splice snapped'
				: kind === 'processing'
					? 'Splicing'
					: kind === 'matched'
						? 'Halves proofed'
						: 'Unspooled'
	);
</script>

<!-- The splice — the two halves m4b-merge joins into one reel.
     A finished book is spliced; an errored book is a snapped
     splice (a break), never just a red label. Pure geometry.   -->
<div class="splice-meter" role="img" aria-label="Splice: {label}">
	<svg width={w} height={h} viewBox="0 0 {w} {h}" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" focusable="false">
		{#if kind === 'pending'}
			<rect x="1" y={h / 2 - 2} width={half - gap - 2} height="4" rx="1" stroke="var(--bench-text-muted)" stroke-width="1.5" />
			<rect x={half + gap + 2} y={h / 2 - 2} width={half - gap - 2} height="4" rx="1" stroke="var(--bench-text-muted)" stroke-width="1.5" />
		{:else if kind === 'matched'}
			<rect x="1" y={h / 2 - 2} width={half - 2} height="4" rx="1" stroke="var(--state-green)" stroke-width="1.5" />
			<rect x={half + 2} y={h / 2 - 2} width={half - 2} height="4" rx="1" stroke="var(--state-green)" stroke-width="1.5" />
			<line x1={half} y1="2" x2={half} y2={h - 2} stroke="var(--state-green)" stroke-width="1.5" stroke-dasharray="2 3" />
			<g transform="translate({half - 6}, {h - 12})" color="var(--state-green)">
				<Check class="h-3 w-3" strokeWidth="2.5" />
			</g>
		{:else if kind === 'processing'}
			<rect x="1" y={h / 2 - 2} width={w - 2} height="4" rx="1" stroke="var(--state-amber)" stroke-width="1.5" />
			<rect x="1.75" y={h / 2 - 1.25} width={(w - 3.5) * fill} height="2.5" rx="0.5" fill="var(--state-amber)" opacity="0.7" />
			<rect class="splice-meter__chase" x="-4" y={h / 2 - 2} width="7" height="4" fill="var(--state-amber)" />
			<line x1={half} y1="2" x2={half} y2={h - 2} stroke="var(--state-amber)" stroke-width="1.5" stroke-dasharray="2 3" />
		{:else if kind === 'done'}
			<rect x="1" y={h / 2 - 2} width={w - 2} height="4" rx="1" fill="var(--state-green)" />
			<!-- The splice seam — where the two halves were joined -->
			<path
				d="M {half - seamW / 2} 0 L {half + seamW / 2} {h} L {half + seamW / 2 + 3} {h} L {half - seamW / 2 + 3} 0 Z"
				fill="var(--bench-bg)"
			/>
			<path d="M {half - seamW / 2} 0 L {half + seamW / 2} {h}" stroke="var(--state-green)" stroke-width="1.5" />
			<g transform="translate({w - 15}, {h - 14})" color="var(--state-green)">
				<Check class="h-3 w-3" strokeWidth="2.5" />
			</g>
		{:else}
			<!-- Snapped splice — a visible break between the halves -->
			<rect x="1" y={h / 2 - 2} width={half - gap - 2} height="4" rx="1" fill="var(--state-red)" />
			<path d="M {w - 3} {h / 2 - 2} L {w - 3} {h / 2 + 2} L {half + gap + 6} {h / 2 + 2} L {half + gap + 1} {h / 2 - 2} Z" fill="var(--state-red)" opacity="0.55" />
			<g transform="translate(2, 1)" color="var(--state-red)">
				<X class={size === 'lg' ? 'h-4 w-4' : 'h-3 w-3'} strokeWidth="2.5" />
			</g>
		{/if}
	</svg>
	{#if size === 'lg'}
		<span class="mt-1.5 block font-mono text-xs tracking-wide text-[var(--text-muted)] tabular-nums" aria-hidden="true">
			{label}
			{#if runtimeMinutes && runtimeMinutes > 0}
				· {formatRuntime(runtimeMinutes)}
			{/if}
		</span>
	{/if}
</div>
