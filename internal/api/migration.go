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

// migrateStatusMap maps legacy Django status strings to new status values.
var migrateStatusMap = map[string]string{
	"Done":       "done",
	"Processing": "processing",
	"Error":      "error",
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

	// Open legacy database with proper DSN format for modernc.org/sqlite
	legacyDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s", req.Path))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open legacy DB: %v", err))
		return
	}
	defer legacyDB.Close()

	// Verify the legacy DB has the expected tables
	if err := checkLegacyTables(legacyDB); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not a valid legacy Bragi Books database: %v", err))
		return
	}

	// Create backup
	backupPath := req.Path + ".bak"
	if err := os.Rename(req.Path, backupPath); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create backup: %v", err))
		return
	}

	// Migrate books
	booksMigrated, err := migrateBooks(legacyDB, h.svc.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migrate books: %v", err))
		return
	}

	// Migrate people (authors + narrators)
	peopleMigrated, err := migratePeople(legacyDB, h.svc.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migrate people: %v", err))
		return
	}

	// Migrate settings
	if err := migrateSettings(legacyDB, h.svc.DB); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("migrate settings: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, MigrateLegacyDBResult{
		Migrated: struct {
			Books  int `json:"books"`
			People int `json:"people"`
		}{
			Books:  booksMigrated,
			People: peopleMigrated,
		},
		Backup: backupPath,
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
func migrateBooks(legacyDB, newDB *sql.DB) (int, error) {
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
		SELECT id, title, asin, short_desc, long_desc, release_date, series, publisher, lang, runtime_length_minutes, format_type, src_path, dest_path, status_id, cover_image_link, created_at, updated_at
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

		err := bookRows.Scan(&legacyID, &title, &asinVal, &shortDesc, &longDesc, &releaseDate,
			&series, &publisher, &lang, &runtimeLen, &formatType, &srcPath, &destPath,
			&statusID, &coverImageLink, &createdAt, &updatedAt)
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

		// Insert into new books table
		_, err = newDB.Exec(`
			INSERT INTO books (title, asin, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, cover_image_url, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, title, asin, desc, releaseDate, series, publisher, lang, runtimeLen, formatType,
			srcPath, destPath, status, coverImageLink, createdAt, updatedAt)
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

// migratePeople reads legacy author/narrator tables and inserts them into the new people table.
func migratePeople(legacyDB, newDB *sql.DB) (int, error) {
	count := 0

	// Migrate authors
	authorCount, err := migratePeopleType(legacyDB, newDB, "author")
	if err != nil {
		return 0, err
	}
	log.Printf("Migrated %d authors", authorCount)
	count += authorCount

	// Migrate narrators
	narratorCount, err := migratePeopleType(legacyDB, newDB, "narrator")
	if err != nil {
		return 0, err
	}
	log.Printf("Migrated %d narrators", narratorCount)
	count += narratorCount

	return count, nil
}

// migratePeopleType migrates a specific type of person (author or narrator) from the legacy DB.
// Returns the number of people migrated, or a negative count on error.
func migratePeopleType(legacyDB, newDB *sql.DB, role string) (int, error) {
	count := 0

	personTable := "importer_author"
	m2mTable := "importer_author_books"
	if role == "narrator" {
		personTable = "importer_narrator"
		m2mTable = "importer_narrator_books"
	}

	// Read people from legacy table
	rows, err := legacyDB.Query(fmt.Sprintf(
		"SELECT %s.id, %s.first_name, %s.last_name, %s.book_id FROM %s INNER JOIN %s ON %s.id = %s.%s_id",
		personTable, personTable, personTable, m2mTable,
		personTable, m2mTable,
		personTable, m2mTable, role,
	))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// We need the new book IDs to insert people. First, collect all (legacyBookID -> newBookID) mappings.
	// We'll read the legacy book ID from the M2M table and map to the new books table.
	// Since book IDs may have changed, we need to match on ASIN or title.
	bookIDCache := make(map[int]int64)

	for rows.Next() {
		var legacyPersonID, legacyBookID int
		var firstName, lastName string
		err := rows.Scan(&legacyPersonID, &firstName, &lastName, &legacyBookID)
		if err != nil {
			return count, err
		}

		name := strings.TrimSpace(firstName + " " + lastName)
		if name == "" {
			continue
		}

		// Find the new book ID by matching legacy book
		var newBookID int64
		if cached, ok := bookIDCache[legacyBookID]; ok {
			newBookID = cached
		} else {
			// Try to match by looking up the legacy book's ASIN or title
			var asin, title string
			err := legacyDB.QueryRow("SELECT asin, title FROM importer_book WHERE id = ?", legacyBookID).Scan(&asin, &title)
			if err != nil {
				continue // skip this person if we can't find the legacy book
			}

			// Try to match by ASIN first
			var foundBookID sql.NullInt64
			if asin != "" {
				err = newDB.QueryRow("SELECT id FROM books WHERE asin = ? LIMIT 1", asin).Scan(&foundBookID)
			}
			// Fallback to title match
			if err != nil || !foundBookID.Valid {
				err = newDB.QueryRow("SELECT id FROM books WHERE title = ? LIMIT 1", title).Scan(&foundBookID)
			}
			if err == nil && foundBookID.Valid {
				newBookID = foundBookID.Int64
				bookIDCache[legacyBookID] = newBookID
			} else {
				// Couldn't find matching book, skip
				continue
			}
		}

		// Insert into new people table
		_, err = newDB.Exec(`
			INSERT INTO people (book_id, name, role) VALUES (?, ?, ?)
		`, newBookID, name, role)
		if err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// migrateSettings reads the legacy importer_setting row and inserts it into the new settings table.
func migrateSettings(legacyDB, newDB *sql.DB) error {
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