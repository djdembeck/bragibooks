//go:build !windows

package api

import (
	"fmt"
	"os"
)

// proberPath returns the pathname to hand to the probe for f: the
// descriptor's /proc/<pid>/fd/<n> path (pid is the prober's own, so
// the path is resolvable by the probe's child process), which resolves
// to the file the open bound (the verified target inside the input
// root), not to the original pathname that a symlink swap after the
// check could repoint. The caller must keep f open for the probe's
// duration: a closed fd would make the path resolve to nothing and the
// probe would degrade.
func proberPath(f *os.File, original string) string {
	return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd())
}
