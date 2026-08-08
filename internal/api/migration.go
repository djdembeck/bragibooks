package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// MigrateLegacyDBRequest is the JSON body for POST /api/migrate.
type MigrateLegacyDBRequest struct {
	Path string `json:"path"`
}

// MigrateLegacyDBResult is the JSON response from a successful migration.
type MigrateLegacyDBResult struct {
	Migrated struct {
		Books  int `json:"books"`
		People int `json:"people"`
	} `json:"migrated"`
	Backup string `json:"backup"`
}

// MigratePeopleRequest is the JSON body for POST /api/migrate/people.
type MigratePeopleRequest struct {
	Path string `json:"path"`
}

// MigratePeopleResult is the JSON response from POST /api/migrate/people.
type MigratePeopleResult struct {
	Migrated struct {
		Authors   int `json:"authors"`
		Narrators int `json:"narrators"`
	} `json:"migrated"`
}

// migrateStatusMap maps legacy Django status strings to new status values.
var migrateStatusMap = map[string]string{
	"Done":       "done",
	"Processing": "processing",
	"Error":      "error",
}

// LegacyMigrationResult holds the result of a legacy DB migration.
type LegacyMigrationResult struct {
	Books  int
	People int
}

// sqlExecer is implemented by both *sql.DB and *sql.Tx, allowing migration
// helpers to work within a transaction or against a bare connection.
type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// RunLegacyMigration migrates data from a legacy Django DB into the new schema.
// It does NOT modify or rename the legacy DB file — reads it in place.
// All writes are wrapped in a single transaction for performance and atomicity.
func RunLegacyMigration(legacyDBPath string, newDB *sql.DB) (*LegacyMigrationResult, error) {
	expandedPath := expandTildePath(legacyDBPath)

	legacyDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", expandedPath))
	if err != nil {
		return nil, fmt.Errorf("open legacy DB: %w", err)
	}
	defer legacyDB.Close()

	if err := checkLegacyTables(legacyDB); err != nil {
		return nil, fmt.Errorf("not a valid legacy Bragi Books database: %w", err)
	}

	tx, err := newDB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback() // safe to call after Commit

	booksMigrated, err := migrateBooks(legacyDB, tx)
	if err != nil {
		return nil, fmt.Errorf("migrate books: %w", err)
	}

	peopleMigrated, err := migratePeople(legacyDB, tx)
	if err != nil {
		return nil, fmt.Errorf("migrate people: %w", err)
	}

	if err := migrateSettings(legacyDB, tx); err != nil {
		return nil, fmt.Errorf("migrate settings: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit migration transaction: %w", err)
	}

	return &LegacyMigrationResult{
		Books:  booksMigrated,
		People: peopleMigrated,
	}, nil
}

// MigrateLegacyDB handles POST /api/migrate.
// Reads the legacy Django db.sqlite3 and migrates data to the new schema.
func (h *Handler) MigrateLegacyDB(w http.ResponseWriter, r *http.Request) {
	var req MigrateLegacyDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Expand ~ in path
	req.Path = expandTildePath(req.Path)

	// Create a backup COPY (best-effort — warn if it fails)
	backupPath := req.Path + ".bak"
	if bData, readErr := os.ReadFile(req.Path); readErr == nil {
		if writeErr := os.WriteFile(backupPath, bData, 0644); writeErr != nil {
			log.Printf("Warning: failed to create backup of legacy DB: %v", writeErr)
		}
	} else {
		log.Printf("Warning: failed to read legacy DB for backup: %v", readErr)
	}

	result, err := RunLegacyMigration(req.Path, h.svc.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, MigrateLegacyDBResult{
		Migrated: struct {
			Books  int `json:"books"`
			People int `json:"people"`
		}{
			Books:  result.Books,
			People: result.People,
		},
		Backup: backupPath,
	})
}

// RecoverPeople handles POST /api/migrate/people.
// Recovery endpoint: migrates people (authors + narrators) from a legacy DB
// into an already-populated new DB (books already exist).
func (h *Handler) RecoverPeople(w http.ResponseWriter, r *http.Request) {
	var req MigratePeopleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	req.Path = expandTildePath(req.Path)

	legacyDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s", req.Path))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open legacy DB: %v", err))
		return
	}
	defer legacyDB.Close()

	if err := checkLegacyTables(legacyDB); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not a valid legacy Bragi Books database: %v", err))
		return
	}

	mapping, err := buildBookMapping(h.svc.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("build book mapping: %v", err))
		return
	}

	authorCount, err := migratePeopleType(legacyDB, h.svc.DB, "author", mapping)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migrate authors: %v", err))
		return
	}

	narratorCount, err := migratePeopleType(legacyDB, h.svc.DB, "narrator", mapping)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migrate narrators: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, MigratePeopleResult{
		Migrated: struct {
			Authors   int `json:"authors"`
			Narrators int `json:"narrators"`
		}{
			Authors:   authorCount,
			Narrators: narratorCount,
		},
	})
}

