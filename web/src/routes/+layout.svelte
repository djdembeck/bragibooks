<script lang="ts">
	import '../app.css';
</script>

<svelte:head>
	<meta charset="utf-8" />
	<meta name="viewport" content="width=device-width, initial-scale=1" />
	<link rel="icon" href="%sveltekit.assets%/favicon.svg" />
	<title>Bragi Books</title>
</svelte:head>

<div class="min-h-screen bg-[oklch(0.18_0.01_270)] text-[oklch(0.85_0.01_270)]">
	<slot />
</div>