<script lang="ts">
	import type { OutputProfile } from '#lib/types.js';
	import OutputProfileSelector from '../OutputProfileSelector.svelte';

	interface Props {
		/** Initial bound profile id (null = Global settings). */
		initialId: number | null;
		/** Initial profile list; tests simulate refreshes via refreshProfiles. */
		profiles: OutputProfile[];
		/** Non-empty when the profile list is unavailable. */
		profilesError?: string | null;
	}

	let {
		initialId,
		profiles: initialProfiles,
		profilesError = null
	}: Props = $props();

	// Seeded once from the initial props; every later change goes through
	// refreshProfiles (a profile dropped by a concurrent deletion, a failed
	// fetch) — the same re-fetch path the Book and Queue pages take. The
	// top-level reads intentionally capture the initial values.
	// svelte-ignore state_referenced_locally
	let selectedProfileId = $state<number | null>(initialId);
	// svelte-ignore state_referenced_locally
	let currentProfiles = $state<OutputProfile[]>(initialProfiles);
	// svelte-ignore state_referenced_locally
	let currentError = $state<string | null>(profilesError);

	/** Simulate a profile-list refresh (or its failure) after mount. */
	export function refreshProfiles(next: OutputProfile[], error: string | null = null) {
		currentProfiles = next;
		currentError = error;
	}
</script>

<OutputProfileSelector
	profiles={currentProfiles}
	profilesError={currentError}
	bind:selectedProfileId={selectedProfileId}
	batchCount={2}
	selectId="probe-select"
	summaryId="probe-summary"
/>

<p id="probe-bound">{selectedProfileId ?? 'null'}</p>
