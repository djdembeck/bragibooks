package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	_ "modernc.org/sqlite"
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
