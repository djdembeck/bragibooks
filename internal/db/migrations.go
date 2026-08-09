package db

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
)

// migration is a single schema migration step.
type migration struct {
	version int
	sql     string
}

// migrations is the ordered list of all schema migrations.
// Each migration is idempotent — it uses IF NOT EXISTS where applicable.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE IF NOT EXISTS migration_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT UNIQUE NOT NULL,
	applied_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS books (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	asin TEXT,
	audiobookdb_book_id TEXT,
	audiobookdb_release_id TEXT,
	description TEXT DEFAULT '',
	release_date TEXT,
	series TEXT DEFAULT '',
	publisher TEXT DEFAULT '',
	language TEXT DEFAULT '',
	runtime_length_minutes INTEGER DEFAULT 0,
	format_type TEXT DEFAULT '',
	src_path TEXT DEFAULT '',
	dest_path TEXT DEFAULT '',
	status TEXT DEFAULT 'pending',
	status_message TEXT DEFAULT '',
	cover_image_url TEXT DEFAULT '',
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_books_status ON books(status);
CREATE INDEX IF NOT EXISTS idx_books_asin ON books(asin);
CREATE INDEX IF NOT EXISTS idx_books_audiobookdb_book_id ON books(audiobookdb_book_id);

CREATE TABLE IF NOT EXISTS people (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN ('author', 'narrator')),
	audiobookdb_person_id TEXT,
	created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_people_book_id ON people(book_id);

CREATE TABLE IF NOT EXISTS settings (
	id INTEGER PRIMARY KEY CHECK(id = 1),
	audiobookdb_api_key TEXT DEFAULT '',
	m4b_merge_binary TEXT DEFAULT 'm4b-merge',
	input_dir TEXT DEFAULT '',
	output_dir TEXT DEFAULT '',
	completed_dir TEXT DEFAULT '',
	num_cpus INTEGER DEFAULT 1,
	output_scheme TEXT DEFAULT '{author}/{title}',
	region TEXT DEFAULT 'us',
	created_at TEXT DEFAULT (datetime('now')),
	updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS processing_jobs (
	id TEXT PRIMARY KEY,
	book_id INTEGER REFERENCES books(id) ON DELETE SET NULL,
	m4b_merge_args TEXT,
	status TEXT DEFAULT 'queued',
	output TEXT DEFAULT '',
	error TEXT DEFAULT '',
	output_file TEXT,
	started_at TEXT,
	completed_at TEXT,
	created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_processing_jobs_status ON processing_jobs(status);
CREATE INDEX IF NOT EXISTS idx_processing_jobs_book_id ON processing_jobs(book_id);
`,
	},
	{
		version: 2,
		sql: `
ALTER TABLE books ADD COLUMN converted BOOLEAN DEFAULT 0;

`,
	},
	{
		version: 3,
		sql: `
ALTER TABLE settings ADD COLUMN audiobookdb_base_url TEXT DEFAULT 'https://audiobookdb.org/api';

`,
	},
	{
		version: 4,
		sql: `
ALTER TABLE people ADD COLUMN asin TEXT DEFAULT '';

`,
	},
}

// RunMigrations applies all pending schema migrations in order.
// It tracks applied migrations in the migration_log table.
func RunMigrations(db *sql.DB) error {
	if err := ensureMigrationLogTable(db); err != nil {
		return fmt.Errorf("failed to ensure migration_log table: %w", err)
	}

	applied, err := getAppliedNames(db)
	if err != nil {
		return fmt.Errorf("failed to read applied migrations: %w", err)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for _, m := range migrations {
		if applied[fmt.Sprintf("v%d", m.version)] {
			continue
		}

		log.Printf("Applying migration v%d", m.version)

		_, err := db.Exec(m.sql)
		if err != nil {
			return fmt.Errorf("migration v%d failed: %w", m.version, err)
		}

		if _, err := db.Exec(
			"INSERT INTO migration_log (name) VALUES (?)",
			fmt.Sprintf("v%d", m.version),
		); err != nil {
			return fmt.Errorf("failed to record migration v%d: %w", m.version, err)
		}
	}

	return nil
}

// ensureMigrationLogTable creates the tracking table if it doesn't exist.
func ensureMigrationLogTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS migration_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			applied_at TEXT DEFAULT (datetime('now'))
		)
	`)
	return err
}

// getAppliedNames returns a set of already-applied migration names.
func getAppliedNames(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM migration_log ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}
