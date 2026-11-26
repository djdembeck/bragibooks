//go:build windows

package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeDrivePath pins the shared prefix stripping that
// openInsideRoot and openPartInDir apply before the containment check:
// GetFinalPathNameByHandle yields \\?\C:\… for a local drive, and the
// containment check compares against a root without that prefix. The
// \\?\UNC\ form is already rewritten to \\host\share by
// normalizeFinalPath, so it passes through untouched (stripping it a
// second time would drop the server name).
func TestNormalizeDrivePath(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\dir\file`: `C:\dir\file`,
		// UNC: already converted by normalizeFinalPath; must not be
		// stripped again.
		`\\server\share\dir\file`: `\\server\share\dir\file`,
		// Already-normal: untouched.
		`C:\dir\file`: `C:\dir\file`,
		`D:\vol\file`: `D:\vol\file`,
	} {
		if got := normalizeDrivePath(in); got != want {
			t.Errorf("normalizeDrivePath(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestOpenPartInDirAcceptsPrefixedDrivePath is the regression for the
// \\?\ containment bug: on a local drive, GetFinalPathNameByHandle
// returns \\?\C:\… for the opened part, and the containment check
// compared that form against the bare drive root and refused every
// in-root folder part. After the shared normalization the part must be
// accepted.
func TestOpenPartInDirAcceptsPrefixedDrivePath(t *testing.T) {
	root := t.TempDir()
	part := filepath.Join(root, "part.mp3")
	if err := os.WriteFile(part, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	dir, err := openInsideRoot(root, root, true)
	if err != nil {
		t.Fatalf("openInsideRoot: %v", err)
	}
	defer dir.Close()
	f, err := openPartInDir(dir, "part.mp3", root)
	if err != nil {
		t.Fatalf("openPartInDir refused an in-root part: %v", err)
	}
	defer f.Close()
	// The opened descriptor must name the fixture, not a stale copy.
	gotInfo, err := os.Stat(f.Name())
	if err != nil {
		t.Fatalf("stat pinned part %q: %v", f.Name(), err)
	}
	wantInfo, err := os.Stat(part)
	if err != nil {
		t.Fatalf("stat fixture %q: %v", part, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Errorf("openPartInDir = %q; does not identify the fixture %q", f.Name(), part)
	}
	// Pin the prefix case when the volume uses it: on a local drive the
	// part's final path carries the \\?\ spelling that the old
	// comparison rejected. On a UNC volume the final path is already
	// \\host\share form, and the same containment path applies.
	if final, ok := finalPathByHandle(f); ok && !strings.HasPrefix(root, `\\`) {
		if !strings.HasPrefix(final, `\\?\`) {
			t.Errorf("final path %q lacks the \\?\\ drive prefix on a local volume; the regression case is not pinned", final)
		}
	}
}
