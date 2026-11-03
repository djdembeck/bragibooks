package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/bragibooks/bragibooks/internal/api"
	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/db"
)

// newStartupTestEnv builds the startup half of main(): a migrated DB
// (schema v6), the default settings row, and a ConfigManager whose save
// target is a throwaway file — piped through the same environment the
// legacy-token migration runs against on a real upgrade.
func newStartupTestEnv(t *testing.T) (*sql.DB, *config.ConfigManager) {
	t.Helper()
	// Shared in-memory cache: file:memdb syntax, single logical DB.
	dbh, err := sql.Open("sqlite", "file:startuptest?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := dbh.Close(); err != nil {
			t.Errorf("close test db: %v", err)
		}
	})
	if err := db.RunMigrations(dbh); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := ensureDefaultSettings(dbh); err != nil {
		t.Fatalf("ensure default settings: %v", err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(t.TempDir() + "/config.yaml")
	return dbh, cm
}

// settingsState reads the (api_enabled, api_key_digest, legacy_token_migrated)
// triple from the row.
func settingsState(t *testing.T, dbh *sql.DB) (int, string, int) {
	t.Helper()
	var enabled int
	var digest string
	var marker int
	if err := dbh.QueryRow("SELECT api_enabled, api_key_digest, COALESCE(legacy_token_migrated, 0) FROM settings WHERE id = 1").Scan(&enabled, &digest, &marker); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	return enabled, digest, marker
}

// apiAuthRouter mounts one protected route behind the same auth middleware
// main() uses, so a test can prove which credentials the middleware
// accepts for a given (enabled, digest) live state.
func apiAuthRouter(t *testing.T, dbh *sql.DB, cm *config.ConfigManager) http.Handler {
	t.Helper()
	cfg := cm.Config()
	dbVal := new(atomic.Value)
	dbVal.Store(audiobookdb.NewClient("", cfg.APIKey.BaseURL))
	h := api.NewHandler(&api.Services{
		DB:          dbh,
		Config:      cm,
		Processor:   audio.NewProcessor(cfg.M4bMerge.Binary, cfg.Directories.OutputDir, cfg.Directories.CompletedDir, cfg.Processing.NumCPUs, cfg.Processing.PathFormat, "info"),
		AudiobookDB: dbVal,
	})
	r := chi.NewRouter()
	h.RegisterRoutes(r, api.RequireAdminWithState(cm.AdminAuthState))
	return r
}

func getWithAuth(t *testing.T, router http.Handler, remoteAddr, token string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/books", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestLegacyTokenMigration pins the one-time cutover of
// BRAGIBOOKS_ADMIN_TOKEN into the settings-based external API key
// (finding: legacy token cutover): a fresh upgrade migrates and the
// legacy token works as the API key; a second restart never runs again;
// a user disable + restart never resurrects the token; the deployment
// pin suppresses the migration entirely.
func TestLegacyTokenMigration(t *testing.T) {
	const legacy = "legacytoken"

	// (a) Fresh upgrade: no state, no marker -> the migration seeds
	// enabled + the token's digest, and the token authenticates remote
	// requests.
	t.Run("fresh-upgrade-migrates", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		dbh, cm := newStartupTestEnv(t)

		migrated, digest, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("migration returned an error on a fresh upgrade: %v", err)
		}
		if !migrated {
			t.Fatal("migration did not apply on a fresh upgrade")
		}
		want := sha256hex(legacy)
		if digest != want {
			t.Fatalf("migrated digest = %q, want sha256(%q)", digest, legacy)
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if enabled != 1 || rowDigest != want || marker != 1 {
			t.Fatalf("row after migration = (enabled=%d, digest=%q, marker=%d), want (1, sha256(token), 1)", enabled, rowDigest, marker)
		}
		// main() swaps the live state from the migration result.
		cm.SetAPIEndpointState(true, digest)

		router := apiAuthRouter(t, dbh, cm)
		// A remote (non-LAN) client with the legacy token is admitted.
		if code := getWithAuth(t, router, "203.0.113.7:5000", legacy); code != http.StatusOK {
			t.Fatalf("remote GET with legacy token = %d, want 200", code)
		}
		// ...and the same client without a key is refused (401).
		if code := getWithAuth(t, router, "203.0.113.7:5000", ""); code != http.StatusUnauthorized {
			t.Fatalf("remote GET without key = %d, want 401", code)
		}
	})

	// (b) Second restart with the token still set in the environment:
	// the marker makes it a no-op; the first migration's digest is
	// untouched.
	t.Run("second-restart-no-respawn", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		dbh, _ := newStartupTestEnv(t)
		want := sha256hex(legacy)
		if _, err := dbh.Exec("UPDATE settings SET api_enabled = 1, api_key_digest = ?, legacy_token_migrated = 1 WHERE id = 1", want); err != nil {
			t.Fatal(err)
		}

		migrated, digest, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("second restart returned an error: %v", err)
		}
		if migrated || digest != "" {
			t.Fatalf("second restart re-ran the migration (migrated=%v, digest=%q)", migrated, digest)
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if enabled != 1 || rowDigest != want || marker != 1 {
			t.Fatalf("row changed on second restart: (enabled=%d, digest=%q, marker=%d)", enabled, rowDigest, marker)
		}
	})

	// (c) User disable + restart: the disable clears the digest (state
	// looks fresh again), but the marker must keep the migration from
	// re-seeding the token.
	t.Run("disable-then-restart-no-resurrect", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		dbh, _ := newStartupTestEnv(t)
		// The row as the disable endpoint would have left it.
		if _, err := dbh.Exec("UPDATE settings SET api_enabled = 0, api_key_digest = '', legacy_token_migrated = 1 WHERE id = 1"); err != nil {
			t.Fatal(err)
		}

		migrated, _, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("restart post-disable returned an error: %v", err)
		}
		if migrated {
			t.Fatal("migration re-ran after a user disable — the token is resurrected")
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if enabled != 0 || rowDigest != "" || marker != 1 {
			t.Fatalf("row after restart post-disable = (enabled=%d, digest=%q, marker=%d), want (0, '', 1)", enabled, rowDigest, marker)
		}
	})

	// (e) Non-fresh upgrade (state already in the DB, marker unset):
	// the migration warns and ignores the token, but the observation
	// MUST consume the marker — otherwise a subsequent user disable
	// leaves the row reading "fresh" and the next boot re-seeds the
	// legacy token: resurrection after an explicit disable (P1).
	t.Run("nonfresh-warn-consumes-marker-disable-stays-inert", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		dbh, cm := newStartupTestEnv(t)
		// A populated row the user already manages: a different digest,
		// not the legacy token's.
		if _, err := dbh.Exec("UPDATE settings SET api_enabled = 1, api_key_digest = ? WHERE id = 1", sha256hex("preexistingkey")); err != nil {
			t.Fatal(err)
		}
		const sentinelTS = "2020-01-02 03:04:05"
		if _, err := dbh.Exec("UPDATE settings SET created_at = ? WHERE id = 1", sentinelTS); err != nil {
			t.Fatal(err)
		}
		createdAtBefore := sentinelTS

		// Restart 1: warn-and-ignore; the marker flip must persist.
		migrated, digest, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("non-fresh observation returned an error: %v", err)
		}
		if migrated || digest != "" {
			t.Fatalf("migration seeded on a non-fresh row (migrated=%v, digest=%q)", migrated, digest)
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if marker != 1 {
			t.Fatalf("marker not consumed on the non-fresh warn path: (enabled=%d, digest=%q, marker=%d), want marker=1", enabled, rowDigest, marker)
		}
		if enabled != 1 || rowDigest != sha256hex("preexistingkey") {
			t.Fatalf("row state clobbered by the marker flip: (enabled=%d, digest=%q), want (1, sha256(preexistingkey))", enabled, rowDigest)
		}
		var createdAt string
		if err := dbh.QueryRow("SELECT created_at FROM settings WHERE id = 1").Scan(&createdAt); err != nil {
			t.Fatal(err)
		}
		if createdAt != createdAtBefore {
			t.Errorf("marker write reset created_at: %q -> %q", createdAtBefore, createdAt)
		}

		// The user then explicitly disables the API; the disable clears
		// the digest, so the row reads exactly like a fresh state.
		if _, err := dbh.Exec("UPDATE settings SET api_enabled = 0, api_key_digest = '' WHERE id = 1"); err != nil {
			t.Fatal(err)
		}

		// Restart 2: NO re-seed; the legacy token stays inert.
		migrated, digest, err = migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("restart 2 returned an error: %v", err)
		}
		if migrated || digest != "" {
			t.Fatalf("legacy token re-seeded after an explicit disable (migrated=%v, digest=%q) — resurrection", migrated, digest)
		}
		enabled, rowDigest, marker = settingsState(t, dbh)
		if enabled != 0 || rowDigest != "" || marker != 1 {
			t.Fatalf("row after restart post-disable = (enabled=%d, digest=%q, marker=%d), want (0, '', 1)", enabled, rowDigest, marker)
		}
		// main() would have kept the live state from the migration's
		// (false, "") result: the endpoint is off, so even a client
		// presenting the legacy token is refused.
		cm.SetAPIEndpointState(false, "")
		router := apiAuthRouter(t, dbh, cm)
		if code := getWithAuth(t, router, "203.0.113.7:5000", legacy); code != http.StatusForbidden {
			t.Fatalf("remote GET with resurrected legacy token = %d, want 403 (endpoint disabled)", code)
		}
	})

	// (d) Deployment pin present: the deployment manages the flag — the
	// legacy var is ignored even on a fresh state. The observation still
	// consumes the marker (it can never seed under the pin), so a pin
	// removed later faces an already-consumed row: no re-seed.
	t.Run("pin-suppresses-migration", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		t.Setenv("SERVER_API_ENDPOINT_ENABLED", "true")
		dbh, _ := newStartupTestEnv(t)

		const sentinelTS = "2020-01-02 03:04:05"
		if _, err := dbh.Exec("UPDATE settings SET created_at = ? WHERE id = 1", sentinelTS); err != nil {
			t.Fatal(err)
		}

		migrated, _, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("first observation under the pin returned an error: %v", err)
		}
		if migrated {
			t.Fatal("migration applied while the deployment pin is set")
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if enabled != 0 || rowDigest != "" || marker != 1 {
			t.Fatalf("row after first observation under the pin = (enabled=%d, digest=%q, marker=%d), want (0, '', 1) — state unchanged, marker consumed", enabled, rowDigest, marker)
		}
		var updatedAt1 string
		if err := dbh.QueryRow("SELECT updated_at FROM settings WHERE id = 1").Scan(&updatedAt1); err != nil {
			t.Fatal(err)
		}

		// The pin path consumes the marker on EVERY boot while the pin
		// is present; once the marker is set the second boot must be a
		// pure no-op — no row rewrite, no updated_at churn.
		migrated, _, err = migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("second boot under the pin returned an error: %v", err)
		}
		if migrated {
			t.Fatal("second boot under the pin ran the migration")
		}
		var createdAt, updatedAt2 string
		if err := dbh.QueryRow("SELECT created_at, updated_at FROM settings WHERE id = 1").Scan(&createdAt, &updatedAt2); err != nil {
			t.Fatal(err)
		}
		if createdAt != sentinelTS {
			t.Errorf("pin boot reset created_at: %q -> %q", sentinelTS, createdAt)
		}
		if updatedAt2 != updatedAt1 {
			t.Errorf("pin boot rewrote the row after the marker was consumed: updated_at %q -> %q", updatedAt1, updatedAt2)
		}
	})

	// (f) Pin removed on a later boot: the marker was already consumed
	// while the pin was set (condition (d)'s row), so the state stays
	// fresh-looking but unconsumed — no re-seed, the token is inert.
	t.Run("pin-removed-later-no-reseed", func(t *testing.T) {
		t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
		dbh, _ := newStartupTestEnv(t)
		// The row as (d)'s pin-observation bootstrap left it: marker
		// consumed, state untouched.
		if _, err := dbh.Exec("UPDATE settings SET api_enabled = 0, api_key_digest = '', legacy_token_migrated = 1 WHERE id = 1"); err != nil {
			t.Fatal(err)
		}

		migrated, _, err := migrateLegacyAdminToken(dbh)
		if err != nil {
			t.Fatalf("pin-removed restart returned an error: %v", err)
		}
		if migrated {
			t.Fatal("token seeded after the pin was removed — the marker was already consumed")
		}
		enabled, rowDigest, marker := settingsState(t, dbh)
		if enabled != 0 || rowDigest != "" || marker != 1 {
			t.Fatalf("row after pin removal = (enabled=%d, digest=%q, marker=%d), want (0, '', 1)", enabled, rowDigest, marker)
		}
	})

	// (g) A failed marker-consumption write must fail closed — the
	// migration returns a fatal error so main() aborts startup. It does
	// NOT log-and-continue: continuing would leave the marker unset, and
	// a later user disable (or pin removal) would read as fresh and the
	// legacy token would re-seed on the next boot (finding: legacy token
	// cutover, P1). The broken DB layer is modelled by a read-only
	// connection: every observation-path read succeeds, the INSERT OR
	// REPLACE write fails.
	t.Run("marker-write-failure-aborts-startup", func(t *testing.T) {
		tmpPath := filepath.Join(t.TempDir(), "legacy.sqlite3")
		writable, err := sql.Open("sqlite", tmpPath)
		if err != nil {
			t.Fatalf("open writable test db: %v", err)
		}
		defer func() {
			if err := writable.Close(); err != nil {
				t.Errorf("close writable test db: %v", err)
			}
		}()
		if err := db.RunMigrations(writable); err != nil {
			t.Fatalf("run migrations: %v", err)
		}
		if err := ensureDefaultSettings(writable); err != nil {
			t.Fatalf("ensure default settings: %v", err)
		}
		// A non-fresh row: state present, marker unset — first
		// observation would consume the marker.
		if _, err := writable.Exec("UPDATE settings SET api_enabled = 1, api_key_digest = ? WHERE id = 1", sha256hex("preexistingkey")); err != nil {
			t.Fatal(err)
		}

		readonly, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
		if err != nil {
			t.Fatalf("open read-only test db: %v", err)
		}
		defer func() {
			if err := readonly.Close(); err != nil {
				t.Errorf("close read-only test db: %v", err)
			}
		}()

		t.Run("non-fresh-observation", func(t *testing.T) {
			t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
			migrated, _, migErr := migrateLegacyAdminToken(readonly)
			if migrated {
				t.Fatal("migration seeded despite the marker write failing")
			}
			if migErr == nil {
				t.Fatal("marker write failed but no fatal error returned — startup would continue with the token un-marked")
			}
		})

		t.Run("pin-observation", func(t *testing.T) {
			t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)
			t.Setenv("SERVER_API_ENDPOINT_ENABLED", "true")
			migrated, _, migErr := migrateLegacyAdminToken(readonly)
			if migrated {
				t.Fatal("migration seeded under the pin despite the marker write failing")
			}
			if migErr == nil {
				t.Fatal("pin-path marker write failed but no fatal error returned — startup would continue with the token un-marked")
			}
		})
	})
}

