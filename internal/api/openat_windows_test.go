//go:build windows

package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalComparePath pins the shared normalization that
// openInsideRoot and openPartInDir apply to BOTH containment operands:
// GetFinalPathNameByHandle yields \\?\C:\… for a local drive and
// \\?\UNC\host\share\… for a share, and the containment check compares
// against a root without the extended prefix.
func TestCanonicalComparePath(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\dir\file`: `C:\dir\file`,
		// Extended UNC: the raw form the handle resolution returns;
		// rewritten to the standard UNC form.
		`\\?\UNC\server\share\dir\file`: `\\server\share\dir\file`,
		// Standard UNC: untouched (rewriting it again would drop the
		// server name).
		`\\server\share\dir\file`: `\\server\share\dir\file`,
		// Already-normal: untouched.
		`C:\dir\file`: `C:\dir\file`,
		`D:\vol\file`: `D:\vol\file`,
		// Whitespace is preserved: a trailing NBSP is part of a
		// resolved directory's identity (configuration trimming
		// happens before resolution in resolveScanRoot).
		"C:\\Books\\vol1\u00a0": "C:\\Books\\vol1\u00a0",
	} {
		if got := canonicalComparePath(in); got != want {
			t.Errorf("canonicalComparePath(%q) = %q; want %q", in, got, want)
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
	// Pin the DOS device form when the volume uses it: on a local drive
	// the part's raw handle path must be the drive-letter form \\?\C:\…
	// (the old comparison rejected the prefix; the old flag returned
	// the Volume{GUID} form, which the containment check cannot
	// compare against a drive-letter root at all). On a UNC volume the
	// raw path is \\?\UNC\host\share form, and the same containment
	// path applies.
	if final, ok := rawPathByHandle(f); ok && !strings.HasPrefix(root, `\\`) {
		if !strings.HasPrefix(final, `\\?\`) {
			t.Errorf("final path %q lacks the \\?\\ drive prefix on a local volume; the regression case is not pinned", final)
		}
		if strings.Contains(final, `Volume{`) {
			t.Errorf("final path %q is the VOLUME_NAME_GUID form; the containment check expects the DOS drive-letter form", final)
		}
	}
}

// TestWithinResolvedRoot pins the CSNTFS boundary: the VOLUME
// (drive letter, UNC server and share) compares case-insensitively
// (c:\Books is the same drive as C:\Books, \\Server\Share is the same
// share as \\server\share), and the REST of the path is
// case-sensitive (C:\books and \\s\h\Books are different directories
// on a case-sensitive volume).
func TestWithinResolvedRoot(t *testing.T) {
	for name, tc := range map[string]struct {
		root  string
		final string
		want  bool
	}{
		"in-root":                    {`C:\Books`, `C:\Books\vol1\part.mp3`, true},
		"root itself":                {`C:\Books`, `C:\Books`, true},
		"drive letter case differs":  {`C:\Books`, `c:\Books\vol1\part.mp3`, true},
		"case-only sibling rejected": {`C:\Books`, `C:\books\vol1\part.mp3`, false},
		"prefix-name sibling":        {`C:\Books`, `C:\Books2\vol1\part.mp3`, false},
		"other drive":                {`C:\Books`, `D:\Books\vol1\part.mp3`, false},
		"drive-root in-root":         {`C:\`, `C:\file.mp3`, true},
		"drive-root other drive":     {`C:\`, `D:\file.mp3`, false},
		"unc in-root":                {`\\server\share\books`, `\\server\share\books\vol1\part.mp3`, true},
		"unc case-insensitive":       {`\\Server\Share`, `\\server\share\vol1\part.mp3`, true},
		"unc case-only dir rejected": {`\\s\h\books`, `\\s\h\Books\x`, false},
		"extended drive root":        {`\\?\C:\Books`, `C:\Books\vol1\part.mp3`, true},
		"extended unc root":          {`\\?\UNC\s\h`, `\\s\h\file`, true},
	} {
		if got := withinResolvedRoot(tc.root, tc.final); got != tc.want {
			t.Errorf("withinResolvedRoot(%q, %q) = %v; want %v (%s)", tc.root, tc.final, got, tc.want, name)
		}
	}
}
