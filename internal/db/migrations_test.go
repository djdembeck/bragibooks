package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// testMigrations is a forced-failure suite used only by this test: v1
// creates a table, and v2 halves its columns (the second statement on the
// same table makes a prior fixpoint impossible; v3 is a bare ADD COLUMN to
// exercise the column guard).
var testMigrations = []migration{
	{version: 1,
		sql: `CREATE TABLE itest (
			id INTEGER PRIMARY KEY,
			a TEXT,
			b TEXT,
			c TEXT
		);
		CREATE INDEX idx_itest_a ON itest(a);`,
	},
	{version: 2,
		sql: `ALTER TABLE itest ADD COLUMN d TEXT DEFAULT '';`,
	},
	{version: 3,
		sql: `ALTER TABLE itest ADD COLUMN e TEXT DEFAULT '';`,
	},
}

// captureMigrations temporarily swaps the package's migration list so a
// failing migration can be injected. Returns a restore function.
func captureMigrations(t *testing.T) func() {
	t.Helper()
	old := migrations
	migrations = testMigrations
	return func() { migrations = old }
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "itest.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestRunMigrationsIdempotent applies the test migrations twice (simulating
// a second startup) and checks the schema counts don't change and no
// errors occur, i.e. a re-invocation is a no-op (row: idempotency).
func TestRunMigrationsIdempotent(t *testing.T) {
	defer captureMigrations(t)()
	db := openTestDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	cols := columnCount(t, db, "itest")
	if cols != 6 { // id, a, b, c (v1) + d (v2) + e (v3)
		t.Fatalf("columns after first run = %d, want 6", cols)
	}

	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations must be a no-op, got: %v", err)
	}
	if again := columnCount(t, db, "itest"); again != cols {
		t.Errorf("columns after second run = %d, want %d", again, cols)
	}
	if n := appliedCount(t, db); n != 3 {
		t.Errorf("applied migrations = %d, want 3", n)
	}
}

// TestRunMigrationsAtomicRollback forces a failure in the middle of a
// migration (v2's first statement succeeds, then a bad statement follows)
// and checks that NOTHING from the failed migration is visible: the log
// row is absent and the column was rolled back.
func TestRunMigrationsAtomicRollback(t *testing.T) {
	restore := captureMigrations(t)
	defer restore()
	// Start from a pristine copy AFTER capturing: the shared testMigrations
	// var must not accumulate injections across tests.
	migrations = append([]migration(nil), testMigrations...)
	// Inject a genuinely non-idempotent failing statement as v2's second
	// statement: a bare duplicate index name (the CREATE statement's name
	// matches one v1 made, so sqlite rejects it).
	migrations[1].sql += `
	CREATE INDEX idx_itest_a ON itest(b);`
	db := openTestDB(t)

	if err := RunMigrations(db); err == nil {
		t.Fatal("RunMigrations succeeded, want a forced failure")
	}

	// Rollback invariants:
	// 1. No log row for the failed migration v2.
	if n := appliedCount(t, db); n != 1 {
		t.Errorf("applied after failure = %d, want only v1", n)
	}
	// 2. The schema change made by the first statement of v2 is gone.
	if hasColumn(t, db, "itest", "d") {
		t.Error("column d should have been rolled back")
	}
	// 3. The database itself is still usable.
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Errorf("db unusable after rollback: %v", err)
	}
}

// TestRunMigrationsHealsMissingLogRow reproduces the observed production
// bug: the log row for v2 was lost (e.g. process killed mid-migration)
// while column d exists. Re-run must succeed as a no-op for v2.
func TestRunMigrationsHealsMissingLogRow(t *testing.T) {
	// Fresh copy: sibling tests may have appended failing statements to
	// the shared testMigrations vars.
	migrations = append([]migration(nil), testMigrations...)
	db := openTestDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	// Simulate the lost log row for v2.
	if _, err := db.Exec("DELETE FROM migration_log WHERE name = 'v2'"); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("re-run with missing log row must heal, got: %v", err)
	}
	if !hasColumn(t, db, "itest", "d") {
		t.Fatal("column d missing after heal")
	}
	if n := appliedCount(t, db); n != 3 {
		t.Errorf("applied after heal = %d, want 3", n)
	}
}

func appliedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM migration_log").Scan(&n); err != nil {
		t.Fatalf("count migration_log: %v", err)
	}
	return n
}

func hasColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, col,
	).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n > 0
}

func columnCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?)", table).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n
}
