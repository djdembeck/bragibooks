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
