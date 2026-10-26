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

// TestRunMigrationsRealShape runs the production migration list on a fresh
// DB and asserts the resulting table shapes, so a schema change always has
// to be reflected here. It runs BEFORE the two tests below that permanently
// mutate the package's `migrations` var (in-file order), so it must not be
// moved after them.
func TestRunMigrationsRealShape(t *testing.T) {
	if len(migrations) < 6 {
		t.Fatalf("migrations = %d entries, want the 6 production migrations (run before the test var mutations)", len(migrations))
	}
	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}
	if !hasColumn(t, db, "settings", "api_enabled") {
		t.Error("settings.api_enabled missing after v5")
	}
	if !hasColumn(t, db, "settings", "api_key_digest") {
		t.Error("settings.api_key_digest missing after v5")
	}
	if !hasColumn(t, db, "settings", "legacy_token_migrated") {
		t.Error("settings.legacy_token_migrated missing after v6")
	}
	if !hasColumn(t, db, "settings", "audiobookdb_base_url") {
		t.Error("settings.audiobookdb_base_url missing (v3 regression)")
	}
	if !hasColumn(t, db, "books", "converted") {
		t.Error("books.converted missing (v2 regression)")
	}
	if !hasColumn(t, db, "processing_jobs", "action") {
		t.Error("processing_jobs.action missing (v7 regression)")
	}
	if !hasColumn(t, db, "people", "asin") {
		t.Error("people.asin missing (v4 regression)")
	}
	// Re-applying is a no-op (columns are idempotently skipped).
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations re-apply: %v", err)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied after re-apply = %d, want no new rows (%d)", n, len(migrations))
	}
}

// TestRunMigrationsV5ToV6PinsTheOnUpgradePath pins the cutover from a
// pre-external-API-key v5 DB (no legacy_token_migrated column) to v6: the
// fresh column appears as unconsumed (0) on the EXISTING row, every
// pre-existing column value — including an api_enabled=1 state + digest —
// survives, and the row count is unchanged. A clobbering v6 (DROP/recreate,
// or an all-defaults INSERT OR REPLACE) would lose the stored key state.
// It runs before the two tests that permanently mutate package `migrations`,
// so it must not be moved after them.
func TestRunMigrationsV5ToV6PinsTheOnUpgradePath(t *testing.T) {
	// v5 shape: apply the production migrations up to (excluding) v6.
	old := migrations
	migrations = migrations[:5]
	defer func() { migrations = old }()

	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations to v5: %v", err)
	}

	const (
		rowKey    = "row5-key"
		rowBase   = "https://row5.example"
		rowBinary = "row5-m4b"
		rowIn     = "/v5/in"
		rowOut    = "/v5/out"
		rowDone   = "/v5/done"
		rowScheme = "{v5}/{scheme}"
		rowRegion = "eu"
		rowDigest = "9999999999999999999999999999999999999999999999999999999999999999"
	)
	if _, err := db.Exec(`INSERT INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, api_enabled, api_key_digest)
		VALUES (1, ?, ?, ?, ?, ?, ?, 3, ?, ?, 1, ?)`,
		rowKey, rowBase, rowBinary, rowIn, rowOut, rowDone, rowScheme, rowRegion, rowDigest); err != nil {
		t.Fatal(err)
	}
	// The fixture must be the pre-cutover shape, or the test proves nothing.
	if hasColumn(t, db, "settings", "legacy_token_migrated") {
		t.Fatal("v5 fixture already has the marker column")
	}

	// Upgrade: restore the full list; only v6 is pending.
	migrations = old
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations v5->v6: %v", err)
	}

	if !hasColumn(t, db, "settings", "legacy_token_migrated") {
		t.Fatal("legacy_token_migrated missing after v6")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM settings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("row count after v6 = %d, want 1 (no re-seed, no drop)", n)
	}
	var apiKey, baseURL, binary, inDir, outDir, doneDir, scheme, region, digest string
	var numCPUs, apiEnabled, marker int
	if err := db.QueryRow(`SELECT audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, api_enabled, api_key_digest, legacy_token_migrated FROM settings WHERE id = 1`).
		Scan(&apiKey, &baseURL, &binary, &inDir, &outDir, &doneDir, &numCPUs, &scheme, &region, &apiEnabled, &digest, &marker); err != nil {
		t.Fatal(err)
	}
	if marker != 0 {
		t.Errorf("new marker on the existing row = %d, want 0 (unconsumed)", marker)
	}
	if apiKey != rowKey || baseURL != rowBase || binary != rowBinary || inDir != rowIn ||
		outDir != rowOut || doneDir != rowDone || numCPUs != 3 || scheme != rowScheme ||
		region != rowRegion || apiEnabled != 1 || digest != rowDigest {
		t.Errorf("pre-existing row state lost by v6: (%q, %q, %q, %q, %q, %q, %d, %q, %q, %d, %q)",
			apiKey, baseURL, binary, inDir, outDir, doneDir, numCPUs, scheme, region, apiEnabled, digest)
	}
	// (v7 also applies on the upgrade to the full list — that is the V6ToV7 test.)
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}
}

// TestRunMigrationsV6ToV7PinsTheOnUpgradePath pins the cutover from a
// pre-action v6 DB (processing_jobs rows without an action value) to v7:
// the fresh column backfills every pre-existing row with 'merge' (every
// legacy job was an m4b-merge run) while the row's id, status, and args
// survive. It runs before the two tests that permanently mutate package
// `migrations`, so it must not be moved after them.
func TestRunMigrationsV6ToV7PinsTheOnUpgradePath(t *testing.T) {
	// v6 shape: apply the production migrations up to (excluding) v7.
	old := migrations
	migrations = migrations[:6]
	defer func() { migrations = old }()

	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations to v6: %v", err)
	}

	const (
		rowArgs = `{"src_paths":["/v6/a.m4b"]}`
		rowOut  = `/v6/out/book.m4b`
	)
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'Legacy Book', 'done')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, output) VALUES ('legacy-1', 1, ?, 'done', ?)`,
		rowArgs, rowOut); err != nil {
		t.Fatal(err)
	}
	// The fixture must be the pre-cutover shape, or the test proves nothing.
	if hasColumn(t, db, "processing_jobs", "action") {
		t.Fatal("v6 fixture already has the action column")
	}

	// Upgrade: restore the full list; only v7 is pending.
	migrations = old
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations v6->v7: %v", err)
	}

	var id, status, args, out, action string
	if err := db.QueryRow(`SELECT id, status, m4b_merge_args, output, action FROM processing_jobs WHERE id = 'legacy-1'`).
		Scan(&id, &status, &args, &out, &action); err != nil {
		t.Fatal(err)
	}
	if id != "legacy-1" || status != "done" || args != rowArgs || out != rowOut {
		t.Errorf("pre-existing row state lost by v7: (%q, %q, %q, %q)", id, status, args, out)
	}
	if action != "merge" {
		t.Errorf("backfilled action = %q, want merge", action)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}
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
