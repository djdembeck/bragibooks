<script lang="ts">
	import { get } from '$lib/api';
	import type { BookWithPeople, BooksListResponse } from '$lib/types';

	let books: BookWithPeople[] = $state([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	async function loadBooks() {
		try {
			loading = true;
			error = null;
			const response = await get<BooksListResponse>('/books', {});
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
		<a href="/" class="text-[var(--accent)] font-semibold">Dashboard</a>
		<a href="/books" class="text-[var(--text)] hover:text-[var(--accent)]">Books</a>
		<a href="/import" class="text-[var(--text)] hover:text-[var(--accent)]">Import</a>
		<a href="/process" class="text-[var(--text)] hover:text-[var(--accent)]">Processing</a>
		<a href="/settings" class="text-[var(--text)] hover:text-[var(--accent)]">Settings</a>
	</nav>

	<h1 class="text-3xl font-bold mb-6">Bragi Books</h1>

	<div class="grid grid-cols-3 gap-4 mb-8">
		<div class="bg-[var(--surface)] rounded-lg p-4">
			<h2 class="text-[var(--text-muted)] text-sm mb-1">Total Books</h2>
			<p class="text-2xl font-bold">{books.length}</p>
		</div>
		<div class="bg-[var(--surface)] rounded-lg p-4">
			<h2 class="text-[var(--text-muted)] text-sm mb-1">Done</h2>
			<p class="text-2xl font-bold">{books.filter(b => b.status === 'done').length}</p>
		</div>
		<div class="bg-[var(--surface)] rounded-lg p-4">
			<h2 class="text-[var(--text-muted)] text-sm mb-1">Processing</h2>
			<p class="text-2xl font-bold">{books.filter(b => b.status === 'processing').length}</p>
		</div>
	</div>

	<h2 class="text-xl font-semibold mb-4">Recent Books</h2>

	{#if loading}
		<p class="text-[var(--text-muted)]">Loading...</p>
	{:else if error}
		<p class="text-red-400">{error}</p>
	{:else if books.length === 0}
		<p class="text-[var(--text-muted)]">No books yet. Go to <a href="/import" class="text-[var(--accent)]">Import</a> to get started.</p>
	{:else}
		<div class="space-y-2">
			{#each books.slice(0, 5) as book}
				<a href="/books/{book.id}" class="block bg-[var(--surface)] rounded-lg p-4 hover:bg-[var(--surface-hover)]">
					<div class="flex items-center gap-4">
						{#if book.coverImageUrl}
							<img src="{book.coverImageUrl}" alt="{book.title}" class="w-12 h-16 object-cover rounded" />
						{/if}
						<div class="flex-1">
							<h3 class="font-semibold">{book.title}</h3>
							<p class="text-[var(--text-muted)] text-sm">
								{book.authors.map(a => a.name).join(', ')}
							</p>
						</div>
						<span class="px-2 py-1 rounded text-xs bg-[var(--border)]">{book.status}</span>
					</div>
				</a>
			{/each}
		</div>
	{/if}
</div>