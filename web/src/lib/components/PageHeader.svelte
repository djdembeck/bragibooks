<script lang="ts">
	let { title, description, station }: { title: string; description?: string; station?: string } = $props();
</script>

<div class="mb-5 sm:mb-7">
	<div class="flex flex-wrap items-center gap-3">
		<h1 class="text-2xl font-bold tracking-tight sm:text-3xl text-[var(--enamel)]">{title}</h1>
		{#if station}
			<span class="station-id">{station}</span>
		{/if}
	</div>
	{#if description}
		<p class="mt-1 max-w-prose text-[var(--text-secondary)]">{description}</p>
	{/if}
</div>
