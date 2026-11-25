//go:build windows

package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeFinalPath pins the pure string conversion finalPathByHandle
// applies to GetFinalPathNameByHandle results (the syscall itself stays
// windows-bound). It only runs on Windows; on the Linux/macOS CI it does
// not build, so this is the coverage for: a UNC share handle converting to
// exactly the two-leading-backslash form the containment check compares
// against, a volume device path dropping the \\?\ prefix, and an
// already-normal name passing through unchanged.
func TestNormalizeFinalPath(t *testing.T) {
	for in, want := range map[string]string{
		// UNC: the ENTIRE \\?\UNC\ prefix must go, leaving exactly two
		// leading backslashes (three would read every share handle as
		// outside the root).
		`\\?\UNC\server\share\dir\file`: `\\server\share\dir\file`,
		// Device path: the \\?\ prefix drops to the plain drive form.
		`\\?\C:\dir\file`: `C:\dir\file`,
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
	got, ok := proberPath(f, original)
	if ok {
		t.Fatalf("proberPath ok = true for an unresolvable handle; want false (no silent fallback to the original)")
	}
	if got == original {
		t.Errorf("proberPath = %q; the un-verified original pathname must not be returned with ok=false", got)
	}

	// The positive side of the same contract: a resolvable file handle
	// yields a path bound to the handle, NOT the original pathname (on a
	// local volume GetFinalPathNameByHandle returns the \\?\ drive form,
	// normalized back to C:\… with a different case-insensitive spelling
	// only when it differs — assert at minimum that it is a non-empty,
	// resolvable, normalized form).
	dir := t.TempDir()
	p := filepath.Join(dir, "pinned.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	live, err := os.Open(p)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer live.Close()
	got, ok = proberPath(live, p)
	if !ok {
		t.Fatalf("proberPath ok = false for a resolvable handle; want true")
	}
	if got == "" {
		t.Fatal("proberPath returned an empty path with ok = true")
	}
	if strings.HasPrefix(got, `\\?\`) {
		t.Errorf("proberPath = %q; the \\?\\ prefix must be normalized away", got)
	}
}
