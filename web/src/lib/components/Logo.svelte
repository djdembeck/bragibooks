<script lang="ts">
	let { class: className = '' }: { class?: string } = $props();
</script>

<img
	class={className}
	src="/images/logo-small-black.svg"
	width="44"
	height="35"
	alt=""
	aria-hidden="true"
/>
