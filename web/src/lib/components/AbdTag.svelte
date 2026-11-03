<script lang="ts">
	interface Props {
		/** External AudiobookDB link; opens in a new tab when set. */
		href?: string;
		/** Show the full "AudiobookDB" label next to the mark. */
		withLabel?: boolean;
	}

	let { href, withLabel = false }: Props = $props();
</script>

{#snippet mark()}
	<span class="abd-tag__mark" aria-hidden="true">AbDB</span>
	{#if withLabel}<span class="abd-tag__label">AudiobookDB</span>{/if}
{/snippet}

<!-- The community-sourced marker. Pink is used ONLY here. -->
{#if href}
	<a class="abd-tag" href={href} target="_blank" rel="noopener noreferrer" aria-label="Metadata from AudiobookDB">
		{@render mark()}
	</a>
{:else}
	<span class="abd-tag" aria-label="Metadata from AudiobookDB">
		{@render mark()}
	</span>
{/if}
