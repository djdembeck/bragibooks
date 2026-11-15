<script lang="ts">
	import MetadataEditor from './MetadataEditor.svelte';

	/** Test harness: lets a test drive `bookId` like a route change. */
	let { onInit }: { onInit: (setBookId: (id: number) => void) => void } = $props();
	let bookId = $state(1);
	// Read the handler inside the effect body so the prop is tracked
	// reactively (registering it at the top level would capture its
	// initial value, which Svelte warns about). The setter mutates the
	// local bookId, which Svelte 5 allows.
	$effect(() => {
		onInit((id) => (bookId = id));
	});
</script>

<MetadataEditor {bookId} pathFormat={null} />
