<script lang="ts">
	let {
		variant = 'line',
		width = '100%',
		height,
		class: className = ''
	}: {
		variant?: 'line' | 'circle' | 'rect';
		width?: string;
		height?: string;
		class?: string;
	} = $props();

	const style = $derived.by(() => {
		const resolvedWidth = variant === 'circle' && !width ? '2.5rem' : width;
		const resolvedHeight = height ? height : variant === 'circle' ? '2.5rem' : '';
		const radius = variant === 'circle' ? '9999px' : '';
		return `
			width: ${resolvedWidth};
			height: ${resolvedHeight};
			border-radius: ${radius};
		`.trim();
	});
</script>

<div class="skeleton {className}" style={style} aria-hidden="true"></div>
