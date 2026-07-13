package api

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// --- Schema setup helpers ---

func setupLegacySchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS importer_status (
			id INTEGER PRIMARY KEY,
			status TEXT
		);
		CREATE TABLE IF NOT EXISTS importer_book (
			id INTEGER PRIMARY KEY,
			title TEXT,
			asin TEXT,
			short_desc TEXT,
			long_desc TEXT,
			release_date TEXT,
			series TEXT,
			publisher TEXT,
			lang TEXT,
			runtime_length_minutes INTEGER,
			format_type TEXT,
			src_path TEXT,
			dest_path TEXT,
			status_id INTEGER,
			cover_image_link TEXT,
			created_at TEXT,
			updated_at TEXT,
			converted BOOLEAN DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS importer_author (
			id INTEGER PRIMARY KEY,
			first_name TEXT,
			last_name TEXT
		);
		CREATE TABLE IF NOT EXISTS importer_narrator (
			id INTEGER PRIMARY KEY,
			first_name TEXT,
			last_name TEXT
		);
		CREATE TABLE IF NOT EXISTS importer_author_books (
			id INTEGER PRIMARY KEY,
			book_id INTEGER,
			author_id INTEGER
		);
		CREATE TABLE IF NOT EXISTS importer_narrator_books (
			id INTEGER PRIMARY KEY,
			book_id INTEGER,
			narrator_id INTEGER
		);
		CREATE TABLE IF NOT EXISTS importer_setting (
			id INTEGER PRIMARY KEY,
			input_directory TEXT,
			output_directory TEXT,
			completed_directory TEXT,
			num_cpus INTEGER,
			output_scheme TEXT
		)
	`)
	return err
}

func setupNewSchema(db *sql.DB) error {
	_, err := db.Exec(`
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
			updated_at TEXT DEFAULT (datetime('now')),
			converted BOOLEAN DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS people (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			role TEXT NOT NULL CHECK(role IN ('author', 'narrator')),
			audiobookdb_person_id TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
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
		)
	`)
	return err
}

func newLegacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := setupLegacySchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newEmptyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// --- TestMigrateBooks ---

func TestMigrateBooks(t *testing.T) {
	legacy := newLegacyDB(t)
	t.Cleanup(func() { legacy.Close() })

	newDB := newEmptyDB(t)
	t.Cleanup(func() { newDB.Close() })
	if err := setupNewSchema(newDB); err != nil {
		t.Fatal(err)
	}

	// Insert status rows
	_, err := legacy.Exec(`INSERT INTO importer_status (id, status) VALUES (1, 'Done'), (2, 'Processing'), (3, 'Error')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 3 books with different statuses and converted values
	_, err = legacy.Exec(`INSERT INTO importer_book (id, title, asin, short_desc, long_desc,
		release_date, series, publisher, lang, runtime_length_minutes, format_type, src_path, dest_path,
		cover_image_link, created_at, updated_at, status_id, converted)
		VALUES
		(1, 'Book One', 'B001', 'short1', 'long1', '2024-01-01', '', 'PubA', 'en', 120, 'm4b', '', '', '', datetime('now'), datetime('now'), 1, 1),
		(2, 'Book Two', 'B002', 'short2', 'long2', '2024-02-01', '', 'PubB', 'en', 90, 'm4b', '', '', '', datetime('now'), datetime('now'), 2, 0),
		(3, 'Book Three', '', 'short3', '', '2024-03-01', '', 'PubC', 'en', 60, 'm4b', '', '', '', datetime('now'), datetime('now'), 3, 1)
	`)
	if err != nil {
		t.Fatal(err)
	}

	count, err := migrateBooks(legacy, newDB)
	if err != nil {
		t.Fatalf("migrateBooks error: %v", err)
	}
	if count != 3 {
		t.Errorf("migrateBooks count = %d, want 3", count)
	}

	type bookWant struct {
		title     string
		asin      sql.NullString
		desc      string
		publisher string
		status    string
		converted bool
	}
	wants := []bookWant{
		{title: "Book One", asin: sql.NullString{String: "B001", Valid: true}, desc: "short1 long1", publisher: "PubA", status: "done", converted: true},
		{title: "Book Two", asin: sql.NullString{String: "B002", Valid: true}, desc: "short2 long2", publisher: "PubB", status: "processing", converted: false},
		{title: "Book Three", asin: sql.NullString{Valid: false}, desc: "short3", publisher: "PubC", status: "error", converted: true},
	}

	rows, err := newDB.Query("SELECT title, asin, description, publisher, status, converted FROM books ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		if i >= len(wants) {
			t.Error("more rows returned than expected")
			break
		}
		w := wants[i]
		var title, desc, publisher, status string
		var asin sql.NullString
		var convBool int64
		if err := rows.Scan(&title, &asin, &desc, &publisher, &status, &convBool); err != nil {
			t.Fatal(err)
		}
		converted := convBool == 1

		if title != w.title {
			t.Errorf("book %d title = %q, want %q", i+1, title, w.title)
		}
		if asin.Valid != w.asin.Valid || (asin.Valid && asin.String != w.asin.String) {
			t.Errorf("book %d asin = %+v, want %+v", i+1, asin, w.asin)
		}
		if desc != w.desc {
			t.Errorf("book %d description = %q, want %q", i+1, desc, w.desc)
		}
		if publisher != w.publisher {
			t.Errorf("book %d publisher = %q, want %q", i+1, publisher, w.publisher)
		}
		if status != w.status {
			t.Errorf("book %d status = %q, want %q", i+1, status, w.status)
		}
		if converted != w.converted {
			t.Errorf("book %d converted = %v, want %v (CRITICAL: converted field not preserved)", i+1, converted, w.converted)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != 3 {
		t.Errorf("got %d book rows, want 3", i)
	}
}

// --- TestMigratePeople ---

func TestMigratePeople(t *testing.T) {
	legacy := newLegacyDB(t)
	t.Cleanup(func() { legacy.Close() })

	newDB := newEmptyDB(t)
	t.Cleanup(func() { newDB.Close() })
	if err := setupNewSchema(newDB); err != nil {
		t.Fatal(err)
	}

	// Insert 3 books into legacy
	_, err := legacy.Exec(`INSERT INTO importer_book (id, title, asin)
		VALUES (1, 'Book One', 'B001'), (2, 'Book Two', 'B002'), (3, 'Book Three', '')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 2 authors
	_, err = legacy.Exec(`INSERT INTO importer_author (id, first_name, last_name)
		VALUES (1, 'Jane', 'Doe'), (2, 'John', 'Smith')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert 2 narrators
	_, err = legacy.Exec(`INSERT INTO importer_narrator (id, first_name, last_name)
		VALUES (1, 'Alice', 'Wonder'), (2, 'Bob', 'Builder')`)
	if err != nil {
		t.Fatal(err)
	}

	// Author-book M2M: Jane Doe -> Book One, John Smith -> Book Two
	_, err = legacy.Exec(`INSERT INTO importer_author_books (id, book_id, author_id)
		VALUES (1, 1, 1), (2, 2, 2)`)
	if err != nil {
		t.Fatal(err)
	}

	// Narrator-book M2M: Alice Wonder -> Book One, Bob Builder -> Book Three
	_, err = legacy.Exec(`INSERT INTO importer_narrator_books (id, book_id, narrator_id)
		VALUES (1, 1, 1), (2, 3, 2)`)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-insert books into new DB (migratePeople expects them to exist)
	_, err = newDB.Exec(`INSERT INTO books (id, title, asin)
		VALUES (1, 'Book One', 'B001'), (2, 'Book Two', 'B002'), (3, 'Book Three', '')`)
	if err != nil {
		t.Fatal(err)
	}

	count, err := migratePeople(legacy, newDB)
	if err != nil {
		t.Fatalf("migratePeople error: %v", err)
	}

	// 2 authors + 2 narrators = 4
	if count != 4 {
		t.Errorf("migratePeople count = %d, want 4", count)
	}

	type personWant struct {
		bookID int64
		name   string
		role   string
	}
	wants := []personWant{
		{bookID: 1, name: "Jane Doe", role: "author"},
		{bookID: 2, name: "John Smith", role: "author"},
		{bookID: 1, name: "Alice Wonder", role: "narrator"},
		{bookID: 3, name: "Bob Builder", role: "narrator"},
	}

	rows, err := newDB.Query("SELECT book_id, name, role FROM people ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		if i >= len(wants) {
			t.Error("more person rows returned than expected")
			break
		}
		w := wants[i]
		var bookID int64
		var name, role string
		if err := rows.Scan(&bookID, &name, &role); err != nil {
			t.Fatal(err)
		}
		if bookID != w.bookID {
			t.Errorf("person %d book_id = %d, want %d", i+1, bookID, w.bookID)
		}
		if name != w.name {
			t.Errorf("person %d name = %q, want %q", i+1, name, w.name)
		}
		if role != w.role {
			t.Errorf("person %d role = %q, want %q", i+1, role, w.role)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != 4 {
		t.Errorf("got %d person rows, want 4", i)
	}
}

// --- TestMigrateSettings ---

func TestMigrateSettings(t *testing.T) {
	legacy := newLegacyDB(t)
	t.Cleanup(func() { legacy.Close() })

	newDB := newEmptyDB(t)
	t.Cleanup(func() { newDB.Close() })
	if err := setupNewSchema(newDB); err != nil {
		t.Fatal(err)
	}

	_, err := legacy.Exec(`INSERT INTO importer_setting (id, input_directory, output_directory,
		completed_directory, num_cpus, output_scheme)
		VALUES (1, '/tmp/input', '/tmp/output', '/tmp/done', 4, '{author}/{title}')`)
	if err != nil {
		t.Fatal(err)
	}

	err = migrateSettings(legacy, newDB)
	if err != nil {
		t.Fatalf("migrateSettings error: %v", err)
	}

	var id int
	var inputDir, outputDir, completedDir, outputScheme string
	var numCPUs int

	row := newDB.QueryRow("SELECT id, input_dir, output_dir, completed_dir, num_cpus, output_scheme FROM settings WHERE id = 1")
	if err := row.Scan(&id, &inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme); err != nil {
		t.Fatal(err)
	}

	if id != 1 {
		t.Errorf("settings id = %d, want 1", id)
	}
	if inputDir != "/tmp/input" {
		t.Errorf("input_dir = %q, want %q", inputDir, "/tmp/input")
	}
	if outputDir != "/tmp/output" {
		t.Errorf("output_dir = %q, want %q", outputDir, "/tmp/output")
	}
	if completedDir != "/tmp/done" {
		t.Errorf("completed_dir = %q, want %q", completedDir, "/tmp/done")
	}
	if numCPUs != 4 {
		t.Errorf("num_cpus = %d, want 4", numCPUs)
	}
	if outputScheme != "{author}/{title}" {
		t.Errorf("output_scheme = %q, want %q", outputScheme, "{author}/{title}")
	}
}

// --- TestMigrateSettingsNoRows ---

func TestMigrateSettingsNoRows(t *testing.T) {
	legacy := newLegacyDB(t)
	t.Cleanup(func() { legacy.Close() })

	newDB := newEmptyDB(t)
	t.Cleanup(func() { newDB.Close() })
	if err := setupNewSchema(newDB); err != nil {
		t.Fatal(err)
	}

	// No importer_setting row inserted
	err := migrateSettings(legacy, newDB)
	if err != nil {
		t.Fatalf("migrateSettings with no rows returned error: %v", err)
	}
}

// --- TestCheckLegacyTables ---

func TestCheckLegacyTables(t *testing.T) {
	t.Run("all tables present", func(t *testing.T) {
		db := newLegacyDB(t)
		t.Cleanup(func() { db.Close() })

		err := checkLegacyTables(db)
		if err != nil {
			t.Errorf("checkLegacyTables with all tables: unexpected error: %v", err)
		}
	})

	t.Run("no tables", func(t *testing.T) {
		db := newEmptyDB(t)
		t.Cleanup(func() { db.Close() })
		// Create empty schema without legacy table names — just create an unrelated table
		_, err := db.Exec("CREATE TABLE IF NOT EXISTS some_other_table (id INTEGER PRIMARY KEY)")
		if err != nil {
			t.Fatal(err)
		}

		err = checkLegacyTables(db)
		if err == nil {
			t.Error("checkLegacyTables with no legacy tables: expected error, got nil")
		}
	})

	t.Run("only importer_book", func(t *testing.T) {
		db := newEmptyDB(t)
		t.Cleanup(func() { db.Close() })
		_, err := db.Exec("CREATE TABLE IF NOT EXISTS importer_book (id INTEGER PRIMARY KEY)")
		if err != nil {
			t.Fatal(err)
		}

		err = checkLegacyTables(db)
		if err != nil {
			t.Errorf("checkLegacyTables with importer_book only: unexpected error: %v", err)
		}
	})
}
