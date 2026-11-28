/** Catalog relation IDs are opaque ASCII path segments. Only unreserved
 *  characters are accepted, so an ID from storage cannot change the request
 *  path, query, or fragment. */
const CATALOG_ID = /^[A-Za-z0-9_-]+$/;

export function isCatalogId(value: string): boolean {
	return CATALOG_ID.test(value);
}

/** The catalog edition a book record describes: the release Match saved, or
 *  the first catalog release for records matched before an edition was kept.
 *  Returns no edition when the saved ID is malformed or the expanded catalog
 *  book lists other releases, so the page cannot show another book's
 *  metadata. */
export function catalogReleaseId(
	savedReleaseId: string,
	releases: { id: string }[] | null | undefined
): string {
	const saved = savedReleaseId.trim();
	if (saved) {
		if (!isCatalogId(saved)) return '';
		// An expanded, non-empty release list that omits the saved edition
		// disproves it. Empty or missing releases leave membership unknown,
		// so the saved edition stays the best available record.
		if (releases && releases.length > 0 && !releases.some((release) => release.id === saved)) {
			return '';
		}
		return saved;
	}
	const first = releases?.[0]?.id ?? '';
	return isCatalogId(first) ? first : '';
}
