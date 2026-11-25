//go:build !windows && !linux

package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProberPathOtherUnixReturnsOriginal pins the no-procfs fallback:
// with no /proc on this platform, proberPath cannot name the descriptor
// by path and must hand the probe the ORIGINAL pathname verbatim (the
// walk-time plus open-time containment checks remain the binding). The
// test does not run on Linux CI (Linux resolves the descriptor through
// /proc) — it protects the macOS/BSD build.
func TestProberPathOtherUnixReturnsOriginal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "media", "book.m4b")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := proberPath(f, p); got != p {
		t.Fatalf("proberPath = %q; want the original pathname %q", got, p)
	}
}
