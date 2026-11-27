//go:build linux

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// openRootFinalCheck reports the resolved target of the opened
// descriptor via /proc/self/fd: the file the open bound to, re-read
// AFTER the open so the descriptor's file is verified to be the one the
// mode/symlink checks accepted. This is the binding on Linux; the procfs
// read is Linux-specific, which is why it lives here.
func openRootFinalCheck(f *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
}

// openRootAnchored opens path for reading. On Linux the /proc/self/fd
// re-read in openRootFinalCheck is already the binding (it verifies the
// descriptor's resolved target against the root after the open), so a
// plain O_NOFOLLOW|O_NONBLOCK open of the verified path is sufficient —
// the per-component anchored walk the other-Unix build uses (no procfs)
// is not needed here. Nonblocking: a writerless FIFO at the name opens
// instead of blocking the worker.
func openRootAnchored(root, path string, allowDir bool) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// openIndexDir opens dir for the index walk's chunked ReadDir.
// O_DIRECTORY|O_NOFOLLOW: the entry the walk just observed as a directory
// is re-opened by NAME, so a symlink swapped in between is refused
// (the walk never follows directory symlinks), and O_DIRECTORY fails on
// non-directories instead of opening them. O_NONBLOCK: a FIFO swapped in
// would otherwise block the single scan worker in the open itself —
// cancelling the walk's context does not interrupt a blocking open, so
// the scan (and shutdown with it) would stall.
//
// O_NOFOLLOW protects only the FINAL component of the pathname: a
// filesystem writer that replaces a writable ANCESTOR of the root with a
// symlink between resolveScanRoot and this open would redirect the walk's
// base to an outside tree, and every later openat would then faithfully
// stay under the wrong directory. The /proc/self/fd re-read
// (openRootFinalCheck) verifies the descriptor's resolved target stays
// inside root (verifyIndexDirRoot, fail-closed) before the enumeration.
func openIndexDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := verifyIndexDirRoot(f, dir); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// verifyIndexDirRoot binds an opened walk directory to its resolved
// target: the /proc/self/fd re-read (openRootFinalCheck) reports the
// file the descriptor bound to, and rootContains verifies it stays
// inside dir — the same check openInsideRoot runs on probe handles.
// Fail-closed: a readlink failure refuses the enumeration (the walk's
// root must be PROVEN to be the configured root, not assumed from the
// pathname).
func verifyIndexDirRoot(f *os.File, dir string) error {
	target, cerr := openRootFinalCheck(f)
	if cerr != nil {
		return cerr
	}
	if !rootContains(dir, target) {
		return fmt.Errorf("opened index directory escapes input root: %s", target)
	}
	return nil
}

// openIndexChildDir opens the child directory name against the ALREADY-
// OPEN parent descriptor (openat, O_NOFOLLOW): the walk reads the
// parent's entries from its own descriptor, so a child is opened relative
// to that same directory — an ancestor that a writer replaces with a
// symlink between the entry read and the open cannot redirect the walk
// outside the root the way a full-path re-open by name can. Same flags as
// openIndexDir (directory-only, no-follow, nonblocking). The returned file
// is named with the child's FULL path (the parent's own name + the
// entry): os.ReadDir stores each entry's path from that name, and the
// walk's d.Info() change signals resolve against it — a bare name would
// stat against the process cwd and yield nil (zero signals) for every
// entry the child holds.
func openIndexChildDir(parent *os.File, name, root string) (*os.File, error) {
	fd, err := syscall.Openat(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}
