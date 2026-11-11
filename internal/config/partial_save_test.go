package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestManager builds a manager pointed at a file in t.TempDir with the
// given seed (empty seed = no file). The resolved path is returned so the
// test can assert on file content.
func newTestManager(t *testing.T, seed string) (*ConfigManager, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewConfigManager()
	m.SetConfigPath(path)
	if seed != "" {
		if _, err := m.LoadFrom(path); err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
	}
	return m, path
}

func TestAllowedHostsConfigPrecedence(t *testing.T) {
	t.Setenv("SERVER_ALLOWED_HOSTS", "")
	m, path := newTestManager(t, "server:\n  allowed_hosts: books.lan,books.example\n")
	if got := m.Config().Server.AllowedHosts; got != "books.lan,books.example" {
		t.Fatalf("YAML hosts = %q", got)
	}
	if err := m.SaveKeys(map[string]any{"processing": map[string]any{"num_cpus": 2}}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, readFile(t, path), "allowed_hosts: books.lan,books.example", "hosts lost on settings save")
	t.Setenv("SERVER_ALLOWED_HOSTS", "env.example")
	if _, err := m.LoadFrom(path); err != nil {
		t.Fatal(err)
	}
	if got := m.Config().Server.AllowedHosts; got != "env.example" {
		t.Fatalf("env hosts = %q", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readfile: %v", err)
	}
	return string(b)
}

func assertContains(t *testing.T, s, sub, label string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: %q not in\n%s", label, sub, s)
	}
}

// TestSaveKeysPartialPreservesExternalKeys covers row 12: a settings save
// writes ONLY the explicitly changed keys into the existing file, so
// external keys and unchanged values survive (a full-struct save would
// freeze every key's in-memory value over its env/file value).
func TestSaveKeysPartialPreservesExternalKeys(t *testing.T) {
	seed := "server:\n  port: 9999\ndatabase:\n  path: /data/x.db\nkeeper_key: /data/keep\n"
	m, path := newTestManager(t, seed)

	if err := m.SaveKeys(map[string]any{"server": map[string]any{"port": 8123}}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	s := readFile(t, path)
	assertContains(t, s, "port: 8123", "changed key lost")
	assertContains(t, s, "keeper_key:", "external key lost")
	assertContains(t, s, "path: /data/x.db", "unchanged key value lost")
}

// TestSaveKeysCreatesMissingSections covers partial saves that introduce a
// whole section the file did not have.
func TestSaveKeysCreatesMissingSections(t *testing.T) {
	m, path := newTestManager(t, "")
	if err := m.SaveKeys(map[string]any{"api_key": map[string]any{"base_url": "https://audiobookdb.org/api"}}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	assertContains(t, readFile(t, path), "base_url: https://audiobookdb.org/api", "section create lost")
}

// TestSaveKeysParseErrorIsPropagated covers the load/save error
// propagation requirement: an unreadable existing file must surface as an
// error, not silently produce a fresh all-defaults file.
func TestSaveKeysParseErrorIsPropagated(t *testing.T) {
	m, path := newTestManager(t, "")
	if err := os.WriteFile(path, []byte(":\n not [ yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveKeys(map[string]any{"server": map[string]any{"port": 1}}); err == nil {
		t.Fatal("SaveKeys on unparseable existing file must return an error")
	}
}

// TestSaveWritesConfigFilePrivate covers the secret-bearing-file rule: the
// config file may contain the API key, so both Save and SaveKeys must write
// it 0600 — and must never widen the mode of an existing file.
func TestSaveWritesConfigFilePrivate(t *testing.T) {
	m, path := newTestManager(t, "server:\n  port: 8080\n")

	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertMode(t, path, 0o600)
	if err := m.SaveKeys(map[string]any{"server": map[string]any{"port": 9999}}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	assertMode(t, path, 0o600)

	// A pre-existing world-readable file must be chmodded down on rewrite.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(); err != nil {
		t.Fatalf("Save over 0644 file: %v", err)
	}
	assertMode(t, path, 0o600)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigFallback(path, []byte("server:\n  port: 7777\n"), 0o600); err != nil {
		t.Fatalf("writeConfigFallback over 0644 file: %v", err)
	}
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", path, got, want)
	}
}

// TestStoreAtomicSwapVisible covers row 24: Config/Store use
// atomic.Pointer, so live readers never see a torn struct. The race
// detector is the real check; this pins the visible semantic: the new
// value appears atomically and consistently.
func TestStoreAtomicSwapVisible(t *testing.T) {
	m, _ := newTestManager(t, "")

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = m.Config().Processing.NumCPUs
				_ = m.Config().Server.Port
			}
		}
	}()

	for i := range 200 {
		c := *m.Config()
		c.Processing.NumCPUs = i
		c.Server.Port = 8000 + i
		m.Store(&c)
	}
	close(stop)

	if got := m.Config().Processing.NumCPUs; got != 199 {
		t.Errorf("final NumCPUs = %d, want 199", got)
	}
}

// TestLoadEnvPriorityOverFile covers row 13: when both the env var and the
// YAML file set the same key, the env var wins in the live config.
func TestLoadEnvPriorityOverFile(t *testing.T) {
	seed := "server:\n  port: 9999\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SERVER_PORT", "7777")
	cfg, resolved, err := loadWith(path)
	if err != nil {
		t.Fatalf("loadWith: %v", err)
	}
	if resolved == "" {
		t.Fatal("no resolved path")
	}
	if cfg.Server.Port != 7777 {
		t.Errorf("env-over-file port = %d, want 7777", cfg.Server.Port)
	}
}

