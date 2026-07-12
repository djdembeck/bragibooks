<script lang="ts">
	interface Props {
		class?: string;
	}

	let { class: className = '' }: Props = $props();
</script>

<svg
	class={className}
	viewBox="0 0 24 24"
	fill="none"
	xmlns="http://www.w3.org/2000/svg"
	aria-hidden="true"
	focusable="false"
>
	<rect x="4" y="3" width="4" height="18" rx="2" fill="currentColor" />
	<path d="M8 3h5a5 5 0 0 1 0 10H8z" fill="currentColor" />
	<path d="M8 11h5a5 5 0 0 1 0 10H8z" fill="currentColor" />
</svg>
