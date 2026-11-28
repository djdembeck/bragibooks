//go:build !windows && !linux

package api

import "os"

// proberPath returns the pathname to hand to the probe for f: the
// ORIGINAL pathname, but only once it is verified at probe time to name
// the same filesystem object as the open descriptor, with a type that
// matches the row's kind: a regular file for a file row, a directory
// for a folder row. The open-time containment check binds the
// descriptor, not the name: the extractor reopens the name for the
// ffprobe probes and folder discovery, so a pathname swapped after that
// check would be probed and persisted as the source.
//
// This platform has no /proc, so the OS cannot keep the name bound until
// the consumer opens it. The binding is an identity check at probe time:
// os.Lstat of the pathname, compared with the fd via os.SameFile (st_dev
// + st_ino on unix). Lstat refuses a final-component symlink outright,
// which O_NOFOLLOW at open time misses when the symlink's target shares an
// inode. A parent directory swapped for a symlink to an outside directory
// also fails, because the pathname then resolves to a different-inode
// file. A hardlink of the SAME inode keeps the name resolving to the
// verified inode, so it stays in force.
//
// Any failed step (the lstat, the type check, the comparison) returns
// ok=false. The callers (extractOne, pinPart) take the refusal path and
// degrade the row/part. The remaining micro-window between the lstat and
// the consumer's open is a documented platform limitation.
func proberPath(f *os.File, original string, isDir bool) (string, bool) {
	finfo, err := f.Stat()
	if err != nil {
		return "", false
	}
	oinfo, err := os.Lstat(original)
	if err != nil {
		return "", false
	}
	if isDir {
		// A directory row: a symlink to a directory is not followed by the
		// walk (it is a file-symlink row), so a final-component symlink here
		// is refused; only a real directory is accepted.
		if !oinfo.IsDir() {
			return "", false
		}
	} else if oinfo.Mode()&os.ModeSymlink != 0 {
		// A file row that is a SYMLINK: the open followed it (O_NOFOLLOW
		// pins only the final component at open, and openInsideRoot
		// resolves the link to its regular-file target), so f is bound to
		// the TARGET. Lstat sees the link itself (not a regular file) and
		// would refuse the row before ffprobe sees the verified descriptor —
		// degrading an ordinary in-root audiobook link that indexes fine on
		// Linux. Accept it, and bind by the TARGET's identity (os.Stat
		// follows the link), which is what the descriptor and the probe
		// both see.
		tinfo, terr := os.Stat(original)
		if terr != nil {
			return "", false
		}
		if !tinfo.Mode().IsRegular() {
			return "", false
		}
		if !os.SameFile(finfo, tinfo) {
			return "", false
		}
		return original, true
	} else if !oinfo.Mode().IsRegular() {
		// A device, FIFO, or socket is refused: O_NOFOLLOW at open time
		// rejects only the final-component symlink; a same-inode symlink
		// target would pass a stat-based identity check.
		return "", false
	}
	if !os.SameFile(finfo, oinfo) {
		return "", false
	}
	return original, true
}
