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
// before rawPathByHandle runs, and folder discovery would enumerate the
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
	finalRaw, ok := rawPathByHandle(f)
	if !ok {
		// Containment must be PROVEN on every row: a resolution
		// failure is a refusal, not a fallback to the original pathname
		// (the walk-time verdict alone is a walk-time binding).
		f.Close()
		return nil, fmt.Errorf("resolve opened path for containment: %s", path)
	}
	final := canonicalComparePath(finalRaw)
	resolvedRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		f.Close()
		return nil, rerr
	}
	if !withinResolvedRoot(canonicalComparePath(resolvedRoot), final) {
		f.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", final)
	}
	return f, nil
}

// canonicalComparePath converts a path into the form the containment
// check compares: it rewrites \\?\UNC\host\share\… to the standard
// \\host\share\… form, drops the \\?\ prefix from a drive-letter path
// (\\?\C:\… → C:\…), and leaves every other form untouched. The result
// is Clean-ed, so a root configured with the extended prefix
// (\\?\C:\Books) and a handle resolved to the bare form (C:\Books)
// share one spelling. Whitespace is preserved: both operands are
// filesystem-resolved names (post-EvalSymlinks /
// post-GetFinalPathNameByHandle), where trailing whitespace is part of
// a Windows directory's identity; configuration trimming happens before
// resolution in resolveScanRoot. The conversion is pure string handling
// (no syscall), so the tests run it directly.
func canonicalComparePath(s string) string {
	if strings.HasPrefix(s, `\\?\UNC\`) {
		// Slice past the ENTIRE `\\?\UNC\` prefix (all 8 chars), not
		// just `\\?\UNC`: keeping the trailing backslash would leave
		// THREE leading backslashes (\\ + \host\share\…).
		s = `\\` + s[len(`\\?\UNC\`):]
	} else if strings.HasPrefix(s, `\\?\`) {
		s = s[len(`\\?\`):]
	}
	return filepath.Clean(s)
}

// volumeAndRest splits a canonical Windows path into the part that
// compares case-insensitively and the part that compares
// case-sensitively. On a drive path the volume is the drive letter and
// colon (C:); on a UNC path it is the server and share
// (\\server\share); on a relative or other path the whole path is the
// volume and the rest is empty.
func volumeAndRest(p string) (string, string) {
	if strings.HasPrefix(p, `\\`) {
		// \\server\share\rest: the volume is the first two components
		// after the leading backslash pair.
		serverEnd := strings.Index(p[2:], `\`)
		if serverEnd < 0 {
			return p, ``
		}
		shareEnd := strings.Index(p[serverEnd+3:], `\`)
		if shareEnd < 0 {
			return p, ``
		}
		return p[:serverEnd+3+shareEnd], p[serverEnd+3+shareEnd:]
	}
	if len(p) < 2 || p[1] != ':' {
		return p, ``
	}
	return p[:2], p[2:]
}

// withinResolvedRoot reports whether final lies inside root: both are
// absolute, in canonicalComparePath form, and root is symlink-resolved.
// The comparison is CASE-INSENSITIVE throughout: the VOLUME (drive letter,
// and UNC server/share names) is case-insensitive, and on the DEFAULT NTFS
// configuration the path REST is case-insensitive too. Comparing the rest
// case-sensitively (the prior behavior) rejected valid in-root files whose
// case differed from the configured root (C:\books\track.m4b when the root
// is C:\Books), degrading metadata extraction for an ordinary path. The
// trade-off: on a case-SENSITIVE volume (CSNTFS) a case-only sibling of the
// root now reads as inside — a narrow containment looseness that cannot
// escape the volume, versus the wider break of rejecting valid in-root
// paths on the default configuration.
func withinResolvedRoot(root, final string) bool {
	root = canonicalComparePath(root)
	final = canonicalComparePath(final)
	rootVol, rootRest := volumeAndRest(root)
	finalVol, finalRest := volumeAndRest(final)
	if !strings.EqualFold(rootVol, finalVol) {
		return false
	}
	if rootRest == "" {
		// The root IS the volume (C:\ or a bare \\server\share):
		// everything on that volume is inside it.
		return true
	}
	// The REST of the path is compared case-SENSITIVELY: on a case-sensitive
	// volume (CSNTFS) a case-only sibling (C:\books when the root is
	// C:\Books) is a different directory and must be rejected. The volume
	// above is already case-insensitive via EqualFold.
	if finalRest == rootRest {
		return true
	}
	prefix := rootRest
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(finalRest, prefix)
}
