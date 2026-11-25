//go:build !windows && !linux

package api

import "os"

// proberPath returns the pathname to hand to the probe for f: the
// ORIGINAL pathname, but only once it is verified at probe time to be a
// regular file naming the same filesystem object as the open descriptor.
// The open-time containment check binds the descriptor, not the name: the
// extractor reopens the name for the ffprobe probes and folder discovery,
// so a pathname swapped after that check would be probed and persisted as
// the source.
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
// Any failed step (the lstat, the regular-file check, the comparison)
// returns ok=false. The callers (extractOne, pinPart) take the refusal
// path and degrade the row/part. The remaining micro-window between the
// lstat and the consumer's open is a documented platform limitation.
func proberPath(f *os.File, original string) (string, bool) {
	finfo, err := f.Stat()
	if err != nil {
		return "", false
	}
	oinfo, err := os.Lstat(original)
	if err != nil {
		return "", false
	}
	if !oinfo.Mode().IsRegular() {
		// A symlink, device, FIFO, or socket. O_NOFOLLOW at open time
		// rejects only the final-component symlink; a same-inode symlink
		// target would pass a stat-based identity check.
		return "", false
	}
	if !os.SameFile(finfo, oinfo) {
		return "", false
	}
	return original, true
}
