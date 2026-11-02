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
// to be reflected here. It runs before the tests below that swap the
// package's `migrations` var (in-file order), so it must not be moved after
// them.
func TestRunMigrationsRealShape(t *testing.T) {
	if len(migrations) < 11 {
		t.Fatalf("migrations = %d entries, want the 11 production migrations (run before the test var mutations)", len(migrations))
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
	if hasColumn(t, db, "books", "output_dir_override") {
		t.Error("books.output_dir_override must be dropped after v10")
	}
	if hasColumn(t, db, "books", "path_format_override") {
		t.Error("books.path_format_override must be dropped after v10")
	}
	if !hasColumn(t, db, "output_profiles", "name") {
		t.Fatal("output_profiles.name missing (v10)")
	}
	for _, col := range []string{"id", "output_dir", "output_scheme", "created_at", "updated_at"} {
		if !hasColumn(t, db, "output_profiles", col) {
			t.Errorf("output_profiles.%s missing (v10)", col)
		}
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
	if !hasColumn(t, db, "processing_jobs", "detail") {
		t.Error("processing_jobs.detail missing (v8 regression)")
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
// It runs before the tests that swap package `migrations` in this file,
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
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d (fresh DB took the full list)", n, len(migrations))
	}
}

// TestRunMigrationsV6ToV7PinsTheOnUpgradePath pins the cutover from a
// pre-action v6 DB (processing_jobs rows without an action value) to v7:
// the fresh column backfills every pre-existing row with 'merge' (every
// legacy job was an m4b-merge run) while the row's id, status, and args
// survive. It runs before the tests that swap package `migrations` in this
// file, so it must not be moved after them.
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
		t.Errorf("backfilled action = %q; want merge", action)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}
}

