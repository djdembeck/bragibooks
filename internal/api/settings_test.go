package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/db"
	"io"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newSettingsTestDB opens a file-backed SQLite DB (temp dir) with just
// the settings table (the shape the enable/disable/regenerate endpoints
// touch). A file DB, not an in-memory shared one: concurrent connections
// (used by the serialized-row race test) each truncate the shared in-memory
// cache on write, which drops the single row.
func newSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db")
	// _pragma wal / busy timeout: concurrent connections from the pool
	// must not first-hit SQLITE_BUSY (the race test issues parallel
	// writes); WAL is the documented companion to _pragma foreign_keys.
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout%3d5000&_pragma=journal_mode%3dWAL&_pragma=synchronous%3dNORMAL")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE settings (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			audiobookdb_api_key TEXT DEFAULT '',
			audiobookdb_base_url TEXT DEFAULT 'https://audiobookdb.org/api',
			m4b_merge_binary TEXT DEFAULT 'm4b-merge',
			input_dir TEXT DEFAULT '',
			output_dir TEXT DEFAULT '',
			completed_dir TEXT DEFAULT '',
			num_cpus INTEGER DEFAULT 1,
			output_scheme TEXT DEFAULT '{author}/{title}',
			region TEXT DEFAULT 'us',
			api_enabled BOOLEAN NOT NULL DEFAULT 0,
			api_key_digest TEXT NOT NULL DEFAULT '',
			legacy_token_migrated BOOLEAN NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		)`); err != nil {
		t.Fatalf("create settings schema: %v", err)
	}
	return db
}

// newSettingsTestService wires a Handler with a settings DB and a
// ConfigManager whose save target is a throwaway file in a temp dir.
// Processor and AudiobookDB are real instances: UpdateSettings (the form
// save) applies the live config to both at the end.
func newSettingsTestService(t *testing.T, db *sql.DB) *Handler {
	t.Helper()
	dir := t.TempDir()
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(dir, "config.yaml"))
	cfg := cm.Config()
	dbVal := new(atomic.Value)
	dbVal.Store(audiobookdb.NewClient("", cfg.APIKey.BaseURL))
	return NewHandler(&Services{
		DB:          db,
		Config:      cm,
		Processor:   audio.NewProcessor(cfg.M4bMerge.Binary, cfg.Directories.OutputDir, cfg.Directories.CompletedDir, cfg.Processing.NumCPUs, cfg.Processing.PathFormat, "info"),
		AudiobookDB: dbVal,
	})
}

// buildSettingsRouter serves the external-API settings routes on an
// unauthenticated chi-style mux (auth middleware is covered by
// auth_test.go; these tests exercise the handlers end-to-end minus auth).
func buildSettingsRouter(h *Handler) http.Handler {
	r := http.NewServeMux()
	r.HandleFunc("GET /api/settings", h.GetSettings)
	r.HandleFunc("PUT /api/settings", h.UpdateSettings)
	r.HandleFunc("POST /api/settings/api-endpoint/enable", h.EnableAPIEndpoint)
	r.HandleFunc("POST /api/settings/api-endpoint/disable", h.DisableAPIEndpoint)
	r.HandleFunc("POST /api/settings/api-endpoint/regenerate", h.RegenerateAPIEndpoint)
	return r
}

func settingsRoundTrip(t *testing.T, h *Handler, method, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	buildSettingsRouter(h).ServeHTTP(rec, req)
	out := map[string]any{}
	if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %s response: %v (%s)", path, err, data)
		}
	}
	return rec.Code, out
}

func TestSettingsAPIEndpointEnableFlow(t *testing.T) {
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)
	configPath := h.svc.Config.ConfigPath()

	// GET /api/settings: disabled by default, not env-managed, NO digest.
	code, got := settingsRoundTrip(t, h, http.MethodGet, "/api/settings")
	if code != http.StatusOK {
		t.Fatalf("GET settings = %d", code)
	}
	if got["api_endpoint_enabled"] != false {
		t.Errorf("api_endpoint_enabled = %v, want false", got["api_endpoint_enabled"])
	}
	if got["api_endpoint_env_managed"] != false {
		t.Errorf("api_endpoint_env_managed = %v, want false", got["api_endpoint_env_managed"])
	}
	for _, k := range []string{"api_key_digest", "api_key", "key_digest"} {
		if _, ok := got[k]; ok {
			t.Errorf("GET settings exposes %q — the digest must never leave the server", k)
		}
	}

	// Regenerate before enable: 400.
	code, got = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/regenerate")
	if code != http.StatusBadRequest {
		t.Fatalf("regenerate while disabled = %d, want 400 (%v)", code, got)
	}

	// Enable: 200, a bk_-prefixed 64-hex key, digest persisted.
	code, got = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable")
	if code != http.StatusOK {
		t.Fatalf("enable = %d, want 200 (%v)", code, got)
	}
	key, _ := got["api_key"].(string)
	if !strings.HasPrefix(key, "bk_") || len(key) != 3+64 {
		t.Fatalf("enabled api_key = %q, want bk_<64 hex>", key)
	}
	var stored int
	var digest string
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	if stored != 1 {
		t.Errorf("api_enabled = %d, want 1", stored)
	}
	if digest != keyDigestOf(key) {
		t.Errorf("stored digest = %q, want sha256(key)", digest)
	}

	// The YAML file must gain the flag but never any key material.
	rawYaml := ""
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config file: %v (path %q)", err, configPath)
		}
		rawYaml = string(raw)
	}
	if !strings.Contains(rawYaml, "api_endpoint") || !strings.Contains(rawYaml, "enabled: true") {
		t.Errorf("config file lacks the enabled flag:\n%s", rawYaml)
	}
	if strings.Contains(rawYaml, "bk_") || (digest != "" && strings.Contains(rawYaml, digest)) {
		t.Errorf("config file contains key material:\n%s", rawYaml)
	}

	// GET reflects the new state.
	code, got = settingsRoundTrip(t, h, http.MethodGet, "/api/settings")
	if code != http.StatusOK || got["api_endpoint_enabled"] != true {
		t.Fatalf("GET after enable = %d %v, want 200 {api_endpoint_enabled: true}", code, got)
	}

	// Disable: 200, key wiped, flag false.
	code, got = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/disable")
	if code != http.StatusOK {
		t.Fatalf("disable = %d, want 200 (%v)", code, got)
	}
	if got["enabled"] != false {
		t.Errorf("disable response = %v, want enabled: false", got)
	}
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatal(err)
	}
	if stored != 0 || digest != "" {
		t.Errorf("after disable: api_enabled=%d api_key_digest=%q, want 0/''", stored, digest)
	}

	// A plain form save must not clobber the api fields: a re-enable
	// after the disable writes both columns back again.
	code, _ = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable")
	if code != http.StatusOK {
		t.Fatalf("re-enable = %d", code)
	}
	if err := db.QueryRow("SELECT api_enabled FROM settings WHERE id = 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("api_enabled after re-enable = %d, want 1", stored)
	}
}

// TestSettingsAPIEndpointRegenerateInvalidatesPreviousKey pins the
// reissue rule: a new digest, old key dead.
func TestSettingsAPIEndpointRegenerateInvalidatesPreviousKey(t *testing.T) {
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)

	// Seed an enabled state with a known key: DB row + live snapshot
	// (the handler reads the live state; the DB row pins persistence).
	seededKey := "bk_" + strings.Repeat("ab", 32)
	if _, err := db.Exec("INSERT INTO settings (id, api_enabled, api_key_digest) VALUES (1, 1, ?)", keyDigestOf(seededKey)); err != nil {
		t.Fatal(err)
	}
	h.svc.Config.SetAPIEndpointState(true, keyDigestOf(seededKey))

	if !matchAPIKeyDigest(seededKey, keyDigestOf(seededKey)) {
		t.Fatal("seeded key must match its seed digest")
	}

	code, got := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/regenerate")
	if code != http.StatusOK {
		t.Fatalf("regenerate = %d, want 200 (%v)", code, got)
	}
	newKey, _ := got["api_key"].(string)
	if !strings.HasPrefix(newKey, "bk_") || len(newKey) != 3+64 {
		t.Fatalf("regenerated api_key = %q, want bk_<64 hex>", newKey)
	}
	var stored int
	var digest string
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("enabled after reissue = %d, want 1", stored)
	}
	if digest != keyDigestOf(newKey) {
		t.Errorf("row digest after reissue = %q, want sha256(new key)", digest)
	}
	if matchAPIKeyDigest(seededKey, digest) {
		t.Error("the superseded key still matches the new digest — reissue failed to invalidate it")
	}
	if !matchAPIKeyDigest(newKey, digest) {
		t.Error("the new key does not match the stored digest")
	}
}

// TestSettingsAPIEndpointEnvManaged pins the deployment-managed rule:
// enable and disable are refused, but GET reports env_managed.
func TestSettingsAPIEndpointEnvManaged(t *testing.T) {
	t.Setenv("SERVER_API_ENDPOINT_ENABLED", "true")
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)

	code, got := settingsRoundTrip(t, h, http.MethodGet, "/api/settings")
	if code != http.StatusOK {
		t.Fatalf("GET settings = %d", code)
	}
	if got["api_endpoint_env_managed"] != true {
		t.Errorf("env_managed = %v, want true", got["api_endpoint_env_managed"])
	}
	// Env set to true forces the live flag on too (applyEnvPriority).
	if got["api_endpoint_enabled"] != true {
		t.Errorf("enabled = %v, want true (env pin)", got["api_endpoint_enabled"])
	}

	code, _ = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable")
	if code != http.StatusForbidden {
		t.Fatalf("enable while env-managed = %d, want 403", code)
	}
	code, _ = settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/disable")
	if code != http.StatusForbidden {
		t.Fatalf("disable while env-managed = %d, want 403", code)
	}
}

// TestSettingsAPIEndpointRegenerateWhileEnvManaged pins the deliberate
// "the key is not deployment-managed" contract: the env pin manages ONLY
// the enabled flag (enable/disable are refused), so an env-enabled
// endpoint with a missing or rotated key must stay reissue-able. The
// regenerate mints and stores a new digest, leaves api_enabled exactly
// as it was, and enables no flag mutation: consume/enable endpoints stay
// refused (403) in the same pinned state.
func TestSettingsAPIEndpointRegenerateWhileEnvManaged(t *testing.T) {
	t.Setenv("SERVER_API_ENDPOINT_ENABLED", "true")
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)

	seededKey := "bk_" + strings.Repeat("ab", 32)
	if _, err := db.Exec("INSERT INTO settings (id, api_enabled, api_key_digest) VALUES (1, 1, ?)", keyDigestOf(seededKey)); err != nil {
		t.Fatal(err)
	}
	h.svc.Config.SetAPIEndpointState(true, keyDigestOf(seededKey))

	// Same refusal shape as TestSettingsAPIEndpointEnvManaged: only
	// enable/disable are deployment-managed.
	if code, _ := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable"); code != http.StatusForbidden {
		t.Fatalf("enable while env-managed = %d, want 403", code)
	}
	if code, _ := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/disable"); code != http.StatusForbidden {
		t.Fatalf("disable while env-managed = %d, want 403", code)
	}

	code, got := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/regenerate")
	if code != http.StatusOK {
		t.Fatalf("regenerate while env-managed = %d, want 200 (the key is not deployment-managed): %v", code, got)
	}
	newKey, _ := got["api_key"].(string)
	if !strings.HasPrefix(newKey, "bk_") || len(newKey) != 3+64 {
		t.Fatalf("regenerated api_key = %q, want bk_<64 hex>", newKey)
	}

	var stored int
	var digest string
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("api_enabled after pinned reissue = %d, want 1 (the pin governs only the flag, and the row kept it)", stored)
	}
	if digest == keyDigestOf(seededKey) {
		t.Error("stored digest unchanged after reissue — the superseded key would keep working")
	}
	if digest != keyDigestOf(newKey) {
		t.Errorf("row digest after reissue = %q, want sha256(new key)", digest)
	}

	// The live config agrees with the row: the reissue swapped the digest
	// without touching the pinned flag.
	if h.svc.Config.Config().Server.APIEndpoint.KeyDigest != digest {
		t.Errorf("live digest = %q, row digest = %q — live config out of sync after reissue", h.svc.Config.Config().Server.APIEndpoint.KeyDigest, digest)
	}
	if h.svc.Config.Config().Server.APIEndpoint.Enabled != true {
		t.Error("live flag flipped after a pinned reissue — the pin must govern the flag")
	}

	// The same pinned state still refuses the state-mutation endpoints.
	if code, _ := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable"); code != http.StatusForbidden {
		t.Errorf("enable after pinned reissue = %d, want 403", code)
	}
	if code, _ := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/disable"); code != http.StatusForbidden {
		t.Errorf("disable after pinned reissue = %d, want 403", code)
	}
}

// TestSettingsRowWritesAreSerialized pins the settings row's mutual
// exclusion. The form save (UpdateSettings) and the external-API
// enable/disable/regenerate flows are each a full-row read-modify-write
// (snapshot, INSERT OR REPLACE of the whole row, then — for state
// flows — an atomic live-config swap). Without serialization they
// interleave snapshot-read with row-write: a flow whose snapshot
// predates another flow's live swap later rewrites the entire row from
// that snapshot and clobbers the state the other flow persisted — the
// row then disagrees with the live config, and a restart would load the
// clobbered row and silently change what the API accepts.
//
// The invariant serialization provides: at every persistence flow's
// snapshot-to-write window (widened via the SettingsPersistDelay test
// hook) the row and the live config agree exactly on (api_enabled,
// api_key_digest), because every row write happens paired with its live
// swap, and each flow's snapshot is current at its write. A clobbering
// stale write breaks that agreement, which the wide window makes an
// in-window observation near-certain. The final state must agree as
// well.
func TestSettingsRowWritesAreSerialized(t *testing.T) {
	db := newSettingsTestDB(t)
	// Pre-seed the row: WAL readers use a snapshot from their txn start,
	// so a SELECT in the hook that starts before the first concurrent
	// INSERT OR REPLACE commits would see "no rows". With the row present
	// from the start, every operation is an UPDATE and the count is stable.
	if _, err := db.Exec(`INSERT INTO settings (id, api_enabled, api_key_digest) VALUES (1, 0, '')`); err != nil {
		t.Fatal(err)
	}
	h := newSettingsTestService(t, db)
	h.svc.SettingsPersistDelay = func() {
		assertSettingsRowAgreesWithLive(t, db, h.svc.Config, "mid-window")
		time.Sleep(50 * time.Millisecond)
	}
	router := buildSettingsRouter(h)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			settingSave(t, router)
		}()
		go func() {
			defer wg.Done()
			enableAndDisable(t, router)
		}()
	}
	wg.Wait()

	assertSettingsRowAgreesWithLive(t, db, h.svc.Config, "final")
}

// assertSettingsRowAgreesWithLive checks the settings-row serialization
// invariant: the row the persistence flows last wrote and the live
// config carry the same external-API state (enabled flag and key
// digest). Under unsynchronized interleaving, a late full-row write from
// a stale snapshot breaks exactly this.
func assertSettingsRowAgreesWithLive(t *testing.T, db *sql.DB, cm *config.ConfigManager, ctx string) {
	t.Helper()
	live := cm.Config()
	var stored int
	var digest string
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatalf("%s: read settings row: %v", ctx, err)
	}
	if live.Server.APIEndpoint.Enabled != (stored == 1) {
		t.Errorf("%s: row api_enabled = %d, live = %v — a concurrent write clobbered the row", ctx, stored, live.Server.APIEndpoint.Enabled)
	}
	if live.Server.APIEndpoint.KeyDigest != digest {
		t.Errorf("%s: row digest = %q, live = %q — the row is not the last flow's final write", ctx, digest, live.Server.APIEndpoint.KeyDigest)
	}
}

// settingSave performs one full-row form save (PUT /api/settings).
func settingSave(t *testing.T, router http.Handler) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"num_cpus":2}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)
}

// enableAndDisable performs the two external-API state transitions, one
// full-row read-modify-write each.
func enableAndDisable(t *testing.T, router http.Handler) {
	t.Helper()
	for _, p := range []string{"/api/settings/api-endpoint/enable", "/api/settings/api-endpoint/disable"} {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// TestRegenerateAfterDisableCannotReopen pins the regenerate
// precondition race (finding: regenerate precondition race). The
// "enabled" check is enforced INSIDE settingsMu, against the same locked
// snapshot the write is built from — a disable that commits before the
// regenerate takes the lock must make the regenerate fail with 400
// "enable the external API first", never persist enabled=true with a
// fresh key. The interleaving is driven deterministically through the
// SettingsPersistDelay hook (no timing dependence): while the regenerate
// holds the lock inside its widened pre-commit window, the test flips
// the live state to disabled — exactly a concurrent disable that landed
// in the window if the lock did not serialize it — then releases the
// hook and observes the outcome.
func TestRegenerateAfterDisableCannotReopen(t *testing.T) {
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)
	router := buildSettingsRouter(h)

	// Seed an enabled state the regenerate is allowed to see.
	seededKey := "bk_" + strings.Repeat("cd", 32)
	if _, err := db.Exec("INSERT INTO settings (id, api_enabled, api_key_digest) VALUES (1, 1, ?)", keyDigestOf(seededKey)); err != nil {
		t.Fatal(err)
	}
	h.svc.Config.SetAPIEndpointState(true, keyDigestOf(seededKey))

	// Delivery channel for the request: the hook blocks the in-flight
	// regenerate (under the settings lock) until the test drives the
	// interleave.
	h.svc.SettingsPersistDelay = func() {
		// A concurrent disable that commits inside the regenerate's
		// snapshot-to-write window (in production the disable blocks on
		// the settingsMu this flow holds here and commits the moment it
		// is released — the hook is the deterministic stand-in for that
		// window: it flips the state the next in-lock read must see).
		// The hook runs under the lock, so the outer flow's snapshot is
		// taken after this flip, exactly like one taken after the
		// disable committed.
		h.svc.Config.SetAPIEndpointState(false, "")
		if _, err := db.Exec("UPDATE settings SET api_enabled = 0, api_key_digest = '' WHERE id = 1"); err != nil {
			t.Errorf("simulate disable row write: %v", err)
		}
	}
	t.Cleanup(func() { h.svc.SettingsPersistDelay = nil })

	req := httptest.NewRequest(http.MethodPost, "/api/settings/api-endpoint/regenerate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	code := rec.Code
	got := map[string]any{}
	if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("decode regenerate response: %v (%s)", err, data)
		}
	}

	// The regenerate must NOT have succeeded: a disabled pre-commit
	// snapshot fails the in-lock precondition.
	if code == http.StatusOK {
		t.Fatalf("regenerate after a concurrent disable succeeded — remote access reopened (body: %v)", got)
	}
	if code != http.StatusBadRequest {
		t.Fatalf("regenerate after disable = %d, want 400 (body: %v)", code, got)
	}
	// And the persisted state must remain disabled with no key.
	var stored int
	var digest string
	if err := db.QueryRow("SELECT api_enabled, api_key_digest FROM settings WHERE id = 1").Scan(&stored, &digest); err != nil {
		t.Fatal(err)
	}
	if stored != 0 || digest != "" {
		t.Fatalf("row after failed regenerate = (enabled=%d, digest=%q), want (0, '')", stored, digest)
	}
	if live := h.svc.Config.Config(); live.Server.APIEndpoint.Enabled {
		t.Error("live state re-enabled after a failed regenerate")
	}
}

// TestGenerateAPIKeyFault closes the cryptoRand test seam
// (finding: cryptoRand seam untested): the "could not generate API key"
// 500 branch must fire when the entropy source fails, and neither the
// DB row nor the persisted flag file may change.
func TestGenerateAPIKeyFault(t *testing.T) {
	fault := errors.New("entropy failure")
	oldRand := cryptoRand
	cryptoRand = readerFunc(func(_ []byte) (int, error) { return 0, fault })
	t.Cleanup(func() { cryptoRand = oldRand })

	t.Run("enable", func(t *testing.T) {
		db := newSettingsTestDB(t)
		h := newSettingsTestService(t, db)

		before := settingsRowSnapshot(t, db) // fresh DB: no row
		code, got := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/enable")
		if code != http.StatusInternalServerError {
			t.Fatalf("enable with faulted entropy = %d, want 500 (%v)", code, got)
		}
		// Neither the DB row nor the persisted flag file may change.
		assertSettingsStateUntouched(t, db, h.svc.Config.ConfigPath(), before)
	})

	t.Run("regenerate", func(t *testing.T) {
		db := newSettingsTestDB(t)
		h := newSettingsTestService(t, db)

		seededKey := "bk_" + strings.Repeat("ef", 32)
		if _, err := db.Exec("INSERT INTO settings (id, api_enabled, api_key_digest) VALUES (1, 1, ?)", keyDigestOf(seededKey)); err != nil {
			t.Fatal(err)
		}
		h.svc.Config.SetAPIEndpointState(true, keyDigestOf(seededKey))
		before := settingsRowSnapshot(t, db) // enabled + seeded digest

		code, got := settingsRoundTrip(t, h, http.MethodPost, "/api/settings/api-endpoint/regenerate")
		if code != http.StatusInternalServerError {
			t.Fatalf("regenerate with faulted entropy = %d, want 500 (%v)", code, got)
		}
		// The seeded digest must survive a failed reissue: the row is
		// byte-for-byte what it was before the attempt.
		assertSettingsStateUntouched(t, db, h.svc.Config.ConfigPath(), before)
	})
}

// settingsRow is the (external-API-relevant) settings-row state a failed
// flow must not touch.
type settingsRow struct {
	present bool
	enabled int
	digest  string
	marker  int
}

// settingsRowSnapshot captures the settings row (absent = zero values).
func settingsRowSnapshot(t *testing.T, db *sql.DB) settingsRow {
	t.Helper()
	row := settingsRow{}
	if err := db.QueryRow("SELECT api_enabled, api_key_digest, COALESCE(legacy_token_migrated, 0) FROM settings WHERE id = 1").Scan(&row.enabled, &row.digest, &row.marker); err != nil {
		if err == sql.ErrNoRows {
			return row
		}
		t.Fatalf("read settings row: %v", err)
	}
	row.present = true
	return row
}

// assertSettingsStateUntouched asserts the settings row and the YAML flag
// file are exactly their pre-attempt state: a failed flow must not have
// written either store.
func assertSettingsStateUntouched(t *testing.T, db *sql.DB, configPath string, before settingsRow) {
	t.Helper()
	after := settingsRowSnapshot(t, db)
	if before != after {
		t.Errorf("settings row changed by a failed flow: before %+v, after %+v", before, after)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("flag file %q was written by a failed flow (stat err=%v)", configPath, err)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// assertStateUntouched asserts the settings row and the YAML flag file
// are exactly as created: api_enabled=0, empty digest, marker 0, and
// the flag file absent (a failed flow must not write it).
func assertStateUntouched(t *testing.T, db *sql.DB, configPath string) {
	t.Helper()
	var stored int
	var digest string
	var marker int
	if err := db.QueryRow("SELECT api_enabled, api_key_digest, COALESCE(legacy_token_migrated, 0) FROM settings WHERE id = 1").Scan(&stored, &digest, &marker); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	if stored != 0 || digest != "" || marker != 0 {
		t.Errorf("row changed by a failed flow: (enabled=%d, digest=%q, marker=%d), want (0, '', 0)", stored, digest, marker)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("flag file %q was written by a failed flow (stat err=%v)", configPath, err)
	}
}

// newTestSettingsHandler builds a Handler with a settings-migration DB in a
// temp dir and a ConfigManager pointed at a temp YAML file (before any
// SaveKeys, per the partial-save pattern).
func newTestSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.RunMigrations(d); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(dir, "config.yaml"))

	av := new(atomic.Value)
	av.Store(audiobookdb.NewClient("", ""))

	return &Handler{svc: &Services{
		DB:          d,
		Config:      cm,
		Processor:   audio.NewProcessor("/bin/true", t.TempDir(), "", 1, "{author}/{title}", "info"),
		AudiobookDB: av,
	}}
}

func doPutSettings(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateSettings(rec, req)
	return rec
}

func TestUpdateSettingsOutputSchemeValidation(t *testing.T) {
	cases := []struct {
		name     string
		scheme   string
		want     int
		wantBody string
	}{
		{name: "forbidden colon rejected", scheme: `{title}:x`, want: http.StatusBadRequest, wantBody: "output scheme:"},
		{name: "clean scheme accepted", scheme: `{author}/{title}`, want: http.StatusOK, wantBody: `"output_scheme":"{author}/{title}"`},
		{name: "reserved device name rejected", scheme: `{con}`, want: http.StatusBadRequest, wantBody: "output scheme:"},
		{name: "year scheme accepted (warnings do not gate)", scheme: `{author}/{year}`, want: http.StatusOK, wantBody: `"output_scheme":"{author}/{year}"`},
		{name: "asin alone rejected", scheme: `{asin}`, want: http.StatusBadRequest, wantBody: "output scheme:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestSettingsHandler(t)
			out := t.TempDir()
			rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/i","output_dir":%q,"output_scheme":%q}`, out, tc.scheme))
			if rec.Code != tc.want {
				t.Fatalf("PUT output_scheme=%q: status = %d, want %d (body %s)", tc.scheme, rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("PUT output_scheme=%q: body %q does not contain %q", tc.scheme, rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestUpdateSettingsSchemeRoundTrip pins the wire contract: the raw template
// string is stored and returned unchanged.
func TestUpdateSettingsSchemeRoundTrip(t *testing.T) {
	h := newTestSettingsHandler(t)
	out := t.TempDir()
	rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/i","output_dir":%q,"output_scheme":"{author}/vol. {title}"}`, out))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec2 := httptest.NewRecorder()
	h.GetSettings(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec2.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want %q", got["output_scheme"], "{author}/vol. {title}")
	}
}

// TestUpdateSettingsOmittedOutputSchemeRetained pins the nil-optional-field
// branch: a partial update that does not send output_scheme leaves the
// stored path format untouched.
func TestUpdateSettingsOmittedOutputSchemeRetained(t *testing.T) {
	h := newTestSettingsHandler(t)
	out := t.TempDir()
	if rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/i","output_dir":%q,"output_scheme":"{author}/vol. {title}"}`, out)); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// No output_scheme in the body.
	if rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/in","output_dir":%q}`, t.TempDir())); rec.Code != http.StatusOK {
		t.Fatalf("partial PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	h.GetSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want retained %q", got["output_scheme"], "{author}/vol. {title}")
	}
}

// TestUpdateSettingsRejectedRequestChangesNothing pins no-partial-persistence:
// a 400 on one field must leave the other fields' stored values unchanged.
func TestUpdateSettingsRejectedRequestChangesNothing(t *testing.T) {
	h := newTestSettingsHandler(t)
	out := t.TempDir()
	if rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/i","output_dir":%q,"output_scheme":"{author}/vol. {title}"}`, out)); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec := doPutSettings(t, h, fmt.Sprintf(`{"input_dir":"/in","output_dir":%q,"output_scheme":"{con}"}`, t.TempDir()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rejected PUT: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "output scheme:") {
		t.Fatalf("rejected PUT: body = %s, want validation error containing %q", rec.Body.String(), "output scheme:")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec2 := httptest.NewRecorder()
	h.GetSettings(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec2.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["input_dir"] != "/i" {
		t.Fatalf("GET input_dir = %v, want %q (should be unchanged after rejection)", got["input_dir"], "/i")
	}
	if got["output_dir"] != out {
		t.Fatalf("GET output_dir = %v, want %q (should be unchanged after rejection)", got["output_dir"], out)
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want %q (should be unchanged after rejection)", got["output_scheme"], "{author}/vol. {title}")
	}
}

// TestUpdateSettingsDirectoryWriteAccess covers the save-time rule for the
// directories the server writes to: a CHANGED value the server cannot create
// entries in is a 400 naming the path, an unchanged stored value is not
// re-probed (so a mount that went away cannot block unrelated saves), and a
// blank completed_dir always clears.
func TestUpdateSettingsDirectoryWriteAccess(t *testing.T) {
	h := newTestSettingsHandler(t)
	locked := readOnlyTestDir(t)
	out := t.TempDir()

	// Seed a valid output dir; completed_dir starts empty.
	seedBody := fmt.Sprintf(`{"input_dir":"/i","output_dir":%q}`, out)
	if rec := doPutSettings(t, h, seedBody); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	blocked := filepath.Join(locked, "completed")
	rec := doPutSettings(t, h, fmt.Sprintf(`{"completed_dir":%q}`, blocked))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT completed_dir: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"completed directory ", blocked, "is not writable", "permission denied"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body = %q; want it to contain %q", rec.Body.String(), want)
		}
	}

	rec = doPutSettings(t, h, fmt.Sprintf(`{"output_dir":%q}`, filepath.Join(locked, "out")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT output_dir: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "output directory ") {
		t.Errorf("body = %q; want it to name the output directory setting", rec.Body.String())
	}

	// Persist the bad value directly (the mount-went-away case): an
	// unrelated save that re-sends it unchanged must still succeed, as must
	// clearing it.
	if _, err := h.svc.DB.Exec(`UPDATE settings SET completed_dir = ? WHERE id = 1`, blocked); err != nil {
		t.Fatalf("seed completed_dir: %v", err)
	}
	cm := h.svc.Config
	live := cm.Config()
	live.Directories.CompletedDir = blocked
	cm.Store(live)

	if rec := doPutSettings(t, h, fmt.Sprintf(`{"completed_dir":%q}`, blocked)); rec.Code != http.StatusOK {
		t.Fatalf("unchanged completed_dir: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := doPutSettings(t, h, `{"completed_dir":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clearing completed_dir: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}
