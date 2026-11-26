//go:build linux

package api

import (
	"fmt"
	"os"
)

// openRootFinalCheck reports the resolved target of the opened
// descriptor via /proc/self/fd: the file the open bound to, re-read
// AFTER the open so the descriptor's file is verified to be the one the
// mode/symlink checks accepted. This is the binding on Linux; the procfs
// read is Linux-specific, which is why it lives here.
func openRootFinalCheck(f *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
}
