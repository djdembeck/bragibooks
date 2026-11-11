<script lang="ts">
	import {
		comparisonLabels, evidenceFieldLabels, identifierLabels,
		type EvidenceField, type MatchEvidenceReport
	} from '#lib/matchEvidence.js';

	let { report, current }: {
		report: MatchEvidenceReport | undefined;
		current: MatchEvidenceReport | undefined;
	} = $props();
	const readLabels = {
		not_read: 'Source metadata not read', read: 'Source metadata read',
		partial: 'Source metadata partially read', failed: 'Source metadata read failed'
	};
</script>

<div class="source-evidence" data-source-evidence>
	<p><strong>{readLabels[report?.sourceRead.status ?? 'not_read']}</strong></p>
	{#if !report || report.sourceRead.status === 'not_read'}
		<p>Embedded metadata has not been read. The source name is used for title suggestions, not narrator or edition verification.</p>
	{/if}
	{#if report?.sourceRead.detail}<p>{report.sourceRead.detail}</p>{/if}
	{#if report?.selection.bookId && !current}
		<p>No evidence report applies to the currently loaded selection. Source read status does not verify this choice.</p>
	{/if}
	{#if current && (current.sourceRead.status === 'read' || current.sourceRead.status === 'partial')}
		{#each current.identifiers as identifier}
			<div class="source-evidence__item">
				<p><strong>{identifierLabels[identifier.state]}</strong> · {identifier.scope === 'edition' ? 'Edition' : 'Book'}</p>
				<p>Source ID: <code>{identifier.identifier}</code></p>
				<p>From: {identifier.provenance}</p>
				{#if identifier.bookId}<p>Resolved book: <code>{identifier.bookId}</code>{#if identifier.releaseId} · release: <code>{identifier.releaseId}</code>{/if}</p>{/if}
				<p>{identifier.reason}</p>
			</div>
		{/each}
		{#each Object.entries(current.comparisons) as [field, comparison]}
			<div class="source-evidence__item" data-evidence-detail={field}>
				<p><strong>{evidenceFieldLabels[field as EvidenceField]}</strong> · {comparisonLabels[comparison.state]}</p>
				<dl>
					<div><dt>Source</dt><dd>{comparison.source || 'Not available in source'}</dd></div>
					<div><dt>Selected catalog</dt><dd>{comparison.selected || 'Not available in catalog'}</dd></div>
				</dl>
				<p>From: {comparison.provenance}</p>
				<p>{comparison.reason}</p>
			</div>
		{/each}
	{/if}
</div>

<style>
	.source-evidence { margin-top: 0.75rem; max-width: 65ch; line-height: 1.5; overflow-wrap: anywhere; }
	.source-evidence p { margin: 0.3rem 0; }
	.source-evidence__item { border-top: 1px solid var(--border); padding-top: 0.5rem; margin-top: 0.75rem; }
	dl { margin: 0.4rem 0; }
	dl div { margin: 0.25rem 0; }
	dt { font-weight: 600; }
	dd { margin: 0; }
</style>