// TestLegacyTokenMigrationIgnoresWhitespacePaddedTokens guards the
// TrimSpace on the env value: a padded token is migrated under its
// trimmed value (the same value the client presents).
func TestLegacyTokenMigrationTrimsToken(t *testing.T) {
	t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", "  legacytoken  ")
	dbh, _ := newStartupTestEnv(t)
	migrated, digest, err := migrateLegacyAdminToken(dbh)
	if !migrated || err != nil {
		t.Fatalf("padded token did not migrate (migrated=%v, err=%v)", migrated, err)
	}
	if digest != sha256hex("legacytoken") {
		t.Fatalf("digest = %q, want sha256(trimmed token)", digest)
	}
}

// TestDigestPresent pins the warning-helper's non-leaking rendering.
func TestDigestPresent(t *testing.T) {
	if got := digestPresent(""); got != "empty" {
		t.Errorf("digestPresent(\"\") = %q, want \"empty\"", got)
	}
	if got := digestPresent(strings.Repeat("ab", 32)); got != "present" {
		t.Errorf("digestPresent(digest) = %q, want \"present\"", got)
	}
}

// TestExternalAPIStatusLine pins the exact startup line each external-API
// state selects: disabled → the loopback/LAN line; enabled with a key
// digest → the authorized line; enabled without a key → the
// reissue-directing line.
func TestExternalAPIStatusLine(t *testing.T) {
	const digest = "1122222222222222222222222222222222222222222222222222222222222222"

	tests := []struct {
		name  string
		state config.AdminAuthState
		want  string
	}{
		{"enabled-with-key", config.AdminAuthState{Enabled: true, KeyDigest: digest},
			"External API enabled — remote access authorized with API key; LAN access unchanged."},
		{"enabled-without-key", config.AdminAuthState{Enabled: true, KeyDigest: ""},
			"External API enabled via env but no key exists — open Settings → External API → Reissue key to mint one for remote clients."},
		{"disabled", config.AdminAuthState{Enabled: false, KeyDigest: digest},
			"External API disabled — loopback/LAN access only."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := externalAPIStatusLine(tc.state); got != tc.want {
				t.Errorf("externalAPIStatusLine(%+v) = %q, want %q", tc.state, got, tc.want)
			}
		})
	}
}

