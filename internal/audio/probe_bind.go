package audio

import (
	"os"
	"strconv"
)

// probeChildFD is the descriptor number the first entry of
// cmd.ExtraFiles occupies in the child process (after stdin, stdout,
// and stderr).
const probeChildFD = 3

// bindProbe returns the path ffprobe must read for the verified
// descriptor f and the file to hand to the child via cmd.ExtraFiles.
// A nil f means no descriptor to bind: the path is returned as-is and
// the child opens it itself, so a standalone probe reads by name on
// every platform.
//
// The /dev/fd/3 hand-off is darwin/BSD-only; devFDAvailable gates it.
// There a verified descriptor is always probed through /dev/fd/3: the
// child is handed f (fd 3) and reads the inherited descriptor, so the
// original name is never reopened. The identity check at pin time
// (lstat + SameFile) decides what gets handed over, and a swap of the
// original name cannot repoint the child's open of /dev/fd/3. The
// hand-off is fail-closed: if the child cannot resolve /dev/fd/3 (no
// fdescfs), ffprobe fails to open it, the probe errors, and the row
// degrades. A verified descriptor never falls back to the mutable
// pathname.
//
// On Linux and Windows devFDAvailable is false: the probe path already
// names the descriptor (/proc/<pid>/fd/<n> in the parent's fd table on
// Linux; the handle's NT device path on Windows), and the child cannot
// reopen a parent /proc fd or NT handle. bindProbe returns the path
// as-is and hands off nothing.
func bindProbe(path string, f *os.File) (string, *os.File) {
	if f == nil || !devFDAvailable() {
		return path, nil
	}
	return devFDName + "/" + strconv.Itoa(probeChildFD), f
}
