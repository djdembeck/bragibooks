import type { AudiobookDBRelease } from './types';

/** Release publisher credits as the single string the catalog stores.
 *  Upstream sends a `publishers` array ordered by position (the singular
 *  `publisher` key is null); multiple credits are joined with ", ". */
export function publisherName(
	release: Pick<AudiobookDBRelease, 'publishers'> | null | undefined
): string | undefined {
	const names = (release?.publishers ?? [])
		.map((publisher) => publisher.name?.trim())
		.filter((name): name is string => Boolean(name));
	return names.length > 0 ? names.join(', ') : undefined;
}
