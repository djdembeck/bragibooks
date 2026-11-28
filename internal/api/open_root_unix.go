//go:build !windows

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rootContains reports whether target lies inside root: the same
// containment rule the walk applies to symlinks, so a file the open binds
// to is accepted only when the walk would have accepted its path.
func rootContains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// openInsideRoot opens path for probing, binding the probe to the file at
// open time. The walk's earlier symlink-confinement check is a walk-time
// verdict — a regular file replaced by an escaping symlink in between
// would otherwise be re-followed by the probe; the open-time check catches
// that here.
//
// A FINAL-COMPONENT SYMLINK is allowed when its resolved target stays
// inside root (the walk indexes such links; O_NOFOLLOW alone would reject
// them all); a target outside root is refused before it is probed and
// persisted. The open then runs NONBLOCKING (O_NOFOLLOW|O_NONBLOCK):
// a blocking read-open of a FIFO the writer replaced the file with would
// stall the scan worker until a reader appears that never will. The
// opened descriptor's OWN type (fstat of the fd) is what is trusted —
// a regular file, or a directory when allowDir (folder aggregation needs
// them; the walk already confined them) — so the special-file verdict
// binds to the descriptor, not to a pre-open stat a writer can replace.
// A file row must open as a regular file: a row that is a directory —
// directly, or through a symlink to one whose target the walk's Lstat
// never saw — is a kind change out from under the walk and is refused
// BEFORE the open, so it is never handed to the extractor.
func openInsideRoot(path, root string, allowDir bool) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	openPath := path
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, rerr := filepath.EvalSymlinks(path)
		if rerr != nil {
			return nil, rerr
		}
		if !rootContains(root, resolved) {
			return nil, fmt.Errorf("opened file escapes input root: %s", resolved)
		}
		if !allowDir {
			// A file row pointing at a DIRECTORY target: the walk's
			// Lstat saw the link, not the target, so a link to a
			// directory arrives here as a file row. Refuse the TARGET
			// before the open — the extractor must never enter folder
			// discovery from a file row (its unpinned child paths
			// would reach ffprobe).
			if tinfo, terr := os.Stat(resolved); terr == nil && tinfo.IsDir() {
				return nil, fmt.Errorf("opened path is not a regular file: %s", path)
			}
		}
		openPath = resolved
	}
	if !allowDir && info.IsDir() {
		// A file row that is (or became) a directory: the row kind
		// changed out from under the walk.
		return nil, fmt.Errorf("opened path is not a regular file: %s", path)
	}
	// The open is ANCHORED to the root on every platform: non-Linux has
	// no procfs, so a plain O_NOFOLLOW open of the name (which guards
	// only the final component) would follow an intermediate directory
	// swapped to an escaping file after the walk. openRootAnchored walks
	// each component relative to a verified parent descriptor (openat +
	// O_NOFOLLOW), so the parent-swap case is refused at open time; on
	// Linux it is the equivalent of the plain open the /proc re-read
	// below would verify anyway. Nonblocking where it matters (a
	// writerless FIFO at the name opens instead of blocking the
	// worker); the opened descriptor's own type is re-checked below.
	f, err := openRootAnchored(root, openPath, allowDir)
	if err != nil {
		return nil, err
	}
	// The descriptor's own type is the verdict: a pre-open lstat is a
	// race against replacement, an fstat of the opened fd is not. A
	// FIFO, socket, or device that landed between the checks is closed
	// and degraded here.
	if fi, ferr := f.Stat(); ferr != nil {
		f.Close()
		return nil, ferr
	} else if !fi.Mode().IsRegular() && !(allowDir && fi.IsDir()) {
		f.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", path)
	}
	// Final verification on the opened descriptor. On Linux this re-reads
	// the resolved target (/proc/self/fd/<fd>) so the descriptor's file
	// is verified to be the one the mode/symlink checks accepted; on
	// other Unix platforms the anchored open above IS the binding (no
	// procfs dependency off Linux).
	if target, cerr := openRootFinalCheck(f); cerr != nil {
		f.Close()
		return nil, cerr
	} else if target != "" && !rootContains(root, target) {
		f.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", target)
	}
	return f, nil
}
