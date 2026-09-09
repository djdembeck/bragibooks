import { get } from '$lib/api';
import { normalizeBook } from '$lib/types';
import type { BookWithPeople } from '$lib/types';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	try {
		const raw = await get<unknown>(`/books/${params.id}`);
		const book: BookWithPeople = normalizeBook(raw);
		return { book, notFound: false };
	} catch {
		// 404 / bad id — render the bench's own empty state instead of
		// SvelteKit's error() so the station chrome stays on screen.
		return { book: null, notFound: true };
	}
};
