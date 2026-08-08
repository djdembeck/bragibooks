<script lang="ts">
	let { title, description, station }: { title: string; description?: string; station?: string } = $props();
</script>

<header class="page-header">
	<div class="page-header__row">
		<h1 class="page-header__title">{title}</h1>
		{#if station}
			<span class="station-id page-header__station">{station}</span>
		{/if}
	</div>
	{#if description}
		<p class="page-header__description">{description}</p>
	{/if}
</header>
