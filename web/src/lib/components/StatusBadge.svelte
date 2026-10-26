<script lang="ts">
	import { Circle, Clock, Check, AlertCircle, CheckCircle2 } from '@lucide/svelte';
	import { normalizeStatus } from '#lib/status.js';

	let { status }: { status: string } = $props();

	const kind = $derived(normalizeStatus(status));
	const Icon = $derived(
		kind === 'processing' ? Clock
			: kind === 'error' ? AlertCircle
				: kind === 'done' ? Check
					: kind === 'ready' ? CheckCircle2 : Circle
	);
</script>

<span class="badge status-{kind}" aria-label="Status: {status}">
	<Icon size={12} aria-hidden="true" />
	{status}
</span>
