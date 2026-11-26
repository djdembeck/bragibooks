//go:build !windows && !linux

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openRootFinalCheck has no procfs on this platform: the O_NOFOLLOW open
// of the verified path (see openInsideRoot) is the binding, so the
// descriptor carries no additional target to check. A no-op here keeps
// non-Linux Unix hosts on the same walk-time + open-time checks as
// Linux without a /proc dependency (an ordinary opened file must not
// degrade on macOS).
func openRootFinalCheck(f *os.File) (string, error) { return "", nil }

// openRootAnchored opens openPath for reading, ANCHORED to root: every
// path component is resolved relative to a verified directory descriptor
// (openat + O_NOFOLLOW), starting from the root's own descriptor. On Linux
// the /proc/self/fd re-read in openRootFinalCheck is the binding; here
// there is no procfs, so this anchored walk IS the binding — an
// intermediate directory swapped to an escaping (or special) file after
// the walk cannot redirect the open the way a full-path open by name can,
// because each component is re-resolved against a parent fd the walk
// already verified, and O_NOFOLLOW refuses a symlink at every level.
//
// A FINAL-component symlink is admitted (the walk indexes in-root file
// symlinks): on ELOOP the link's target is resolved and must stay inside
// root before the resolved (symlink-free) target is re-opened anchored.
func openRootAnchored(root, openPath string, allowDir bool) (*os.File, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	rel, rerr := filepath.Rel(root, openPath)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		unix.Close(rootFD)
		return nil, fmt.Errorf("opened path escapes input root: %s", openPath)
	}
	comps := strings.Split(filepath.ToSlash(rel), "/")
	dirFD := rootFD
	defer func() {
		if dirFD != rootFD {
			unix.Close(dirFD)
		}
		unix.Close(rootFD)
	}()
	// Intermediate components: each must be a real directory resolved
	// against the previous fd (O_NOFOLLOW refuses a symlink in place).
	for _, comp := range comps[:len(comps)-1] {
		fd, err := unix.Openat(dirFD, comp, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		st, serr := unix.Fstat(fd)
		if serr != nil {
			unix.Close(fd)
			return nil, serr
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			return nil, fmt.Errorf("opened path is not a regular file: %s", openPath)
		}
		if dirFD != rootFD {
			unix.Close(dirFD)
		}
		dirFD = fd
	}
	base := comps[len(comps)-1]
	var fd int
	if allowDir {
		fd, err = unix.Openat(dirFD, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	} else {
		// Nonblocking: a writerless FIFO at this name opens instead of
		// blocking the worker.
		fd, err = unix.Openat(dirFD, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	}
	if err == unix.ELOOP {
		// Final component is a symlink (a swap since the caller's lstat,
		// which pre-resolves links it sees). The walk admits in-root file
		// symlinks: resolve the target, verify it stays inside root, and
		// re-open the resolved (symlink-free) target anchored.
		resolved, e2 := filepath.EvalSymlinks(openPath)
		if e2 != nil {
			return nil, e2
		}
		if !rootContains(root, resolved) {
			return nil, fmt.Errorf("opened file escapes input root: %s", resolved)
		}
		if !allowDir {
			if ti, terr := os.Stat(resolved); terr == nil && ti.IsDir() {
				return nil, fmt.Errorf("opened path is not a regular file: %s", openPath)
			}
		}
		return openRootAnchored(root, resolved, allowDir)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), openPath), nil
}