// checkLegacyTables verifies the legacy DB has the expected Django tables.
func checkLegacyTables(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT name FROM sqlite_master WHERE type='table' AND name IN ('importer_book', 'importer_author', 'importer_narrator')
	`)
	if err != nil {
		return fmt.Errorf("query legacy tables: %w", err)
	}
	defer rows.Close()

	found := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		found++
	}
	if found < 1 {
		return fmt.Errorf("legacy tables not found (expected importer_book, importer_author, importer_narrator)")
	}
	return nil
}

// migrateBooks reads importer_book rows and inserts them into the new books table.
func migrateBooks(legacyDB *sql.DB, newDB sqlExecer) (int, error) {
	// Get status mapping from importer_status table
	statusMap := make(map[int]string)
	statusRows, _ := legacyDB.Query("SELECT id, status FROM importer_status")
	if statusRows != nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var id int
			var status string
			if err := statusRows.Scan(&id, &status); err == nil {
				statusMap[id] = status
			}
		}
	}

	// Read all books from legacy DB
	bookRows, err := legacyDB.Query(`
		SELECT id, title, asin, short_desc, long_desc, release_date, series, publisher, lang, runtime_length_minutes, format_type, src_path, dest_path, status_id, cover_image_link, created_at, updated_at, converted
		FROM importer_book
	`)
	if err != nil {
		return 0, err
	}
	defer bookRows.Close()

	count := 0
	for bookRows.Next() {
		var legacyID int
		var title, asinVal, shortDesc, longDesc, releaseDate, series, publisher, lang, formatType, srcPath, destPath, coverImageLink, createdAt, updatedAt string
		var statusID int
		var runtimeLen int
		var convertedVal int64

		err := bookRows.Scan(&legacyID, &title, &asinVal, &shortDesc, &longDesc, &releaseDate,
			&series, &publisher, &lang, &runtimeLen, &formatType, &srcPath, &destPath,
			&statusID, &coverImageLink, &createdAt, &updatedAt, &convertedVal)
		if err != nil {
			return count, err
		}

		// Build description from short + long desc
		desc := strings.TrimSpace(shortDesc + " " + longDesc)

		// Map legacy status to new status
		status := migrateStatusMap[statusMap[statusID]]
		if status == "" {
			status = "pending"
		}

		// Handle ASIN
		var asin sql.NullString
		if asinVal != "" {
			asin = sql.NullString{String: asinVal, Valid: true}
		}

		_, err = newDB.Exec(`
			INSERT INTO books (title, asin, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, cover_image_url, created_at, updated_at, converted)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, title, asin, desc, releaseDate, series, publisher, lang, runtimeLen, formatType,
			srcPath, destPath, status, coverImageLink, createdAt, updatedAt, convertedVal != 0)
		if err != nil {
			return count, err
		}
		count++
	}
	if err := bookRows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// bookMapping holds pre-built lookups from legacy ASIN/title to new book ID.
type bookMapping struct {
	byASIN  map[string]int64
	byTitle map[string]int64
}

