<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';

	let books: BookWithPeople[] = $state([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let filter = $state<'all' | 'done' | 'processing' | 'error'>('all');

	async function loadBooks() {
		try {
			loading = true;
			error = null;
			const response = await get<BooksListResponse>(`/books?status=${filter}`, {});
			books = response.books || [];
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load books';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		loadBooks();
	});
</script>

<div class="p-6 max-w-6xl mx-auto">
	<nav class="flex gap-4 mb-8">
		<a href="/" class="text-[var(--text)] hover:text-[var(--accent)]">Dashboard</a>
		<a href="/books" class="text-[var(--accent)] font-semibold">Books</a>
		<a href="/import" class="text-[var(--text)] hover:text-[var(--accent)]">Import</a>
		<a href="/process" class="text-[var(--text)] hover:text-[var(--accent)]">Processing</a>
		<a href="/settings" class="text-[var(--text)] hover:text-[var(--accent)]">Settings</a>
	</nav>

	<h1 class="text-3xl font-bold mb-6">Books</h1>

	<div class="flex gap-2 mb-6">
		{#each ['all', 'done', 'processing', 'error'] as status}
			<button
				class="px-4 py-2 rounded {filter === status ? 'bg-[var(--accent)] text-[var(--bg)]' : 'bg-[var(--surface)] text-[var(--text)] hover:bg-[var(--surface-hover)]'}"
				onclick={() => filter = status}
			>
				{status.charAt(0).toUpperCase() + status.slice(1)}
			</button>
		{/each}
	</div>

	{#if loading}
		<p class="text-[var(--text-muted)]">Loading...</p>
	{:else if error}
		<p class="text-red-400">{error}</p>
	{:else if books.length === 0}
		<p class="text-[var(--text-muted)]">No books found.</p>
	{:else}
		<div class="space-y-2">
			{#each books as book}
				<a href="/books/{book.id}" class="block bg-[var(--surface)] rounded-lg p-4 hover:bg-[var(--surface-hover)]">
					<div class="flex items-center gap-4">
						{#if book.coverImageUrl}
							<img src="{book.coverImageUrl}" alt="{book.title}" class="w-12 h-16 object-cover rounded" />
						{/if}
						<div class="flex-1">
							<h3 class="font-semibold">{book.title}</h3>
							<p class="text-[var(--text-muted)] text-sm">
								{book.authors.map(a => a.name).join(', ')} — {book.narrators.map(n => n.name).join(', ')}
							</p>
							{#if book.series}<p class="text-[var(--text-muted)] text-xs">{book.series}</p>{/if}
						</div>
						<span class="px-2 py-1 rounded text-xs bg-[var(--border)]">{book.status}</span>
					</div>
				</a>
			{/each}
		</div>
	{/if}
</div>