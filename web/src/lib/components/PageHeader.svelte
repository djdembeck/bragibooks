<script lang="ts">
	let { title, description }: { title: string; description?: string } = $props();
</script>

<div class="mb-6 sm:mb-8">
	<h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{title}</h1>
	{#if description}
		<p class="mt-1 max-w-prose text-[var(--text-muted)]">{description}</p>
	{/if}
</div>
