export interface Book {
	id: number;
	title: string;
	asin: string | null;
	audiobookdb_book_id: string | null;
	audiobookdb_release_id: string | null;
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
	audiobookdb_person_id: string | null;
	created_at: string;
}

export interface BookWithPeople {
	id: number;
	title: string;
	asin: string | null;
	audiobookdb_book_id: string | null;
	audiobookdb_release_id: string | null;
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
	status: 'queued' | 'running' | 'done' | 'error';
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

export function formatRuntime(minutes: number): string {
	if (!minutes || minutes <= 0) return '';
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	return `${h}h ${m}m`;
}

export function pickCover(images: AudiobookDBImage[] | undefined): string {
	if (!images || images.length === 0) return '';
	// Prefer the largest image that isn't absurdly huge.
	const img = [...images]
		.sort((a, b) => (b.width || 0) - (a.width || 0))
		.find((img) => (img.width || 0) <= 1200) || images[0];
	// Image URLs from audiobookdb need a size suffix to resolve.
	if (img.url && !img.url.match(/\/\d+x\d+$|^.*\/original$/)) {
		return img.url + '/750x750';
	}
	return img.url;
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
