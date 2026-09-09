<script lang="ts">
	import { page } from '$app/stores';
	import { STATIONS } from '$lib/useStationCounts.svelte';

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}
</script>

<!-- The slim tape — four stations, solid track segments BETWEEN the dots.
     The active station's enamel dot is the "now" light. No counts: the
     dashboard owns the bays. -->
<nav class="rail" aria-label="Workflow">
	<div class="mx-auto max-w-6xl px-4 py-1.5">
		<div class="rail__track">
			{#each STATIONS as station, i (station.id)}
				{@const active = isActive(station.href, $page.url.pathname)}
				{#if i > 0}
					<span class="rail__seg" aria-hidden="true"></span>
				{/if}
				<a
					href={station.href}
					class="rail-station"
					class:rail-station-active={active}
					aria-current={active ? 'step' : undefined}
					aria-label="Station {station.number} {station.label}"
				>
					<span class="rail-station-dot" aria-hidden="true"></span>
					<span class="rail-station-label">{station.label}</span>
				</a>
			{/each}
		</div>
	</div>
</nav>

<style>
	/* Flex row: dot+label blocks joined by solid segments that stop AT the
	   dots — the track never touches a station mark. */
	.rail__track {
		display: flex;
		align-items: flex-start;
		max-width: 34rem;
		margin-inline: auto;
	}

	.rail__seg {
		flex: 1 1 0;
		height: 2px;
		margin-top: calc(0.25rem + 0.375rem - 1px); /* station padding-top + half dot, minus half line */
		background: var(--rail-line);
		min-width: 0.5rem;
	}

	@media (max-width: 480px) {
		.rail__seg {
			min-width: 0.25rem;
		}
	}
</style>
