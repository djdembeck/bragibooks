<script lang="ts">
	import type { SourceASINEvidence } from '#lib/types.js';
	import {
		MISSING_RELEASE_HELPER,
		missingReleaseBody,
		missingReleaseInline,
		missingReleaseLinkLabel,
		missingReleaseProvenanceLabel,
		missingReleaseView,
		type MissingReleaseContext
	} from '#lib/missingRelease.js';

	let {
		evidence = null,
		variant = 'block',
		context = 'batch'
	}: {
		evidence?: SourceASINEvidence | null;
		variant?: 'block' | 'inline';
		context?: MissingReleaseContext;
	} = $props();

	const view = $derived(missingReleaseView(evidence));
	const inline = $derived(variant === 'inline' ? missingReleaseInline(view, context) : null);
	const blockLinkText = 'Add this audiobook to AudiobookDB';
	// The accessible name must start with the visible link text.
	const linkLabel = $derived(missingReleaseLinkLabel(view, inline ? inline.linkText : blockLinkText));
</script>

{#if variant === 'block'}
	<section class="nudge" aria-label="Missing from AudiobookDB">
		<h3 class="nudge__heading">Missing from AudiobookDB?</h3>
		<p class="nudge__body">{missingReleaseBody(view)}</p>
		{#if view.state === 'present' && view.asin}
			<p class="nudge__asin">
				<span class="nudge__asin-label">ASIN</span>
				<code>{view.asin}</code>
				<span class="nudge__asin-source">· {missingReleaseProvenanceLabel(view.provenance)}</span>
			</p>
		{/if}
		<p class="nudge__action">
			<a
				class="nudge__link"
				href={view.addUrl}
				target="_blank"
				rel="noopener noreferrer"
				aria-label={linkLabel}
			>
				{blockLinkText}
				<svg class="nudge__icon" viewBox="0 0 24 24" aria-hidden="true">
					<path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" />
				</svg>
			</a>
		</p>
		<p class="nudge__helper">{MISSING_RELEASE_HELPER}</p>
	</section>
{:else if inline}
	<p
		class="nudge-inline"
		class:nudge-inline--batch={context === 'batch'}
		class:nudge-inline--editor={context === 'editor'}
		class:nudge-inline--search={context === 'search'}
	>
		{inline.lead}<span class="nudge-inline__action"><a class="nudge-inline__link" href={view.addUrl} target="_blank" rel="noopener noreferrer" aria-label={linkLabel}>{inline.linkText}<svg class="nudge-inline__icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg></a>{inline.tail}</span>
	</p>
{/if}

<style>
	.nudge {
		margin-top: 18px;
		padding-top: 14px;
		max-width: 52ch;
		border-top: 1px solid var(--m-border, var(--border));
	}

	.nudge__heading {
		margin: 0;
		color: var(--m-ink-2, var(--text-secondary));
		font-size: 14px;
		font-weight: 600;
		line-height: 1.4;
	}

	.nudge__body {
		margin: 6px 0 0;
		color: var(--m-muted, var(--text-muted));
		font-size: 13.5px;
		line-height: 1.5;
	}

	.nudge__asin {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 2px 6px;
		margin: 10px 0 0;
		color: var(--m-muted, var(--text-muted));
		font-size: 12.5px;
		line-height: 1.5;
	}

	.nudge__asin-label {
		color: var(--m-ink-2, var(--text-secondary));
		font-size: 12px;
		font-weight: 600;
	}

	.nudge__asin code {
		color: var(--m-ink-2, var(--text-secondary));
		font-family: ui-monospace, 'Cascadia Code', 'JetBrains Mono', 'Fira Code', 'SF Mono', 'Consolas', monospace;
		font-size: 13px;
		overflow-wrap: anywhere;
	}

	.nudge__action {
		margin: 10px 0 0;
	}

	.nudge__link {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		min-height: 44px;
		color: var(--workspace-action, var(--action));
		font-size: 14px;
		font-weight: 600;
		line-height: 1.4;
		text-decoration: none;
		overflow-wrap: anywhere;
	}

	.nudge__link:hover {
		text-decoration: underline;
		text-underline-offset: 3px;
	}

	.nudge__link:focus-visible {
		outline: 3px solid var(--focus-ring, var(--action));
		outline-offset: 3px;
		border-radius: 3px;
	}

	.nudge__icon,
	.nudge-inline__icon {
		width: 13px;
		height: 13px;
		flex: none;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.7;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.nudge__helper {
		margin: 2px 0 0;
		color: var(--m-muted, var(--text-muted));
		font-size: 12px;
		line-height: 1.5;
	}

	/* Action scale:
	   block = primary recovery (14px, 44px)
	   search/editor = contextual recovery (13.5px, 32px desktop / 44px phone)
	   batch = inline scan hint (13px, 24px desktop / 32px phone) */
	.nudge-inline {
		margin: 6px 0;
		color: var(--m-muted, var(--text-muted));
		font-size: 13.5px;
		line-height: 1.5;
		overflow-wrap: anywhere;
	}

	.nudge-inline--batch {
		font-size: 13px;
		line-height: 1.45;
	}

	.nudge-inline--editor {
		margin: 16px 0 0;
		padding-top: 12px;
		border-top: 1px solid var(--m-border-soft, var(--border-subtle));
	}

	.nudge-inline--search {
		margin: 8px 0 0;
	}

	/* inline-flex, not flex: the span sits inside a sentence ("Not the right
	   book? Add to AudiobookDB."), and a block-level box would break that
	   sentence onto its own line and strand the trailing punctuation. The
	   32px target is kept by min-height on the inline box. */
	.nudge-inline__action {
		display: inline-flex;
		align-items: center;
		min-height: 32px;
		white-space: nowrap;
	}

	.nudge-inline__link {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		height: 32px;
		padding: 0 3px;
		margin: 0 -3px;
		color: var(--workspace-action, var(--action));
		font-size: 13.5px;
		font-weight: 600;
		text-decoration: underline;
		text-decoration-thickness: 1px;
		text-underline-offset: 2px;
	}

	/* Batch keeps the same inline box and differs only in target size: the
	   scan hint sits inside a dense table row and does not need 32px. */
	.nudge-inline--batch .nudge-inline__action {
		min-height: 0;
	}

	.nudge-inline--batch .nudge-inline__link {
		height: auto;
		min-height: 24px;
		padding: 0;
		margin: 0;
		font-size: 13px;
	}

	@media (max-width: 900px) {
		.nudge-inline--batch .nudge-inline__link {
			min-height: 32px;
		}

		.nudge-inline--search .nudge-inline__action,
		.nudge-inline--editor .nudge-inline__action {
			min-height: 44px;
		}

		.nudge-inline--search .nudge-inline__link,
		.nudge-inline--editor .nudge-inline__link {
			height: 44px;
		}
	}

	@media (max-width: 370px) {
		.nudge-inline--search .nudge-inline__action,
		.nudge-inline--editor .nudge-inline__action {
			flex-wrap: wrap;
			white-space: normal;
		}

		.nudge-inline--search .nudge-inline__link,
		.nudge-inline--editor .nudge-inline__link {
			height: auto;
			min-height: 44px;
			max-width: 100%;
			min-width: 0;
			flex-wrap: wrap;
			white-space: normal;
			overflow-wrap: anywhere;
		}
	}

	.nudge-inline__link:hover {
		text-decoration-thickness: 2px;
	}

	.nudge-inline__link:focus-visible {
		outline: 3px solid var(--focus-ring, var(--action));
		outline-offset: 3px;
		border-radius: 3px;
	}
</style>
