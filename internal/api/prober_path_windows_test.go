//go:build windows

package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizeFinalPath pins the pure string conversion finalPathByHandle
// applies to GetFinalPathNameByHandle results (the syscall itself stays
// windows-bound). It only runs on Windows; on the Linux/macOS CI it does
// not build, so this is the coverage for: a UNC share handle converting to
// exactly the two-leading-backslash form the containment check compares
// against, a volume device path keeping the \\?\ prefix on local drives
// (only \\?\UNC\ is rewritten), and an already-normal name passing
// through unchanged.
func TestNormalizeFinalPath(t *testing.T) {
	for in, want := range map[string]string{
		// UNC: the ENTIRE \\?\UNC\ prefix must go, leaving exactly two
		// leading backslashes (three would read every share handle as
		// outside the root).
		`\\?\UNC\server\share\dir\file`: `\\server\share\dir\file`,
		// Device path: a LOCAL-DRIVE path keeps the \\?\ prefix; only the
		// \\?\UNC\ prefix is rewritten (a \\?\ drive path is a valid
		// spelling of the same file the containment check compares).
		`\\?\C:\dir\file`: `\\?\C:\dir\file`,
		// Already-normal: untouched.
		`C:\dir\file`:    `C:\dir\file`,
		`\\server\share`: `\\server\share`,
	} {
		if got := normalizeFinalPath(in); got != want {
			t.Errorf("normalizeFinalPath(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestProberPathWindowsFailsClosed pins the changed contract: when the
// handle's final path cannot be resolved, proberPath reports ok=false
// instead of falling back to the original pathname — the original was
// never approved for reopening by the open-time containment check, so the
// callers (extractOne, pinPart) degrade the row/part rather than send an
// un-checked pathname to the extractor. The seam is a file whose Fd is an
// unresolvable handle (0: GetFinalPathNameByHandle fails with
// ERROR_INVALID_HANDLE), which makes the syscall fail deterministically
// without needing a live file.
func TestProberPathWindowsFailsClosed(t *testing.T) {
	original := `C:\dir\file`
	// A zero-Fd file: Fd() is 0, an invalid handle, so the resolution
	// fails. Closing it is a no-op (the os.File owns no real descriptor).
	f := &os.File{}
	got, ok := proberPath(f, original, false)
	if ok {
		t.Fatalf("proberPath ok = true for an unresolvable handle; want false (no silent fallback to the original)")
	}
	if got == original {
		t.Errorf("proberPath = %q; the un-verified original pathname must not be returned with ok=false", got)
	}

	// The positive side of the same contract: a resolvable file handle
	// yields a path bound to the handle, NOT the original pathname. The
	// original is a DISTINCT fixture name (both fixtures exist), so the
	// identity check below proves the returned path names the opened
	// file — not an echo of the input. On a local volume
	// GetFinalPathNameByHandle returns the \\?\ drive form and
	// normalizeFinalPath PRESERVES that prefix (only \\?\UNC\ is
	// rewritten), so the test asserts file identity via os.SameFile
	// rather than a prefix spelling.
	dir := t.TempDir()
	p := filepath.Join(dir, "pinned.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	other := filepath.Join(dir, "other.mp3")
	if err := os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatalf("write second fixture: %v", err)
	}
	live, err := os.Open(p)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer live.Close()
	got, ok = proberPath(live, other, false)
	if !ok {
		t.Fatalf("proberPath ok = false for a resolvable handle; want true")
	}
	if got == "" {
		t.Fatal("proberPath returned an empty path with ok = true")
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat proberPath result %q: %v", got, err)
	}
	fixInfo, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat fixture %q: %v", p, err)
	}
	if !os.SameFile(gotInfo, fixInfo) {
		t.Errorf("proberPath = %q; does not identify the opened fixture %q", got, p)
	}
}
