/**
 * Wire shape of Go's `sql.NullString` fields: a plain string for valid
 * values, or the serialized struct `{String, Valid}` (with `Valid: false`
 * for an unset value). Field stays camelCase from JSON; `undefined` also
 * covers absent keys on some envelopes.
 */
export type NullString = string | { String: string; Valid: boolean } | null | undefined;

/** Coerce a `NullString` wire value to a plain display string ('' when unset). */
export function normStr(value: NullString): string {
	if (value == null) return '';
	if (typeof value === 'string') return value;
	return value.Valid ? value.String : '';
}

/** Decode the named/numeric HTML entities stored descriptions carry. */
export function decodeHtmlEntities(value: string): string {
	return value
		.replace(/&nbsp;/gi, ' ')
		.replace(/&quot;/gi, '"')
		.replace(/&#39;|&apos;/gi, "'")
		.replace(/&lt;/gi, '<')
		.replace(/&gt;/gi, '>')
		.replace(/&amp;/gi, '&');
}

export interface Book {
	id: number;
	title: string;
	asin: NullString;
	audiobookdb_book_id: NullString;
	audiobookdb_release_id: NullString;
	description: string;
	release_date: string;
	series: string;
	publisher: string;
	language: string;
	runtime_length_minutes: number;
	format_type: string;
	src_path: string;
	dest_path: string;
	status: 'pending' | 'matched' | 'processing' | 'done' | 'error';
	status_message: string;
	cover_image_url: string;
	created_at: string;
	updated_at: string;
	converted: boolean;
}

export interface Person {
	id: number;
	book_id: number;
	name: string;
	role: 'author' | 'narrator';
	audiobookdb_person_id: NullString;
	created_at: string;
}

export interface BookWithPeople {
	id: number;
	title: string;
	asin: NullString;
	audiobookdb_book_id: NullString;
	audiobookdb_release_id: NullString;
	description: string;
	release_date: string;
	series: string;
	publisher: string;
	language: string;
	runtime_length_minutes: number;
	format_type: string;
	src_path: string;
	dest_path: string;
	status: 'pending' | 'matched' | 'processing' | 'done' | 'error';
	status_message: string;
	cover_image_url: string;
	created_at: string;
	updated_at: string;
	authors: Person[];
	narrators: Person[];
}

export interface BooksListResponse {
	books: BookWithPeople[];
	total: number;
	page: number;
	limit: number;
}

export interface CreateBooksRequest {
	books: { src_path: string; title?: string }[];
}

export interface CreateBooksResponse {
	books: BookWithPeople[];
}

export interface UpdateBookRequest {
	title?: string;
	asin?: string;
	audiobookdb_book_id?: string;
	audiobookdb_release_id?: string;
	description?: string;
	release_date?: string;
	series?: string;
	publisher?: string;
	language?: string;
	runtime_length_minutes?: number;
	format_type?: string;
	src_path?: string;
	dest_path?: string;
	status?: 'pending' | 'matched' | 'processing' | 'done' | 'error';
	status_message?: string;
	cover_image_url?: string;
	authors?: { name: string; audiobookdb_person_id?: string | null }[];
	narrators?: { name: string; audiobookdb_person_id?: string | null }[];
}

export interface ProcessingJob {
	id: string;
	book_id: number | null;
	status: 'queued' | 'running' | 'done' | 'error' | 'cancelled';
	output: string;
	error: string | null;
	output_file: string | null;
	started_at: string | null;
	completed_at: string | null;
	created_at: string;
}

export interface JobsListResponse {
	jobs: ProcessingJob[];
}

export interface StatusCounts {
	counts: {
		pending: number;
		matched: number;
		processing: number;
		done: number;
		error: number;
	};
}

export interface Settings {
	audiobookdb_api_key?: string;
	audiobookdb_base_url?: string;
	m4b_merge_binary: string;
	input_dir: string;
	output_dir: string;
	completed_dir: string;
	num_cpus: number;
	output_scheme: string;
	region: string;
	created_at?: string;
	updated_at?: string;
}

export interface DirectoryEntry {
	name: string;
	type: 'file' | 'dir';
	path: string;
	size: number;
	mod_time: string;
}

export interface DirectoriesResponse {
	path: string;
	entries: DirectoryEntry[];
}

export interface SearchHit {
	id: string;
	type: string;
	data: AudiobookDBBook | AudiobookDBRelease | Record<string, unknown>;
}

export interface SearchResponse {
	results: SearchHit[];
}

export interface AudiobookDBImage {
	id: string;
	url: string;
	width: number;
	height: number;
}

export interface AudiobookDBPersonRole {
	role: { name: string };
	person: { id: string; name: string };
}

export interface AudiobookDBExternal {
	type: string;
	id: string;
	url: string;
}

export interface AudiobookDBBook {
	id: string;
	title: string;
	description: string | null;
	/**
	 * Search-hit payloads carry a single preselected cover (`image`,
	 * absolute CDN URL) instead of the `images[]` array that detail
	 * payloads include. Both fields are optional in practice.
	 */
	image?: { url: string; width: number; height: number; blurdata?: string } | null;
	disambiguation: string | null;
	type?: string;
	originallyPublishedAt?: string;
	images: AudiobookDBImage[];
	people: AudiobookDBPersonRole[];
	releases: { id: string; title: string }[];
	series: { seriesId: string; position: number }[];
	external: AudiobookDBExternal[];
	genres: { id: string; title: string }[];
	tags: { id: string; title: string }[];
}

export interface AudiobookDBRelease {
	id: string;
	title: string;
	duration: string;
	runtimeLengthMs: number;
	runtimeLengthSec: number;
	isbn: string | null;
	releaseDate: string | null;
	chapterDetail?: {
		chapters: { title: string; ordinal: number; startOffsetMs: number; lengthMs: number; lengthString: string }[];
	};
	people: AudiobookDBPersonRole[];
	publisher: { id: string; name: string };
	language: { id: string; title: string };
	images: AudiobookDBImage[];
}

export interface ProcessingRequest {
	book_ids: number[];
	src_dirs: string[];
}

export interface ProcessingResponse {
	jobs: Array<{ id: string; book_id: number }>;
}

export interface MigrationResult {
	migrated: { books: number; people: number };
	backup: string;
}

/**
 * Audio container extensions a single-file source may use. Kept in sync with
 * `audioExtensions` in `internal/api/books.go` so the title shown here matches
 * the title the API derived at intake. A period that is not one of these
 * belongs to the name — author initials ("Ursula K. Le Guin"), abbreviations
 * ("Dr."), or volume numbers ("Vol. 2") must survive, otherwise catalog
 * searches run against a truncated title.
 */
export const AUDIO_FILE_EXTS = /\.(mp3|m4a|m4b|mp4|aac|ogg|oga|opus|flac|wav|wma|mka)$/i;

export function formatRuntime(minutes: number): string {
	if (!minutes || minutes <= 0) return '';
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	return `${h}h ${m}m`;
}

/**
 * Book cover URL at the right derivative size for where it renders.
 *
 * ADB cover URLs are storage keys — fetching as-is 404s; append a
 * derivative per the API docs (small 192 / medium 384 / large 768 /
 * detail 1200, .jpg or .webp). If the URL already carries a derivative,
 * it is swapped for the requested one. Anything that is not an ADB
 * storage key (local `/covers/…`, absolute non-CDN images) passes
 * through untouched.
 */
const ADB_COVER_RE = /\/(small|medium|large|detail)\.(jpg|webp)$/i;

export function adbCoverUrl(url: string | null | undefined, size: 'small' | 'medium' | 'large' | 'detail' = 'medium'): string {
	if (!url) return '';
	// Derivative-ready ADB key: swap the size suffix. Covers bare keys
	// (no extension yet) and already-sized ones.
	if (/\/[a-f0-9]{16,}$/i.test(url) || ADB_COVER_RE.test(url)) {
		return url.replace(ADB_COVER_RE, '') + `/${size}.jpg`;
	}
	// Local asset or foreign absolute URL — render as-is.
	return url;
}

/**
 * Preselected search-hit cover (books carry `image` in hit payloads,
 * `images[]` in detail payloads).
 */
export function hitCoverUrl(image: { url: string } | null | undefined, size: 'small' | 'medium' | 'large' | 'detail' = 'small'): string {
	return adbCoverUrl(image?.url, size);
}

export function pickCover(images: AudiobookDBImage[] | undefined): string {
	if (!images || images.length === 0) return '';
	// Prefer the largest image that isn't absurdly huge.
	const img = [...images]
		.sort((a, b) => (b.width || 0) - (a.width || 0))
		.find((img) => (img.width || 0) <= 1200) || images[0];
	return adbCoverUrl(img.url, 'medium');
}

export function extractAsin(external: AudiobookDBExternal[] | undefined): string | null {
	if (!external) return null;
	const amazon = external.find((e) => e.type === 'amazon' || e.type === 'asin');
	if (amazon?.id) return amazon.id;
	// Fallback: extract an ASIN-looking string from the URL path.
	if (amazon?.url) {
		const m = amazon.url.match(/(?:dp|gp\/product)\/([A-Z0-9]{10})/);
		if (m) return m[1];
	}
	return null;
}

export function peopleByRole(
	people: AudiobookDBPersonRole[] | undefined,
	role: string
): { name: string; audiobookdb_person_id?: string | null }[] {
	if (!people) return [];
	return people
		.filter((p) => p.role.name.toLowerCase() === role.toLowerCase())
		.map((p) => ({ name: p.person.name, audiobookdb_person_id: p.person.id || null }));
}

/**
 * Normalize a raw API book payload (which may contain sql.Null* wrappers)
 * into a BookWithPeople with plain values.
 */
export function normalizeBook(raw: unknown): BookWithPeople {
	const r = raw as Record<string, unknown>;
	const unwrap = (v: unknown): string | null => {
		if (v == null) return null;
		if (typeof v === 'object' && 'String' in (v as Record<string, unknown>)) {
			const w = v as { String: string; Valid: boolean };
			return w.Valid ? w.String : null;
		}
		return v as string;
	};
	const unwrapNum = (v: unknown): number =>
		typeof v === 'number' ? v : Number(v) || 0;
	const str = (v: unknown): string => unwrap(v) ?? '';
	const people = (Array.isArray(r.authors) ? r.authors : [])
		.concat(Array.isArray(r.narrators) ? r.narrators : []);
	const toPerson = (p: Record<string, unknown>): Person => ({
		id: unwrapNum(p.id),
		book_id: unwrapNum(p.book_id),
		name: String(p.name ?? ''),
		role: p.role === 'narrator' ? 'narrator' : 'author',
		audiobookdb_person_id: unwrap(p.audiobookdb_person_id),
		created_at: String(p.created_at ?? ''),
	});
	return {
		id: unwrapNum(r.id),
		title: String(r.title ?? ''),
		asin: unwrap(r.asin),
		audiobookdb_book_id: unwrap(r.audiobookdb_book_id),
		audiobookdb_release_id: unwrap(r.audiobookdb_release_id),
		description: str(r.description),
		release_date: str(r.release_date),
		series: str(r.series),
		publisher: str(r.publisher),
		language: str(r.language),
		runtime_length_minutes: unwrapNum(r.runtime_length_minutes),
		format_type: str(r.format_type),
		src_path: str(r.src_path),
		dest_path: str(r.dest_path),
		status: (str(r.status) || 'pending') as BookWithPeople['status'],
		status_message: str(r.status_message),
		cover_image_url: str(r.cover_image_url),
		created_at: str(r.created_at),
		updated_at: str(r.updated_at),
		authors: people.filter((p) => p.role === 'author').map(toPerson),
		narrators: people.filter((p) => p.role === 'narrator').map(toPerson),
	};
}

/**
 * Cover for a stored book record: local `/covers/…` passes through,
 * ADB-stored keys get the requested derivative.
 */
export function bookCoverUrl(url: string | null | undefined, size: 'small' | 'medium' | 'large' | 'detail' = 'medium'): string {
	return adbCoverUrl(url, size);
}