// TestLoadSettingsFromDBRestoresAPIState pins the restart restoration in
// loadSettingsFromDB: the stored digest has no deployment source and is
// ALWAYS restored from the DB row (a lost digest would kill the key on
// every restart); the enabled flag follows the usual YAML/env gate — a
// deployment pin wins over the stored state, and without a pin the stored
// state is restored.
func TestLoadSettingsFromDBRestoresAPIState(t *testing.T) {
	const digest = "1122222222222222222222222222222222222222222222222222222222222222"

	tests := []struct {
		name        string
		envPin      string // value for SERVER_API_ENDPOINT_ENABLED; "" = unset
		rowEnabled  int
		rowDigest   string
		wantEnabled bool
	}{
		// (a) Unpinned: the stored enabled state is restored.
		{"unpinned-restores-enabled-state", "", 1, digest, true},
		// (b) Pin present: the pin governs ONLY the flag — it pins the
		// state to true over a stored disabled row, and the digest still
		// comes from the DB row.
		{"pin-pins-flag-digest-still-restored", "true", 0, digest, true},
		// (c) No pin, DB says enabled: restored.
		{"no-pin-db-enabled-restored", "", 1, digest, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envPin != "" {
				t.Setenv("SERVER_API_ENDPOINT_ENABLED", tc.envPin)
			}
			dbh, cm := newStartupTestEnv(t)
			if _, err := dbh.Exec("UPDATE settings SET api_enabled = ?, api_key_digest = ? WHERE id = 1", tc.rowEnabled, tc.rowDigest); err != nil {
				t.Fatal(err)
			}

			loadSettingsFromDB(dbh, cm)

			apiState := cm.Config().Server.APIEndpoint
			if apiState.Enabled != tc.wantEnabled {
				t.Fatalf("Enabled = %v, want %v (pin=governs flag only, DB governs state)", apiState.Enabled, tc.wantEnabled)
			}
			if apiState.KeyDigest != tc.rowDigest {
				t.Fatalf("KeyDigest = %q, want the DB row's digest restored", apiState.KeyDigest)
			}
		})
	}
}

