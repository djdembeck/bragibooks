//go:build windows

package api

import "testing"

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
