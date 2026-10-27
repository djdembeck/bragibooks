<script lang="ts">
	import { liveNow } from '../liveNow.svelte';

	// Wraps liveNow so a test runs it during real component initialisation
	// (the Svelte client build is on under vitest, so $effect must live
	// inside a mount). `gate` starts closed; `setGate` flips it, which
	// re-runs liveNow's effect, mirroring a page's live/idle transition.
	let gate = $state(false);
	const clock = liveNow(() => gate);

	export function getClock() {
		return clock;
	}

	export function setGate(b: boolean) {
		gate = b;
	}
</script>
