<script lang="ts">
	import { createBookSelection, type BookSelection } from '../bookSelection.svelte';

	// Wraps createBookSelection so a test runs it during real component
	// initialisation (the Svelte client build is on under vitest, so the
	// module's $state must live inside a mount).
	const selection = createBookSelection();

	export function getSelection(): BookSelection {
		return selection;
	}
</script>
