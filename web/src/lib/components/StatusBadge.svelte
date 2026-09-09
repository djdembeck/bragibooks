<script lang="ts">
	import { normalizeStatus } from '$lib/status';

	let { status }: { status: string } = $props();

	const kind = $derived(normalizeStatus(status));
</script>

<span class="badge status-{kind}" aria-label="Status: {status}">
	{status}
</span>
