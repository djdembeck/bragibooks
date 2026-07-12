<script lang="ts">
	import { get } from '$lib/api';
	import type { ProcessingJob } from '$lib/types';

	let jobs: ProcessingJob[] = $state([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function loadJobs() {
		try {
			loading = true;
			error = null;
			// Note: backend endpoint for listing jobs would be added as needed
			jobs = [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load jobs';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		loadJobs();
	});
</script>

<div class="p-6 max-w-6xl mx-auto">
	<nav class="flex gap-4 mb-8">
		<a href="/" class="text-[var(--text)] hover:text-[var(--accent)]">Dashboard</a>
		<a href="/books" class="text-[var(--text)] hover:text-[var(--accent)]">Books</a>
		<a href="/import" class="text-[var(--text)] hover:text-[var(--accent)]">Import</a>
		<a href="/process" class="text-[var(--accent)] font-semibold">Processing</a>
		<a href="/settings" class="text-[var(--text)] hover:text-[var(--accent)]">Settings</a>
	</nav>

	<h1 class="text-3xl font-bold mb-2">Processing</h1>
	<p class="text-[var(--text-muted)] mb-6">Monitor active processing jobs.</p>

	{#if loading}
		<p class="text-[var(--text-muted)]">Loading...</p>
	{:else if error}
		<p class="text-red-400">{error}</p>
	{:else if jobs.length === 0}
		<p class="text-[var(--text-muted)]">No active jobs.</p>
	{:else}
		<div class="space-y-2">
			{#each jobs as job}
				<div class="bg-[var(--surface)] rounded-lg p-4">
					<div class="flex items-center gap-4">
						<div class="flex-1">
							<p class="font-semibold">Book #{job.bookId}</p>
							<p class="text-[var(--text-muted)] text-sm">Status: {job.status}</p>
						</div>
						<span class="px-2 py-1 rounded text-xs bg-[var(--border)]">{job.status}</span>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>