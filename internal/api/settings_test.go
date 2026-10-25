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
	"testing"

	"github.com/bragibooks/bragibooks/internal/config"
	_ "modernc.org/sqlite"
)

// newSettingsTestDB opens an in-memory SQLite DB with just the settings
// table (the shape the enable/disable/regenerate endpoints touch).
func newSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
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
func newSettingsTestService(t *testing.T, db *sql.DB) *Handler {
	t.Helper()
	dir := t.TempDir()
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(dir, "config.yaml"))
	return NewHandler(Services{DB: db, Config: cm})
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
