package db

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
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
	{
		version: 5,
		sql: `
ALTER TABLE settings ADD COLUMN api_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN api_key_digest TEXT NOT NULL DEFAULT '';

`,
	},
	{
		version: 6,
		sql: `
ALTER TABLE settings ADD COLUMN legacy_token_migrated BOOLEAN NOT NULL DEFAULT 0;

`,
	},
}

// RunMigrations applies all pending schema migrations in order.
// It tracks applied migrations in the migration_log table.
//
// Each migration's SQL statements are executed inside a single
// transaction together with its migration_log INSERT, so a migration is
// either fully applied (schema + log row) or absent — a crash mid-migration
// cannot leave the schema half-applied with the log row already present.
// Within a migration, every statement is applied idempotently:
//   - statements containing "IF NOT EXISTS" or CREATE TRIGGER/INDEX are
//     skipped when the object already exists;
//   - bare "ALTER TABLE ... ADD COLUMN" statements are skipped when the
//     column already exists.
//
// This makes a migration safe to re-apply even without its log row.
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

		name := fmt.Sprintf("v%d", m.version)
		if err := applyMigration(db, name, m.sql); err != nil {
			return fmt.Errorf("migration v%d failed: %w", m.version, err)
		}
	}

	return nil
}

// applyMigration executes stmts in one transaction, ending with the
// migration_log INSERT, so schema change and log entry commit atomically.
func applyMigration(db *sql.DB, name, stmts string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	stmts = strings.TrimSpace(stmts)
	if stmts == "" {
		// Nothing to apply (e.g. an already-satisfied migration): just
		// record it so it is not re-attempted on the next run.
	}
	for _, stmt := range splitStatements(stmts) {
		if stmtIsSkippable(tx, stmt) {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply %q: %w", truncate(stmt, 60), err)
		}
	}

	if _, err := tx.Exec(
		"INSERT INTO migration_log (name) VALUES (?)",
		name,
	); err != nil {
		return fmt.Errorf("failed to record migration %s: %w", name, err)
	}

	return tx.Commit()
}

// splitStatements splits SQL on semicolons, ignoring semicolons inside
// string literals (the migration SQL has none today, but it keeps the
// splitter honest if a future migration needs to).
func splitStatements(sqlText string) []string {
	var out []string
	var cur strings.Builder
	inString := false
	for _, r := range sqlText {
		switch {
		case inString:
			cur.WriteRune(r)
			if r == '\'' {
				inString = false
			}
		case r == '\'':
			inString = true
			cur.WriteRune(r)
		case r == ';':
			s := strings.TrimSpace(cur.String())
			if s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// alterAddColumnRe recognizes a bare ALTER TABLE ... ADD COLUMN statement.
var alterAddColumnRe = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(\w+)\s+ADD\s+(?:COLUMN\s+)?(\w+)`)

// stmtIsSkippable reports whether the statement is already satisfied by the
// current schema and can be skipped (idempotent re-application).
//
// Statements carrying "IF NOT EXISTS" are self-guarding and MUST run (they
// are the idempotent form), so the only form worth prechecking is the bare
// "ALTER TABLE ... ADD COLUMN" used by migrations v2-v4: it is skipped when
// the column already exists, which makes a re-invocation after a lost log
// row safe. Any other bare, non-idempotent statement is executed and would
// fail loudly if its object already exists — the intended behavior for a
// migration that is genuinely not idempotent.
func stmtIsSkippable(tx *sql.Tx, stmt string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	if strings.Contains(upper, "IF NOT EXISTS") {
		return false
	}
	if m := alterAddColumnRe.FindStringSubmatch(stmt); m != nil {
		table, column := m[1], m[2]
		var n int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
			table, column,
		).Scan(&n); err == nil {
			return n > 0
		}
	}
	return false
}

// truncate shortens s to n chars for readable migration errors.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
