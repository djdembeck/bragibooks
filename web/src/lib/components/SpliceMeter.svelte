<script lang="ts">
	import { formatRuntime } from '$lib/types';

	interface Props {
		status: string;
		progress?: number;
		runtimeMinutes?: number;
		size?: 'sm' | 'lg';
		/** Color the seam notch is cut with — should match the surface
		 *  the meter sits on so the notch reads as a real gap. */
		cutColor?: string;
	}

	let { status, progress, runtimeMinutes, size = 'sm', cutColor = 'var(--bg-panel)' }: Props = $props();

	const kind = $derived.by(() => {
		const normalized = status.toLowerCase();
		if (normalized === 'done' || normalized === 'completed') return 'done';
		if (normalized === 'processing' || normalized === 'running' || normalized === 'queued') return 'processing';
		if (normalized === 'error' || normalized === 'failed') return 'error';
		if (normalized === 'ready' || normalized === 'matched') return 'matched';
		return 'pending';
	});

	// Geometry — bar height, chase width, seam angle all derive from size.
	const w = $derived(size === 'lg' ? 200 : 110);
	const barH = $derived(size === 'lg' ? 6 : 4);
	const half = $derived(w / 2);
	const gap = $derived(size === 'lg' ? 12 : 8);
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
     A finished book is spliced (seam notch visible); an errored
     book is a snapped splice (a break), never just a red label. -->
<div class="splice-meter" style="--splice-w: {w}px" role="img" aria-label="Splice: {label}">
	<svg width={w} height={barH + (size === 'lg' ? 8 : 4)} viewBox="0 0 {w} {barH + (size === 'lg' ? 8 : 4)}" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" focusable="false">
		{#if kind === 'pending'}
			<rect x="1" y="3" width={half - gap - 2} height={barH} stroke="var(--text-muted)" stroke-width="1" stroke-dasharray="3 2" />
			<rect x={half + gap + 2} y="3" width={half - gap - 2} height={barH} stroke="var(--text-muted)" stroke-width="1" stroke-dasharray="3 2" />
		{:else if kind === 'matched'}
			<rect x="1" y="3" width={half - 2} height={barH} fill="var(--state-green)" />
			<rect x={half + 2} y="3" width={half - 2} height={barH} fill="var(--state-green)" />
		{:else if kind === 'processing'}
			<rect x="1" y="3" width={w - 2} height={barH} fill="var(--state-amber)" opacity="0.35" />
			<rect x="1" y="3" width={(w - 2) * fill} height={barH} fill="var(--state-amber)" />
			<rect class="splice-meter__chase" x="-6" y="3" width="8" height={barH} fill="var(--state-amber)" />
		{:else if kind === 'done'}
			<rect x="1" y="3" width={w - 2} height={barH} fill="var(--state-green)" />
			<!-- The seam — a diagonal notch cut where the halves were joined.
			     Two theater args: the notch flare at the top/bottom edges. -->
			<path
				d="M {half - 2} 0 L {half + 2} {barH + 6} L {half + 5} {barH + 6} L {half + 1} 0 Z"
				fill={cutColor}
			/>
			<!-- Check at the right end -->
			<path d="M {w - 11} {barH / 2 + 1} l 3 3 l 6 -6" stroke="var(--state-green)" stroke-width="1.75" />
		{:else}
			<!-- Snapped: left half solid red, right half falling away, jagged gap -->
			<rect x="1" y="3" width={half - gap - 2} height={barH} fill="var(--state-red)" />
			<path d="M {half + gap + 8} 3 L {w - 1} 3 L {w - 1} {barH + 3} L {half + gap + 2} {barH + 3} Z" fill="var(--state-red)" opacity="0.55" />
			<path d="M {half - 3} 2 l 4 5 l -2 3" stroke="var(--state-red)" stroke-width="1.5" />
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

<style>
	.splice-meter {
		line-height: 0;
	}

	@keyframes splice-chase {
		from { transform: translateX(0); }
		to { transform: translateX(calc(var(--splice-w, 200px) + 12px)); }
	}

	.splice-meter__chase {
		animation: splice-chase 1.6s linear infinite;
	}

	@media (prefers-reduced-motion: reduce) {
		.splice-meter__chase {
			animation: none;
			opacity: 0;
		}
	}
</style>
