<script lang="ts">
	let { size = '1rem' }: { size?: string } = $props();
</script>

<!-- The one spinner. Size it where it sits; reduced motion slows it, never
     stops it — loading state must stay legible. -->
<span class="spinner" style="width:{size};height:{size}" aria-hidden="true"></span>

<style>
	.spinner {
		display: inline-block;
		flex-shrink: 0;
		border: 2px solid currentColor;
		border-top-color: transparent;
		border-radius: 9999px;
		animation: spinner-rotate 0.9s linear infinite;
	}

	@keyframes spinner-rotate {
		to {
			transform: rotate(360deg);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.spinner {
			animation-duration: 3s;
		}
	}
</style>
