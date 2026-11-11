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
	// The observer is resident for the life of the layout: a scan started on
	// any page after mount — the normal enable-from-Import journey — must be
	// visible app-wide without a reload. Polling is 1 s while the last
	// response said the index is enabled, 10 s while it is disabled (a tiny
	// local endpoint; this also catches a disable→enable flip started on
	// another page). A transient read failure just keeps polling.
	let indexStatus = $state<IndexStatus | null>(null);
	// Show only while a scan is running AND new items are being indexed:
	// the server counts genuinely new paths (no stored row before this
	// scan) in `added`, which is unknown until the fast walk classifies
	// (absent/0) and 0 for a refresh that finds nothing new — the no-op
	// rescan must stay quiet. A scan that is merely queued (a previous
	// scan still in flight) is covered by that scan's own published count.
	let busy = $derived.by(() => {
		const st = indexStatus?.enabled ? indexStatus.state : null;
		return !!st && st.status === 'running' && (st.added ?? 0) > 0;
	});

	// ONE polling loop for the component's lifetime: the effect below
	// starts it on mount and cancels it on unmount, and reads no reactive
	// state, so it never restarts. The delay is decided at schedule time
	// from the CURRENT enabled flag (1 s while the last response said the
	// index is enabled, 10 s while disabled — a tiny local endpoint; this
	// also catches a disable→enable flip started on another page). Reading
	// indexStatus inside the async loop is outside the effect's tracked
	// scope, so each response write can't restart the loop and collapse
	// the cadence to network round-trips; a transient read failure just
	// keeps polling.
	$effect(() => {
		let cancelled = false;
		const loop = async () => {
			while (!cancelled) {
				try {
					const cur = await get<IndexStatus>('/index');
					if (cancelled) return;
					indexStatus = cur;
				} catch {
					// transient read failure: keep polling
				}
				// Read the CURRENT enabled flag at schedule time: outside
				// the effect's tracked scope, so the write above cannot
				// restart the loop.
				await new Promise((r) => setTimeout(r, indexStatus?.enabled ? 1000 : 10000));
			}
		};
		void loop();
		return () => {
			cancelled = true;
		}
	});

	function progressText(): string {
		const st = indexStatus?.state;
		if (!st) return '';
		const total = st.total ?? 0;
		const extracted = st.extracted ?? 0;
		const added = st.added ?? 0;
		if (total > 0) {
			return `Adding ${added} new sources — reading audio files, ${extracted} of ${total}`;
		}
		// The classify published new paths but the total is not set yet
		// (extraction about to start) — or a tree whose extract set the
		// walk has not finished counting.
		return `Scanning the input directory — ${added} new`;
	}
</script>

{#if busy}
	<div class="index-banner" role="status" aria-live="polite">
		<div class="index-banner__inner">
			<span class="index-banner__text">
				<span class="index-banner__label">Source index <span aria-hidden="true">·</span></span>
				<span class="sr-only">in progress: </span>
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

	/* The quiet name of the operation, so the strip reads as the source
	   index on every page, not just where the context is obvious. */
	.index-banner__label {
		color: var(--text-muted);
		font-weight: 600;
		margin-right: 0.375rem;
	}

	/* Indeterminate rail: the job-card progress grammar (3px amber).
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

	/* Reduced motion: the rail stays as a static activity mark (the shared
	   in-flight grammar) instead of disappearing entirely. */
	@media (prefers-reduced-motion: reduce) {
		.index-banner__rail > span {
			animation: none;
			width: 100%;
		}
	}
</style>
