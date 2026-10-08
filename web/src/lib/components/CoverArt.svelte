<script lang="ts">
	import { bookCoverUrl } from '$lib/types';

	interface Props {
		/** Cover URL in any backend form; a falsy value renders the placeholder spine. */
		url?: string | null;
		/** Book title — becomes meaningful alt text (`"{title} — cover art"`). */
		title?: string;
		/** sm — 48px manifest row · xl — record page hero. */
		size?: 'sm' | 'xl';
	}

	let { url, title, size = 'sm' }: Props = $props();

	const variant = $derived(size === 'xl' ? 'large' : 'small');
</script>

<!-- Square art, one grammar everywhere: cover always square, bench always knows
     when there is none — the placeholder spine stands in, never a stretched image. -->
{#if url}
	<img
		src={bookCoverUrl(url, variant)}
		alt={title ? `${title} — cover art` : ''}
		loading="lazy"
		decoding="async"
		class="cover cover--{size}"
	/>
{:else}
	<span class="cover cover--{size} cover--placeholder" aria-hidden="true">
		<svg viewBox="0 0 16 20" fill="none" xmlns="http://www.w3.org/2000/svg">
			<rect x="1.5" y="1.5" width="13" height="17" rx="2" stroke="currentColor" stroke-width="1.5" />
			<circle cx="8" cy="8" r="3" stroke="currentColor" stroke-width="1.5" />
			<circle cx="8" cy="8" r="0.75" fill="currentColor" />
			<line x1="4" y1="15.5" x2="12" y2="15.5" stroke="currentColor" stroke-width="1.5" />
		</svg>
	</span>
{/if}

<style>
	.cover {
		display: flex;
		flex-shrink: 0;
		align-items: center;
		justify-content: center;
		object-fit: cover;
		background: var(--elevated);
		color: var(--text-muted);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.cover--placeholder svg {
		width: 40%;
		height: 52%;
	}

	.cover--sm {
		width: 3rem;
		height: 3rem;
	}

	.cover--xl {
		width: 16rem;
		max-width: 100%;
		height: auto;
		aspect-ratio: 1 / 1;
		border-color: var(--border);
		border-radius: var(--radius-md);
	}

	.cover--xl.cover--placeholder {
		width: 100%;
	}

	.cover--xl.cover--placeholder svg {
		width: 32%;
		height: 40%;
	}
</style>
