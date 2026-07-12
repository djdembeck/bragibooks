<script lang="ts">
	let { status }: { status: string } = $props();

	const kind = $derived.by(() => {
		const normalized = status.toLowerCase();
		if (normalized === 'done' || normalized === 'completed') return 'done';
		if (normalized === 'processing' || normalized === 'running' || normalized === 'queued') return 'processing';
		if (normalized === 'error' || normalized === 'failed') return 'error';
		if (normalized === 'ready' || normalized === 'matched') return 'ready';
		return 'pending';
	});
</script>

<span class="badge status-{kind}">
	{status}
</span>
