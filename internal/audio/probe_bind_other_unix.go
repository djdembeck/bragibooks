//go:build !windows && !linux

package audio

import (
	"io"
	"os"
	"os/exec"
	"sync"
)

// devFDName is the child-side name of an inherited descriptor on
// macOS and most BSDs: /dev/fd resolves a child's own fd table, so
// ffprobe's open of /dev/fd/3 hits the descriptor the parent handed
// over via cmd.ExtraFiles, not a name lookup.
const devFDName = "/dev/fd"

// devFDAvailable reports whether a child can read a nonstandard
// inherited descriptor through /dev/fd. The probe runs once and is
// cached. A variable so a test can force the fallback branch.
var devFDAvailable = sync.OnceValue(probeDevFD)

// probeDevFD verifies the hand-off end to end. It opens a temp file,
// hands it to a child as fd 3 via cmd.ExtraFiles, and runs
// /bin/cat /dev/fd/3 in that child. A bare Lstat of /dev/fd misses
// the FreeBSD case where the directory exists but fdescfs is
// unmounted, so only descriptors 0-2 resolve. Any probe failure (no
// exec, nonzero exit, no /dev/fd) leaves the hand-off disabled, and
// the probe falls back to the pathname.
func probeDevFD() bool {
	tmp, err := os.CreateTemp("", "audio-probe-fd-*")
	if err != nil {
		return false
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	cmd := exec.Command("/bin/sh", "-c", "/bin/cat /dev/fd/3 > /dev/null")
	cmd.ExtraFiles = []*os.File{tmp}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}
