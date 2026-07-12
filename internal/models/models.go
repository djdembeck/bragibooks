package models

import "database/sql"

// Book represents an audiobook in the library.
type Book struct {
	ID                   int64          `db:"id" json:"id"`
	Title                string         `db:"title" json:"title"`
	ASIN                 sql.NullString `db:"asin" json:"asin"`
	AudiobookdbBookID    sql.NullString `db:"audiobookdb_book_id" json:"audiobookdb_book_id"`
	AudiobookdbReleaseID sql.NullString `db:"audiobookdb_release_id" json:"audiobookdb_release_id"`
	Description          string         `db:"description" json:"description"`
	ReleaseDate          string         `db:"release_date" json:"release_date"`
	Series               string         `db:"series" json:"series"`
	Publisher            string         `db:"publisher" json:"publisher"`
	Language             string         `db:"language" json:"language"`
	RuntimeLengthMinutes int            `db:"runtime_length_minutes" json:"runtime_length_minutes"`
	FormatType           string         `db:"format_type" json:"format_type"`
	SrcPath              string         `db:"src_path" json:"src_path"`
	DestPath             string         `db:"dest_path" json:"dest_path"`
	Status               string         `db:"status" json:"status"`
	StatusMessage        string         `db:"status_message" json:"status_message"`
	CoverImageURL        string         `db:"cover_image_url" json:"cover_image_url"`
	CreatedAt            string         `db:"created_at" json:"created_at"`
	UpdatedAt            string         `db:"updated_at" json:"updated_at"`
}

// Person represents an author or narrator linked to a book.
type Person struct {
	ID                  int64          `db:"id" json:"id"`
	BookID              int64          `db:"book_id" json:"book_id"`
	Name                string         `db:"name" json:"name"`
	Role                string         `db:"role" json:"role"` // "author" or "narrator"
	AudiobookdbPersonID sql.NullString `db:"audiobookdb_person_id" json:"audiobookdb_person_id"`
	CreatedAt           string         `db:"created_at" json:"created_at"`
}

// BookWithPeople is a Book enriched with its authors and narrators.
type BookWithPeople struct {
	Book
	Authors   []Person `json:"authors"`
	Narrators []Person `json:"narrators"`
}

// ProcessingJob tracks an m4b-merge processing job.
type ProcessingJob struct {
	ID           string         `db:"id" json:"id"`
	BookID       sql.NullInt64  `db:"book_id" json:"book_id"`
	M4bMergeArgs string         `db:"m4b_merge_args" json:"-"` // serialized CLI args
	Status       string         `db:"status" json:"status"`
	Output       string         `db:"output" json:"output"`
	Error        sql.NullString `db:"error" json:"error"`
	OutputFile   sql.NullString `db:"output_file" json:"output_file"`
	StartedAt    sql.NullString `db:"started_at" json:"started_at"`
	CompletedAt  sql.NullString `db:"completed_at" json:"completed_at"`
	CreatedAt    string         `db:"created_at" json:"created_at"`
}

// Settings holds application configuration persisted in the database.
type Settings struct {
	AudiobookdbAPIKey string `db:"audiobookdb_api_key" json:"-"`
	M4bMergeBinary    string `db:"m4b_merge_binary" json:"m4b_merge_binary"`
	InputDir          string `db:"input_dir" json:"input_dir"`
	OutputDir         string `db:"output_dir" json:"output_dir"`
	CompletedDir      string `db:"completed_dir" json:"completed_dir"`
	NumCPUs           int    `db:"num_cpus" json:"num_cpus"`
	OutputScheme      string `db:"output_scheme" json:"output_scheme"`
	Region            string `db:"region" json:"region"`
	CreatedAt         string `db:"created_at" json:"created_at"`
	UpdatedAt         string `db:"updated_at" json:"updated_at"`
}