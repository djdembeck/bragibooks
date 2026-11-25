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
	// GetFinalPathNameByHandle yields \\?\C:\… device paths; drop the
	// prefix so the comparison runs on the same form as the resolved
	// root (a \\?\UNC\ name is already converted to \\host\share form
	// by finalPathByHandle).
	if strings.HasPrefix(final, `\\?\`) && !strings.HasPrefix(final, `\\?\UNC\`) {
		final = final[len(`\\?\`):]
	}
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

// withinResolvedRoot reports whether final lies inside root: both are
// absolute, root is symlink-resolved, and the comparison is
// case-insensitive (NTFS is case-insensitive, and a case-only mismatch
// must not read as "outside").
func withinResolvedRoot(root, final string) bool {
	rootL := strings.ToLower(filepath.Clean(root))
	finalL := strings.ToLower(filepath.Clean(final))
	if rootL == finalL {
		return true
	}
	prefix := rootL
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(finalL, prefix)
}
