//go:build windows

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openIndexDir opens dir for the index walk's chunked ReadDir. Windows
// has no O_NOFOLLOW/O_NONBLOCK open flags: FindFirstFile (what ReadDir
// drives) reports a junction/reparse point as a FILE, not a directory,
// so the no-follow guarantee the Unix flags give comes from the
// enumeration itself, and a swapped-in named pipe opens without a writer
// blocking the worker (a blocking read-open on a pipe waits for a writer,
// but FindFirstFile on a pipe does not). The plain open is therefore the
// Windows-equivalent of the Unix directory-only open.
func openIndexDir(dir string) (*os.File, error) {
	return os.Open(dir)
}

// openIndexChildDir opens the child directory name against the ALREADY-
// OPEN parent descriptor. Windows has no openat (a truly handle-relative
// open), so the relative open is built from the parent's full path (f.Name()
// is the path the parent was opened with — the walk never re-points it): a
// child is opened relative to that same directory, so an ancestor replaced
// between the entry read and the open cannot redirect the walk outside the
// root the way a full-path re-open from the walk's original root string can.
// Two fail-closed checks close the residual path-string window:
//
//  1. Lstat the child BEFORE the open: the walk must never follow a
//     junction/reparse point as a directory (the enumeration reports one as
//     a file), so a child that is a reparse point at open time is swapped in
//     after the entry read — refuse it (skip the child) instead of opening
//     whatever the reparse target points at. The check is against the SAME
//     joined path the open uses, so it verifies the exact name being opened.
//
//  2. AFTER the open, bind the handle to its resolved final path
//     (rawPathByHandle, the same technique openInsideRoot uses) and verify
//     it stays inside the scan's ORIGINAL resolved root (withinResolvedRoot,
//     against the root string as captured by resolveScanRoot at scan
//     start): os.Open FOLLOWS reparse points, so the Lstat above (which
//     does not) is not a binding for the open — a junction swapped into an
//     ancestor between the entry read and the open would redirect the
//     handle outside the root, and readDirWith would then enumerate that
//     outside directory. The comparison base is the original resolved root
//     STRING, not a fresh EvalSymlinks(root): a re-resolution would follow
//     the swapped ancestor junction and re-base the comparison onto the
//     outside tree, letting the redirected child pass. Windows has no
//     openat, so there is no per-component / root-handle anchor — the
//     original resolved root string is the strongest binding the platform
//     allows (a root re-open by pathname would follow the same junction).
//     A resolution failure is a refusal, not a fallback.
//
// The directory verdict is the enumeration's (FindFirstFile reports a
// reparse point as a file), same as openIndexDir.
func openIndexChildDir(parent *os.File, name, root string) (*os.File, error) {
	joined := filepath.Join(parent.Name(), name)
	// Fail closed on a reparse-point child: the walk never follows
	// junctions, so a reparse point here is a swap-in (the entry read saw a
	// real directory). Lstat does NOT follow the reparse point — it reports
	// the link itself — so ModeSymlink is the reparse indicator. Any Lstat
	// error (vanished, permission) fails the open with that same error:
	// the plain open below would fail too, so the walk stops on it.
	if info, err := os.Lstat(joined); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		// Reparse point: skip the child (the caller treats the error as a
		// walk stop for this subtree).
		return nil, fmt.Errorf("child %q is a reparse point", name)
	}
	f, err := os.Open(joined)
	if err != nil {
		return nil, err
	}
	// Bind the handle to its resolved final path and verify containment
	// against the scan's ORIGINAL resolved root (check 2 in the doc
	// above): os.Open follows reparse points, so the Lstat is not a
	// binding for the open. root is already the symlink-resolved form
	// captured by resolveScanRoot at scan start — the comparison base is
	// that original string, NOT a fresh EvalSymlinks(root): re-resolving
	// would follow a junction a writer swapped into an ancestor between
	// the scan start and this open, re-basing the comparison onto the
	// outside tree the child was redirected into.
	finalRaw, ok := rawPathByHandle(f)
	if !ok {
		f.Close()
		return nil, fmt.Errorf("resolve opened index directory for containment: %s", joined)
	}
	if !withinResolvedRoot(root, finalRaw) {
		f.Close()
		return nil, fmt.Errorf("opened index directory escapes input root: %s", finalRaw)
	}
	return f, nil
}

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
// The VOLUME (drive letter, and UNC server/share names) is compared
// case-INSENSITIVELY (drive-letter and server/share case is not
// meaningful), and the path REST is compared case-SENSITIVELY: on a
// case-sensitive volume (CSNTFS) a case-only sibling (C:\books when the
// root is C:\Books) is a different directory and must be rejected, and
// the strict rule keeps a case-only sibling of the root from reading as
// inside on every configuration.
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
