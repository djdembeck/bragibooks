<script lang="ts">
	import { get } from '#lib/api.js';
	import type { IndexStatus } from '#lib/types.js';

	// App-wide "source index is working" banner, mounted once in the root
	// layout directly under the nav. Shows only while a scan is in flight
	// (queued/running): extraction progress as "X of N files" once the slow
	// per-file pass has its count, a plain phase line before that. Terminal
	// states are owned by the pages that act on them (Import's status line,
	// Settings' status row), so the banner never announces completion.
	//
	// Polling mirrors the in-page pollers: 1 s while in flight, stopped on
	// any terminal status; a transient read failure just keeps polling.
	let indexStatus = $state<IndexStatus | null>(null);
	let busy = $derived(
		!!indexStatus?.enabled &&
			(indexStatus.state.status === 'queued' || indexStatus.state.status === 'running')
	);

	// Fetch once on mount, then keep polling every 1 s while a scan is in
	// flight; stop after any terminal status. (Polling only "while busy"
	// would never see the first in-flight state — busy comes from this
	// fetch.) A transient read failure just keeps polling.
	$effect(() => {
		let cancelled = false;
		const poll = async (): Promise<boolean> => {
			try {
				const cur = await get<IndexStatus>('/index');
				if (cancelled) return false;
				indexStatus = cur;
				return cur.enabled &&
					(cur.state.status === 'queued' || cur.state.status === 'running');
			} catch {
				return true;
			}
		};
		(async () => {
			while (!cancelled) {
				if (!(await poll())) return;
				await new Promise((r) => setTimeout(r, 1000));
			}
		})();
		return () => {
			cancelled = true;
		};
	});

	function progressText(): string {
		const st = indexStatus?.state;
		if (!st) return '';
		const total = st.total ?? 0;
		const extracted = st.extracted ?? 0;
		if (total > 0) {
			return `Reading audio files — ${extracted} of ${total}`;
		}
		if (st.status === 'queued') return 'Index queued — starting…';
		return 'Scanning the input directory…';
	}
</script>

{#if busy}
	<div class="index-banner" role="status" aria-live="polite">
		<div class="index-banner__inner">
			<span class="index-banner__text">
				<span class="sr-only">Source index in progress: </span>
				{progressText()}
			</span>
			<div class="index-banner__rail" aria-hidden="true"><span></span></div>
		</div>
	</div>
{/if}

<style>
	/* Full-width strip under the gold nav; content aligns with
	   .workspace-main (90rem cap, 2.5rem / 1rem side padding). */
	.index-banner {
		background: var(--surface);
		border-bottom: 1px solid var(--border-subtle);
	}

	.index-banner__inner {
		position: relative;
		width: 100%;
		max-width: 90rem;
		margin-inline: auto;
		padding: 0.5rem 2.5rem;
		min-height: 2.5rem;
		display: flex;
		align-items: center;
		gap: 1rem;
		font-size: 0.8125rem;
		color: var(--text-secondary);
	}

	.index-banner__text {
		flex-shrink: 0;
	}

	/* Indeterminate rail: the job-card progress grammar (4px amber).
	   Placed as a bottom-edge line so the strip stays one line tall. */
	.index-banner__rail {
		position: absolute;
		left: 0;
		right: 0;
		bottom: -1px;
		height: 3px;
		background: var(--state-amber-border);
		overflow: hidden;
	}

	.index-banner__rail > span {
		display: block;
		width: 35%;
		height: 100%;
		background: var(--state-amber);
		animation: index-banner-rail 1.5s linear infinite;
	}

	@keyframes index-banner-rail {
		0% { transform: translateX(-105%); }
		100% { transform: translateX(300%); }
	}

	@media (max-width: 900px) {
		.index-banner__inner {
			padding: 0.5rem 1rem;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.index-banner__rail {
			display: none;
		}
	}
</style>
