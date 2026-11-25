//go:build !windows && !linux

package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProberPathOtherUnixRefusesUnpinned pins the fail-closed contract:
// with no /proc on this platform, proberPath cannot name the descriptor
// by path, so it returns the original pathname only as a harmless
// payload and reports ok=false — the callers (extractOne, pinPart) take
// the refusal path and degrade the row/part rather than hand an
// un-pinned name to the extractor. The test does not run on Linux CI
// (Linux resolves the descriptor through /proc) — it protects the
// macOS/BSD build.
func TestProberPathOtherUnixRefusesUnpinned(t *testing.T) {
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
	got, ok := proberPath(f, p)
	if ok {
		t.Fatal("proberPath ok = true; want false (no procfs: the unpinned name must be refused)")
	}
	if got != p {
		t.Fatalf("proberPath = %q; want the original pathname %q as the harmless payload", got, p)
	}
}
