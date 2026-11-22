<script lang="ts">
	import PathFormatBuilder from '../PathFormatBuilder.svelte';

	/** Test harness: drives outputDir like a settings page would. */
	let { value, initialOutputDir, onInit }: {
		value: string;
		initialOutputDir: string;
		onInit: (setOutputDir: (dir: string) => void) => void;
	} = $props();
	let outputDir = $state(initialOutputDir);
	// Read the handler inside the effect body so the prop is tracked
	// reactively (registering it at the top level would capture its
	// initial value, which Svelte warns about). The setter mutates the
	// local outputDir, which Svelte 5 allows.
	$effect(() => {
		onInit((dir) => (outputDir = dir));
	});
</script>

<PathFormatBuilder {value} {outputDir} />
