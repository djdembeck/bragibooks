<script lang="ts">
	import type { BookWithPeople } from '$lib/types';
	import { formatRuntime, bookCoverUrl } from '$lib/types';
	import { AbdTag, SpliceMeter, StatusBadge } from '$lib/components';

	interface Props {
		book: BookWithPeople;
		/** Defaults to the detail page; the whole row links here. */
		href?: string;
		/** Dense rows for catalog lists. */
		dense?: boolean;
	}

	let { book, href, dense = false }: Props = $props();

	const target = $derived(href ?? `/books/${book.id}`);

	const kind = $derived.by(() => {
		const normalized = book.status.toLowerCase();
		if (normalized === 'done' || normalized === 'completed') return 'done';
		if (normalized === 'processing' || normalized === 'running' || normalized === 'queued') return 'processing';
		if (normalized === 'error' || normalized === 'failed') return 'error';
		if (normalized === 'ready' || normalized === 'matched') return 'ready';
		return 'pending';
	});

	const authors = $derived(book.authors?.map((a) => a.name).join(', '));
	const narrators = $derived(book.narrators?.map((n) => n.name).join(', '));

	const meta = $derived(
		[authors, narrators && `Narrated by ${narrators}`, book.runtime_length_minutes ? formatRuntime(book.runtime_length_minutes) : null]
			.filter(Boolean)
			.join(' · ')
	);

	const counter = $derived(
		book.runtime_length_minutes && book.runtime_length_minutes > 0 ? formatRuntime(book.runtime_length_minutes) : `#${book.id}`
	);
</script>

<!-- A reel on the rack: end cap + cover + lit label + status.
     The whole row is a link to the book's detail page.        -->
<a
	href={target}
	class="reel-row no-underline"
	class:reel-row--dense={dense}
	aria-label="{book.title}"
>
	<!-- End cap / spindle — status shape glyph + mono counter -->
	<span class="reel-row__cap" aria-hidden="true">
		<span class="status-glyph status-{kind}" aria-hidden="true"></span>
		<span class="reel-row__counter tabular-nums">{counter}</span>
	</span>

	<!-- Cover — a rounded reel, or a placeholder can -->
	{#if book.cover_image_url}
		<img src={bookCoverUrl(book.cover_image_url, 'medium')} alt="" class="reel-row__cover" loading="lazy" />
	{:else}
		<span class="reel-row__cover reel-row__cover--placeholder" aria-hidden="true">
			<svg width="40%" height="52%" viewBox="0 0 16 20" fill="none" xmlns="http://www.w3.org/2000/svg">
				<rect x="1.5" y="1.5" width="13" height="17" rx="2" stroke="currentColor" stroke-width="1.5" />
				<circle cx="8" cy="8" r="3" stroke="currentColor" stroke-width="1.5" />
				<circle cx="8" cy="8" r="0.75" fill="currentColor" />
				<line x1="4" y1="15.5" x2="12" y2="15.5" stroke="currentColor" stroke-width="1.5" />
			</svg>
		</span>
	{/if}

	<!-- Label block — the lit title, the richer secondary line -->
	<span class="reel-row__label">
		<span class="reel-row__title block">{book.title}</span>
		{#if meta}
			<span class="reel-row__meta block">
				{meta}
				{#if book.audiobookdb_book_id}
					<AbdTag />
				{/if}
			</span>
		{:else if book.audiobookdb_book_id}
			<span class="reel-row__meta block"><AbdTag /></span>
		{/if}
	</span>

	<!-- Status — the splice tells the story; the badge names it -->
	<span class="reel-row__status">
		<SpliceMeter status={book.status} runtimeMinutes={book.runtime_length_minutes} />
		<StatusBadge status={book.status} />
	</span>
</a>