// buildBookMapping queries the new DB for books and returns maps to look up
// a new book ID by ASIN or title.
func buildBookMapping(newDB sqlExecer) (*bookMapping, error) {
	rows, err := newDB.Query("SELECT id, asin, title FROM books")
	if err != nil {
		return nil, fmt.Errorf("query new books for mapping: %w", err)
	}
	defer rows.Close()

	m := &bookMapping{
		byASIN:  make(map[string]int64),
		byTitle: make(map[string]int64),
	}

	for rows.Next() {
		var id int64
		var asin, title string
		if err := rows.Scan(&id, &asin, &title); err != nil {
			return nil, err
		}
		if asin != "" {
			m.byASIN[asin] = id
		}
		m.byTitle[title] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// migratePeople reads legacy author/narrator tables and inserts them into the new people table.
func migratePeople(legacyDB *sql.DB, newDB sqlExecer) (int, error) {
	// Build the book mapping once, shared by both author and narrator passes
	mapping, err := buildBookMapping(newDB)
	if err != nil {
		return 0, fmt.Errorf("build book mapping: %w", err)
	}

	count := 0

	authorCount, err := migratePeopleType(legacyDB, newDB, "author", mapping)
	if err != nil {
		return 0, err
	}
	log.Printf("Migrated %d authors", authorCount)
	count += authorCount

	narratorCount, err := migratePeopleType(legacyDB, newDB, "narrator", mapping)
	if err != nil {
		return 0, err
	}
	log.Printf("Migrated %d narrators", narratorCount)
	count += narratorCount

	return count, nil
}

// migratePeopleType migrates a specific type of person (author or narrator)
// from the legacy DB using pre-built book mappings.
func migratePeopleType(legacyDB *sql.DB, newDB sqlExecer, role string, mapping *bookMapping) (int, error) {
	personTable := "importer_author"
	m2mTable := "importer_author_books"
	m2mFK := "author"
	if role == "narrator" {
		personTable = "importer_narrator"
		m2mTable = "importer_narrator_books"
		m2mFK = "narrator"
	}

	// 1. Pre-load legacy book IDs -> {asin, title}
	legacyBooks, err := loadLegacyBooks(legacyDB)
	if err != nil {
		return 0, fmt.Errorf("load legacy books: %w", err)
	}

	// 2. Read all person-book M2M rows into memory
	rows, err := legacyDB.Query(fmt.Sprintf(
		"SELECT %s.id, %s.first_name, %s.last_name, %s.book_id FROM %s INNER JOIN %s ON %s.id = %s.%s_id",
		personTable, personTable, personTable, m2mTable,
		personTable, m2mTable,
		personTable, m2mTable, m2mFK,
	))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// Collect all rows in memory first
	type personRow struct {
		id        int
		firstName string
		lastName  string
		bookID    int
	}
	var allRows []personRow

	for rows.Next() {
		var r personRow
		if err := rows.Scan(&r.id, &r.firstName, &r.lastName, &r.bookID); err != nil {
			return 0, err
		}
		allRows = append(allRows, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// 3. Match and insert
	count := 0
	for _, r := range allRows {
		name := strings.TrimSpace(r.firstName + " " + r.lastName)
		if name == "" {
			continue
		}

		// Look up legacy book
		lb, ok := legacyBooks[r.bookID]
		if !ok {
			continue
		}

		// Match to new book ID via ASIN first, then title
		var newBookID int64
		if lb.asin != "" {
			if id, ok := mapping.byASIN[lb.asin]; ok {
				newBookID = id
			}
		}
		if newBookID == 0 {
			if id, ok := mapping.byTitle[lb.title]; ok {
				newBookID = id
			}
		}
		if newBookID == 0 {
			continue
		}

		// Insert into new people table
		_, err = newDB.Exec(
			"INSERT INTO people (book_id, name, role) VALUES (?, ?, ?)",
			newBookID, name, role,
		)
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// legacyBookInfo holds ASIN and title for a legacy book.
type legacyBookInfo struct {
	asin  string
	title string
}

// loadLegacyBooks reads all books from the legacy DB into a map keyed by legacy ID.
func loadLegacyBooks(db *sql.DB) (map[int]legacyBookInfo, error) {
	rows, err := db.Query("SELECT id, asin, title FROM importer_book")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[int]legacyBookInfo)
	for rows.Next() {
		var id int
		var asin, title string
		if err := rows.Scan(&id, &asin, &title); err != nil {
			return nil, err
		}
		m[id] = legacyBookInfo{asin: asin, title: title}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// migrateSettings reads the legacy importer_setting row and inserts it into the new settings table.
func migrateSettings(legacyDB *sql.DB, newDB sqlExecer) error {
	// Read the legacy setting row (there should be exactly one)
	row := legacyDB.QueryRow(`
		SELECT input_directory, output_directory, completed_directory, num_cpus, output_scheme
		FROM importer_setting LIMIT 1
	`)

	var inputDir, outputDir, completedDir, outputScheme string
	var numCPUs int
	err := row.Scan(&inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		return nil // no settings to migrate
	}

	_, err = newDB.Exec(`
		INSERT OR REPLACE INTO settings (id, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, updated_at)
		VALUES (1, 'm4b-merge', ?, ?, ?, ?, ?, 'us', datetime('now'))
	`, inputDir, outputDir, completedDir, numCPUs, outputScheme)
	return err
}

// expandTildePath expands ~ in a path to the user's home directory.
func expandTildePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}