// TestRunMigrationsV7ToV8PinsTheOnUpgradePath pins the cutover from a
// pre-detail v7 DB (processing_jobs rows without a detail column) to v8:
// the fresh column lands NULL on every pre-existing row — merge jobs never
// had a detail, and pre-v8 audit details were stored in m4b_merge_args,
// which is legacy state the app no longer reads — while every pre-existing
// column value survives. It runs before the tests that swap package
// `migrations` in this file, so it must not be moved after them.
func TestRunMigrationsV7ToV8PinsTheOnUpgradePath(t *testing.T) {
	// v7 shape: apply the production migrations up to (excluding) v8.
	old := migrations
	migrations = migrations[:7]
	defer func() { migrations = old }()

	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations to v7: %v", err)
	}

	const (
		rowArgs = `{"src_paths":["/v7/a.m4b"]}`
		rowOut  = `/v7/out/book.m4b`
	)
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'Legacy Book', 'done')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, output, action) VALUES ('legacy-1', 1, ?, 'done', ?, 'merge')`,
		rowArgs, rowOut); err != nil {
		t.Fatal(err)
	}
	// The fixture must be the pre-cutover shape, or the test proves nothing.
	if hasColumn(t, db, "processing_jobs", "detail") {
		t.Fatal("v7 fixture already has the detail column")
	}

	// Upgrade: restore the full list; only v8 is pending.
	migrations = old
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations v7->v8: %v", err)
	}

	var id, status, args, out, action string
	var detail sql.NullString
	if err := db.QueryRow(`SELECT id, status, m4b_merge_args, output, action, detail FROM processing_jobs WHERE id = 'legacy-1'`).
		Scan(&id, &status, &args, &out, &action, &detail); err != nil {
		t.Fatal(err)
	}
	if id != "legacy-1" || status != "done" || args != rowArgs || out != rowOut || action != "merge" {
		t.Errorf("pre-existing row state lost by v8: (%q, %q, %q, %q, %q)", id, status, args, out, action)
	}
	if detail.Valid {
		t.Errorf("detail on the pre-existing row = %q; want NULL", detail.String)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}
}

// seedTestBook inserts a minimal books row and asserts it is visible; every
// column the migration under test touches must not be required here.
func seedTestBook(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO books (title, status) VALUES (?, 'matched')", "Kept Book"); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM books").Scan(&n); err != nil || n != 1 {
		t.Fatalf("seeded books count = %d (err %v), want 1", n, err)
	}
}

// TestRunMigrationsV9ToV10PinsTheProfileCutover pins the upgrade from a
// database that already ran the OLD unreleased v9 (books carried
// output_dir_override/path_format_override) to the empty v9 + v10: the book
// rows survive intact, both superseded columns are dropped, and the
// output_profiles table appears with its full column set.
func TestRunMigrationsV9ToV10PinsTheProfileCutover(t *testing.T) {
	old := migrations
	migrations = migrations[:9]
	defer func() { migrations = old }()

	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations to v9: %v", err)
	}
	// Emulate a database that ran the old v9 SQL before it was emptied.
	for _, col := range []string{"output_dir_override", "path_format_override"} {
		if _, err := db.Exec("ALTER TABLE books ADD COLUMN " + col + " TEXT DEFAULT ''"); err != nil {
			t.Fatalf("emulate old v9 column %s: %v", col, err)
		}
	}
	seedTestBook(t, db)

	// Upgrade: restore the full list; only v10 is pending.
	migrations = old
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations v9->v10: %v", err)
	}
	if hasColumn(t, db, "books", "output_dir_override") {
		t.Error("books.output_dir_override still present after v10")
	}
	if hasColumn(t, db, "books", "path_format_override") {
		t.Error("books.path_format_override still present after v10")
	}
	for _, col := range []string{"id", "name", "output_dir", "output_scheme", "created_at", "updated_at"} {
		if !hasColumn(t, db, "output_profiles", col) {
			t.Errorf("output_profiles.%s missing after v10", col)
		}
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM books WHERE title = ?", "Kept Book").Scan(&n); err != nil || n != 1 {
		t.Fatalf("book row after v10: count = %d (err %v), want 1 (rows survive the drops)", n, err)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations = %d, want all %d", n, len(migrations))
	}

	// Re-applying v10 (lost log row) is a no-op: the CREATE is
	// IF NOT EXISTS, both drops see absent columns and are skipped.
	if _, err := db.Exec("DELETE FROM migration_log WHERE name = 'v10'"); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations re-apply v10: %v", err)
	}
	if hasColumn(t, db, "books", "output_dir_override") || hasColumn(t, db, "books", "path_format_override") {
		t.Error("book override columns resurrected by v10 re-apply")
	}
	var nameCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM output_profiles").Scan(&nameCount); err != nil || nameCount != 0 {
		t.Fatalf("output_profiles rows = %d (err %v), want 0 (no reseed)", nameCount, err)
	}
	if n := appliedCount(t, db); n != len(migrations) {
		t.Errorf("applied migrations after v10 heal = %d, want %d", n, len(migrations))
	}
}

// TestRunMigrationsV9HealCannotResurrectColumns pins the empty-v9 marker:
// on a database where v10 has already run, re-applying v9 after a lost v9
// log row must NOT re-add the superseded book columns that the old v9 SQL
// would have created.
func TestRunMigrationsV9HealCannotResurrectColumns(t *testing.T) {
	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	seedTestBook(t, db)

	// Lose the v9 log row, then heal.
	if _, err := db.Exec("DELETE FROM migration_log WHERE name = 'v9'"); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations heal v9: %v", err)
	}
	if hasColumn(t, db, "books", "output_dir_override") {
		t.Error("v9 heal re-added books.output_dir_override (old v9 would have)")
	}
	if hasColumn(t, db, "books", "path_format_override") {
		t.Error("v9 heal re-added books.path_format_override (old v9 would have)")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM books WHERE title = ?", "Kept Book").Scan(&n); err != nil || n != 1 {
		t.Fatalf("book row after v9 heal: count = %d (err %v), want 1", n, err)
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
