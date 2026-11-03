<script lang="ts">
	import { jobActionLabel } from '#lib/jobActions.js';

	let { action }: { action: string | null | undefined } = $props();

	const label = $derived(jobActionLabel(action));
</script>

<span class="job-action-label" aria-label="Action: {label}">{label}</span>

<style>
	.job-action-label {
		font-size: 0.75rem;
		font-weight: 600;
		color: var(--text-secondary);
		white-space: nowrap;
	}
</style>