// TestLegacyTokenMigrationFreshSeedWriteFailure pins the fresh-path fail-
// closed rule (the read-only failure subtest for the observation path,
// extended to the seed): with the legacy var set and the settings row in
// its FRESH state (enabled=0, empty digest, unconsumed marker — exactly
// what ensureDefaultSettings creates), a write-denied DB makes the
// migration return a fatal error and persist nothing. main() fatals on
// that error; a partial seed (enabled=1 with the marker, enabled=1 with
// the marker unconsumed) would be exactly the resurrectable state this
// test must rule out.
func TestLegacyTokenMigrationFreshSeedWriteFailure(t *testing.T) {
	const legacy = "legacytoken"
	t.Setenv("BRAGIBOOKS_ADMIN_TOKEN", legacy)

	tmpPath := filepath.Join(t.TempDir(), "fresh.sqlite3")
	writable, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		t.Fatalf("open writable test db: %v", err)
	}
	defer func() {
		if err := writable.Close(); err != nil {
			t.Errorf("close writable test db: %v", err)
		}
	}()
	if err := db.RunMigrations(writable); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	// The default row IS the fresh state the seed writes into.
	if err := ensureDefaultSettings(writable); err != nil {
		t.Fatalf("ensure default settings: %v", err)
	}

	readonly, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only test db: %v", err)
	}
	defer func() {
		if err := readonly.Close(); err != nil {
			t.Errorf("close read-only test db: %v", err)
		}
	}()

	migrated, digest, migErr := migrateLegacyAdminToken(readonly)
	if migrated || digest != "" {
		t.Fatalf("migration seeded despite the seed write failing (migrated=%v, digest=%q)", migrated, digest)
	}
	if migErr == nil {
		t.Fatal("fresh seed write failed but no fatal error returned — startup would continue with the token unseeded")
	}

	// Nothing partial persisted: the row stays in its exact fresh state —
	// no enabled flag, no digest, no unconsumed-marker half-state.
	enabled, rowDigest, marker := settingsState(t, readonly)
	if enabled != 0 || rowDigest != "" || marker != 0 {
		t.Fatalf("partial state persisted on a failed fresh seed: (enabled=%d, digest=%q, marker=%d), want (0, '', 0)", enabled, rowDigest, marker)
	}
}
