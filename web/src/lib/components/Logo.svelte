<script lang="ts">
	/**
	 * Bragi Books mark — an authored, bold glyph for small sizes:
	 * four book spines grounded on a shelf inside a framed badge
	 * (three upright, one leaning). Exact fat geometry; color via currentColor.
	 */
	interface Props {
		class?: string;
	}

	let { class: className = '' }: Props = $props();
</script>

<svg
	class={className}
	viewBox="0 0 80 80"
	fill="none"
	xmlns="http://www.w3.org/2000/svg"
	aria-hidden="true"
	focusable="false"
>
	<!-- Badge frame -->
	<rect x="5" y="5" width="70" height="70" stroke="currentColor" stroke-width="5" />

	<!-- Book stack: three upright, one leaning, on a shelf -->
	<g fill="currentColor">
		<rect x="18" y="22" width="9" height="36" />
		<rect x="30" y="19" width="9" height="39" />
		<rect x="42" y="22" width="9" height="36" />
		<rect x="54" y="24" width="9" height="34" transform="rotate(12 58.5 58)" />
		<rect x="15" y="58" width="50" height="4.5" />
	</g>
</svg>
