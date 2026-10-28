<script lang="ts">
	import type { OutputProfile } from '#lib/types.js';

	interface Props {
		/** Available profiles; "Global settings" is always offered as option "". */
		profiles: OutputProfile[];
		/** null = Global settings; a positive profile id otherwise. Two-way:
		 *  bind:selectedProfileId so the parent's submit state tracks the pick. */
		selectedProfileId: number | null;
		/** Non-empty when the profile list is unavailable (warning row). */
		profilesError?: string | null;
		/** When a profile is selected, the summary row names this many books. */
		batchCount: number;
		/** Noun used in the summary row ("book" / "books"). */
		batchNoun?: string;
		/** noun the batch applies to in the summary row ("batch" / "queue"). */
		batchScope?: string;
		/** Element id the <label> points at; defaults to "output-profile-select". */
		selectId?: string;
		/** Element id of the summary/warning row; defaults to "profile-summary". */
		summaryId?: string;
		/** Extra classes for the label element. */
		labelClass?: string;
		/** Extra classes for the <select> element. */
		selectClass?: string;
		/** Extra classes for the summary/warning <p> element. */
		summaryClass?: string;
	}

	let {
		profiles,
		selectedProfileId = $bindable<number | null>(null),
		profilesError = null,
		batchCount,
		batchNoun = 'books',
		batchScope = 'batch',
		selectId = 'output-profile-select',
		summaryId = 'profile-summary',
		labelClass = '',
		selectClass = '',
		summaryClass = '',
	}: Props = $props();

	// Null-safe when a profile row vanished during a concurrent refresh:
	// the select then renders as Global settings, consistent with the pages'
	// own "return to global" behavior after a selection is applied.
	const selectedProfile = $derived(
		selectedProfileId === null ? null : profiles.find((p) => p.id === selectedProfileId) ?? null
	);

	// Reconcile a bound id that a concurrent deletion (e.g. another tab)
	// removed from the loaded list: clear it so both Book and Queue
	// consumers fall back to Global settings via two-way binding, instead
	// of POSTing an id the backend answers with 404. Skip while the list
	// itself is unavailable — the warning row covers that, and a selection
	// we cannot verify against must not be dropped.
	$effect(() => {
		if (
			selectedProfileId !== null &&
			!profilesError &&
			!profiles.some((p) => p.id === selectedProfileId)
		) {
			selectedProfileId = null;
		}
	});

	function onselectchange(event: Event) {
		const value = (event.currentTarget as HTMLSelectElement).value;
		selectedProfileId = value === '' ? null : Number(value);
	}
</script>

<div class="output-profile-selector">
	<label class="output-profile-selector__label {labelClass}" for={selectId}>Output profile</label>
	<select
		id={selectId}
		class="output-profile-selector__select {selectClass}"
		value={selectedProfileId === null ? '' : String(selectedProfileId)}
		onchange={onselectchange}
		aria-describedby={selectedProfile || profilesError ? summaryId : undefined}
	>
		<option value="">Global settings</option>
		{#each profiles as profile (profile.id)}
			<option value={profile.id}>{profile.name}</option>
		{/each}
	</select>
	{#if selectedProfile}
		<p id={summaryId} class="output-profile-selector__summary {summaryClass}" aria-live="polite">
			{selectedProfile.output_dir || 'Uses the global output directory'} · {selectedProfile.output_scheme || 'Uses the global path format'} — applies only to these {batchCount} {batchNoun} in this {batchScope}.
		</p>
	{:else if profilesError}
		<p id={summaryId} class="output-profile-selector__summary output-profile-selector__summary--warn {summaryClass}" aria-live="polite">{profilesError}</p>
	{/if}
</div>

<style>
	.output-profile-selector {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
		min-width: 0;
	}
	.output-profile-selector__label {
		font-size: 0.8125rem;
		color: var(--text-secondary);
		white-space: nowrap;
	}
	.output-profile-selector__select {
		/* Content-sized: the global width:100% rule would make the select
		 * fill its parent, wrapping the label onto its own line and — with
		 * the summary row inside the same flex container — inflating the
		 * whole selector to the summary sentence's width. A small flex basis
		 * keeps the label beside the select even when a long profile name
		 * would otherwise raise the select's max-content width. */
		flex: 1 1 8rem;
		width: auto;
		max-width: 100%;
		min-width: 0;
		min-height: 2.75rem;
		padding: 0 0.5rem;
		font-size: 0.8125rem;
		color: var(--text);
		background: var(--bg-panel);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
	}
	.output-profile-selector__summary {
		margin: 0;
		flex: 0 0 100%;
		font-size: 0.75rem;
		color: var(--text-muted);
		overflow-wrap: anywhere;
	}
	.output-profile-selector__summary--warn {
		color: var(--state-amber, var(--text-secondary));
	}
</style>
