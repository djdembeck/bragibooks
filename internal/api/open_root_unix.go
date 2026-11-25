//go:build !windows

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// openInsideRoot opens path for probing, binding the probe to the file at
// open time: O_NOFOLLOW refuses a final-component symlink, and the opened
// file's resolved target (/proc/self/fd/<fd>) must still lie inside root.
// The walk's earlier symlink-confinement check is a walk-time verdict — a
// regular file replaced by an escaping symlink in between would otherwise
// be re-followed by the probe; the open-time check catches that here.
func openInsideRoot(path, root string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	resolved, rerr := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	if rerr != nil {
		f.Close()
		return nil, rerr
	}
	if rel, rerr := filepath.Rel(root, resolved); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		f.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", resolved)
	}
	return f, nil
}
