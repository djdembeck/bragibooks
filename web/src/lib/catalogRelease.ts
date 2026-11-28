/** The catalog edition a book record describes: the release Match saved, or
 *  the first catalog release for records matched before an edition was kept. */
export function catalogReleaseId(
	savedReleaseId: string,
	releases: { id: string }[] | null | undefined
): string {
	return savedReleaseId || releases?.[0]?.id || '';
}
