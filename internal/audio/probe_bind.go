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
// descriptor f (f nil: no descriptor to bind, probe by name) and the
// file to hand to the child via cmd.ExtraFiles (nil: the child opens
// the path itself). Where devFDAvailable() is true (macOS/BSD, where
// /dev/fd resolves a child's own fd table), the child is handed f
// (fd 3) and probes /dev/fd/3: the descriptor's identity check
// (lstat + SameFile at pin time) is the gate for what gets handed
// over, and the child's open of /dev/fd/3 cannot be repointed by a
// swap of the original name. Where it is false (Linux, where the
// probe path is /proc/<pid>/fd/<n> in the PARENT's fd table, and
// hosts without /dev/fd), the child probes the verified pathname; the
// residual window between the pin's lstat and the child's open is the
// documented platform limitation.
func bindProbe(path string, f *os.File) (string, *os.File) {
	if f == nil || !devFDAvailable() {
		return path, nil
	}
	return devFDName + "/" + strconv.Itoa(probeChildFD), f
}
