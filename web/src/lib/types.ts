export interface Book {
	id: number;
	title: string;
	asin: string | null;
	audiobookdbBookId: string | null;
	audiobookdbReleaseId: string | null;
	description: string;
	releaseDate: string;
	series: string;
	publisher: string;
	language: string;
	runtimeLengthMinutes: number;
	formatType: string;
	srcPath: string;
	destPath: string;
	status: 'pending' | 'processing' | 'done' | 'error';
	statusMessage: string;
	coverImageUrl: string;
	createdAt: string;
	updatedAt: string;
}

export interface Person {
	id: number;
	bookId: number;
	name: string;
	role: 'author' | 'narrator';
	audiobookdbPersonId: string | null;
	createdAt: string;
}

export interface BookWithPeople {
	id: number;
	title: string;
	asin: string | null;
	audiobookdbBookId: string | null;
	audiobookdbReleaseId: string | null;
	description: string;
	releaseDate: string;
	series: string;
	publisher: string;
	language: string;
	runtimeLengthMinutes: number;
	formatType: string;
	srcPath: string;
	destPath: string;
	status: 'pending' | 'processing' | 'done' | 'error';
	statusMessage: string;
	coverImageUrl: string;
	createdAt: string;
	updatedAt: string;
	authors: Person[];
	narrators: Person[];
}

export interface BooksListResponse {
	books: BookWithPeople[];
	total: number;
	page: number;
	limit: number;
}

export interface ProcessingJob {
	id: string;
	bookId: number | null;
	status: 'queued' | 'running' | 'done' | 'error';
	output: string;
	error: string | null;
	outputFile: string | null;
	startedAt: string | null;
	completedAt: string | null;
	createdAt: string;
}

export interface Settings {
	m4bMergeBinary: string;
	inputDir: string;
	outputDir: string;
	completedDir: string;
	numCpus: number;
	outputScheme: string;
	region: string;
	createdAt: string;
	updatedAt: string;
}

export interface DirectoryEntry {
	name: string;
	type: 'file' | 'dir';
	path: string;
}

export interface DirectoriesResponse {
	entries: DirectoryEntry[];
}

export interface SearchHit {
	id: string;
	type: string;
	data: Record<string, unknown>;
}

export interface SearchResponse {
	results: SearchHit[];
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