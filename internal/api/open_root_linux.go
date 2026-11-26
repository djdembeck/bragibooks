//go:build linux

package api

import (
	"fmt"
	"os"
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
