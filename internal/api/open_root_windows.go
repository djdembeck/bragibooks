//go:build windows

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openInsideRoot opens path for probing, binding the probe to the file at
// open time. The walk's earlier symlink-confinement check is a walk-time
// verdict — a regular file replaced by an escaping symlink in between
// would otherwise be re-followed by the probe; the open-time check catches
// that here. Directories pass through when allowDir (folder aggregation
// needs them; the walk already confined them) — and a DIRECTORY row is
// contained-verified too: a directory replaced by a junction/reparse point
// to an outside directory before the open would otherwise be accepted
// before finalPathByHandle runs, and folder discovery would enumerate the
// outside directory. A FILE row must open as a regular file whose resolved
// final path stays inside the input root: os.Open follows reparse points,
// so the check runs on the handle's final name (the same resolution
// proberPath uses for the probe's pathname), never on root alone.
func openInsideRoot(path, root string, allowDir bool) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, serr := f.Stat()
	if serr != nil {
		f.Close()
		return nil, serr
	}
	isDir := info.IsDir()
	if isDir && !allowDir {
		// A file row that is (or became) a directory: the row kind
		// changed out from under the walk.
		f.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", path)
	}
	if !isDir && !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", path)
	}
	final, ok := finalPathByHandle(f)
	if !ok {
		// Containment must be PROVEN on every row: a resolution
		// failure is a refusal, not a fallback to the original pathname
		// (the walk-time verdict alone is a walk-time binding).
		f.Close()
		return nil, fmt.Errorf("resolve opened path for containment: %s", path)
	}
	final = normalizeDrivePath(final)
	resolvedRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		f.Close()
		return nil, rerr
	}
	if !withinResolvedRoot(resolvedRoot, final) {
		f.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", final)
	}
	return f, nil
}

// normalizeDrivePath converts a GetFinalPathNameByHandle result into the
// form the containment check compares: it drops the \\?\ prefix from a
// drive-letter path (\\?\C:\… → C:\…), so the comparison runs on the
// same form as the resolved root. A \\?\UNC\ name is left alone:
// finalPathByHandle already converted it to \\host\share form.
func normalizeDrivePath(s string) string {
	if strings.HasPrefix(s, `\\?\`) && !strings.HasPrefix(s, `\\?\UNC\`) {
		return s[len(`\\?\`):]
	}
	return s
}

// withinResolvedRoot reports whether final lies inside root: both are
// absolute and root is symlink-resolved. Drive letters compare
// case-insensitively (Windows drive letters are case-insensitive), but
// the rest of the path compares case-SENSITIVELY: on a case-sensitive
// volume (CSNTFS) a case-only sibling of the root (C:\books when the
// root is C:\Books) is a different directory and must not read as
// inside. UNC roots stay fully case-insensitive: server and share
// names are case-insensitive.
func withinResolvedRoot(root, final string) bool {
	root = filepath.Clean(root)
	final = filepath.Clean(final)
	if strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, `\\?\UNC\`) {
		rootL := strings.ToLower(root)
		finalL := strings.ToLower(final)
		if rootL == finalL {
			return true
		}
		prefix := rootL
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		return strings.HasPrefix(finalL, prefix)
	}
	if len(root) < 2 || len(final) < 2 {
		return false
	}
	if !strings.EqualFold(root[:2], final[:2]) {
		return false
	}
	// Reuse the root's drive-letter spelling in final so the prefix
	// check compares the remainder case-sensitively while the drive
	// letter stays case-insensitive (c:\Books\x is inside C:\Books).
	folded := root[:2] + final[2:]
	if folded == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(folded, prefix)
}
