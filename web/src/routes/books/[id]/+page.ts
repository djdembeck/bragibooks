import { ApiError, get } from '$lib/api';
import { normalizeBook } from '$lib/types';
import { toCleanedError } from '$lib/adbError';
import type { BookWithPeople } from '$lib/types';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	try {
		const raw = await get<unknown>(`/books/${params.id}`);
		const book: BookWithPeople = normalizeBook(raw);
		return { book, notFound: false, loadError: null };
	} catch (e) {
		// A missing book is expected (stale id, deleted reel) — render the
		// bench's own empty state instead of SvelteKit's error() so the
		// station chrome stays on screen. Anything else is a real failure the
		// operator must see, not a silent "not found".
		if (e instanceof ApiError && e.status === 404) {
			return { book: null, notFound: true, loadError: null };
		}
		return { book: null, notFound: false, loadError: toCleanedError(e) };
	}
};