// TestLoadParseErrorIsNotSilent covers row 23: a genuine parse error must
// propagate from loadWith, while a purely missing file is the silent
// "no file" case (defaults, no error).
func TestLoadParseErrorIsNotSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("%%%: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadWith(path); err == nil {
		t.Fatal("parse error must propagate from loadWith")
	}
	if _, _, err := loadWith(filepath.Join(dir, "nope.yaml")); err != nil {
		t.Errorf("missing file must not be an error: %v", err)
	}
}

// TestSaveAndSaveKeysUseDefaultConfigPath covers the container default-path
// fallback: with no config file loaded (ConfigPath == ""), Save and
// SaveKeys must fall back to defaultConfigPath() — /config/config.yaml
// when the app-owned /config volume exists, config/config.yaml (relative
// to the working directory) elsewhere.
func TestSaveAndSaveKeysUseDefaultConfigPath(t *testing.T) {
	tests := []struct {
		name      string
		container bool // /config volume present
	}{
		{name: "config-volume-present", container: true},
		{name: "config-volume-absent", container: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			oldWd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(base); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(oldWd) })

			// The test writes real config data to the resolved default path,
			// so it may only target a /config volume it created itself. A
			// pre-existing /config (a dev-machine bind mount) can carry
			// operator config and must never be overwritten; in that
			// environment the case is skipped instead.
			if st, statErr := os.Stat("/config"); statErr == nil && st.IsDir() {
				if !tc.container {
					t.Skipf("/config volume exists; the volume-absent default cannot be exercised")
				}
				t.Skipf("/config volume exists; not writing test data to a real volume")
			} else if tc.container {
				// No volume in this environment; only a root test harness
				// can create it, so skip the case elsewhere.
				if err := os.MkdirAll("/config", 0o755); err != nil {
					t.Skipf("cannot create /config volume for this case: %v", err)
				}
				t.Cleanup(func() { _ = os.RemoveAll("/config") })
			}

			// No explicit path: built the same way NewConfigManager()
			// resolves at startup (conventional search paths only).
			m := NewConfigManager()
			if p := m.ConfigPath(); p != "" && p != "/config/config.yaml" {
				t.Skipf("unexpected config file %q found in this environment; cannot exercise the fallback", p)
			}

			target := filepath.Join(base, "config", "config.yaml")
			if tc.container {
				target = "/config/config.yaml"
			}

			if err := m.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			st, statErr := os.Stat(target)
			if statErr != nil || !st.Mode().IsRegular() {
				t.Errorf("Save did not write %s (stat err = %v)", target, statErr)
			}

			if err := m.SaveKeys(map[string]any{"api_key": map[string]any{"api_key": "k1"}}); err != nil {
				t.Fatalf("SaveKeys: %v", err)
			}
			assertContains(t, readFile(t, target), "api_key: k1", "SaveKeys missed the default-path file")
		})
	}
}

// TestLoadYAMLIndexingEnabledPrecedence pins the overrideKey path for
// directories.indexing_enabled: a YAML file containing the key is
// authoritative when no env var is set, and the env var wins only with
// the EXACT value "true" (any other value leaves the YAML preference in
// effect — the on-only, exact-true contract, unlike the true-ish values
// SERVER_API_ENDPOINT_ENABLED accepts).
func TestLoadYAMLIndexingEnabledPrecedence(t *testing.T) {
	tests := []struct {
		name string
		seed string
		env  string // "" = unset
		want bool
	}{
		{"yaml-true-no-env-enabled", "directories:\n  indexing_enabled: true\n", "", true},
		{"yaml-false-no-env-disabled", "directories:\n  indexing_enabled: false\n", "", false},
		{"yaml-false-env-true-enables", "directories:\n  indexing_enabled: false\n", "true", true},
		// Non-exact env values are NOT pins (Viper env-layering must not
		// win at Unmarshal time): the YAML preference stays in effect.
		{"yaml-true-env-false-preserved", "directories:\n  indexing_enabled: true\n", "false", true},
		{"yaml-true-env-TRUE-preserved", "directories:\n  indexing_enabled: true\n", "TRUE", true},
		{"yaml-true-env-1-preserved", "directories:\n  indexing_enabled: true\n", "1", true},
		{"yaml-true-env-on-preserved", "directories:\n  indexing_enabled: true\n", "on", true},
		{"yaml-true-env-yes-preserved", "directories:\n  indexing_enabled: true\n", "yes", true},
		// Default (no YAML key) is equally protected from non-exact env.
		{"no-yaml-env-false-disabled", "", "false", false},
		{"no-yaml-env-TRUE-disabled", "", "TRUE", false},
		{"no-yaml-env-1-disabled", "", "1", false},
		{"no-yaml-env-on-disabled", "", "on", false},
		{"no-yaml-env-yes-disabled", "", "yes", false},
		{"no-yaml-env-true-enables", "", "true", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv("DIRECTORIES_INDEXING_ENABLED", "")
			} else {
				t.Setenv("DIRECTORIES_INDEXING_ENABLED", tc.env)
			}
			m, _ := newTestManager(t, tc.seed)
			if got := m.Config().Directories.IndexingEnabled; got != tc.want {
				t.Fatalf("IndexingEnabled = %v, want %v (YAML key is authoritative; only exact env \"true\" overrides)", got, tc.want)
			}
		})
	}
}
