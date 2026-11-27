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

// openIndexDir opens dir for the index walk's chunked ReadDir.
// O_DIRECTORY|O_NOFOLLOW: the entry the walk just observed as a directory
// is re-opened by NAME, so a symlink swapped in between is refused
// (the walk never follows directory symlinks), and O_DIRECTORY fails on
// non-directories instead of opening them. O_NONBLOCK: a FIFO swapped in
// would otherwise block the single scan worker in the open itself —
// cancelling the walk's context does not interrupt a blocking open, so
// the scan (and shutdown with it) would stall.
func openIndexDir(dir string) (*os.File, error) {
	return os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
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
func openIndexChildDir(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

// openRootAnchoredRetryBound caps the ELOOP re-open loop: each attempt
// resolves a final-component symlink to its (symlink-free) target, so a
// second attempt must open a real file — more retries mean the path is
// oscillating under the walk and the open is refused.
const openRootAnchoredRetryBound = 3

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
// The root itself is opened with O_NOFOLLOW: the walk must anchor to the
// INTENDED root directory, not a symlink a filesystem writer swapped into
// the name (a followed root would re-base every later openat on a
// different directory, and the non-Linux openRootFinalCheck has no
// descriptor target to verify against). A root that IS a symlink is
// resolved first — the caller passes the resolved form, so this is
// defensive — and the walk then anchors to the resolved directory.
//
// A FINAL-component symlink is admitted (the walk indexes in-root file
// symlinks): on ELOOP from the FINAL component the link's target is
// resolved and must stay inside root before the resolved (symlink-free)
// target is re-opened anchored. The re-open is a bounded loop
// (openRootAnchoredRetryBound), not an unbounded recursion: every attempt's
// descriptors are closed before the next starts, so a path that oscillates
// under the walk leaks neither fds nor stack frames.
//
// An ELOOP from an INTERMEDIATE component is NOT retried: re-resolving the
// whole path would open the target of an ancestor a writer swapped to a
// symlink, while the walk's metadata is stored under the original name —
// the open would bind to a different file than the row describes. The
// intermediate openat already walked every component against a verified
// parent fd, so an ELOOP there means an ancestor is a symlink loop the
// walk did not see; fail closed.
func openRootAnchored(root, openPath string, allowDir bool) (*os.File, error) {
	// A symlinked root resolves to the directory the walk is meant to
	// anchor on; the O_NOFOLLOW open below then binds to that directory,
	// not to the name a writer can re-point.
	resolvedRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = r
	}
	for attempt := 0; attempt <= openRootAnchoredRetryBound; attempt++ {
		f, err, finalLoop := openRootAnchoredAttempt(resolvedRoot, openPath, allowDir)
		if err != unix.ELOOP {
			return f, err
		}
		if !finalLoop {
			// An intermediate ancestor, not the final component, hit the
			// loop: fail closed (see the contract above).
			return nil, err
		}
		// Final component is a symlink (a swap since the caller's lstat,
		// which pre-resolves links it sees). The walk admits in-root file
		// symlinks: resolve the target, verify it stays inside root, and
		// re-open the resolved (symlink-free) target anchored. The attempt
		// above closed every fd it opened, so the next iteration starts
		// from a clean state.
		resolved, e2 := filepath.EvalSymlinks(openPath)
		if e2 != nil {
			return nil, e2
		}
		if !rootContains(resolvedRoot, resolved) {
			return nil, fmt.Errorf("opened file escapes input root: %s", resolved)
		}
		if !allowDir {
			if ti, terr := os.Stat(resolved); terr == nil && ti.IsDir() {
				return nil, fmt.Errorf("opened path is not a regular file: %s", openPath)
			}
		}
		openPath = resolved
	}
	return nil, fmt.Errorf("open of %s loops through symlinks", openPath)
}

// openRootAnchoredAttempt runs ONE anchored open of openPath relative to
// root: it opens the root descriptor, walks the intermediate components
// against their parent fds, and opens the base. On unix.ELOOP it returns
// (nil, unix.ELOOP, finalLoop) for the caller's bounded retry loop, with
// finalLoop true ONLY when the FINAL component's openat looped (an
// in-place symlink the walk admits); an ELOOP from an intermediate
// component (an ancestor the O_NOFOLLOW walk saw as a directory) comes
// back with finalLoop false and the caller fails closed. Every other
// outcome is terminal. All descriptors this attempt opened are closed
// before it returns.
func openRootAnchoredAttempt(root, openPath string, allowDir bool) (*os.File, error, bool) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err, false
	}
	rel, rerr := filepath.Rel(root, openPath)
	if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		unix.Close(rootFD)
		return nil, fmt.Errorf("opened path escapes input root: %s", openPath), false
	}
	comps := strings.Split(filepath.ToSlash(rel), "/")
	dirFD := rootFD
	// Close every fd this attempt opened, exactly once, on every exit.
	// The success path closes them itself and hands the base fd to the
	// returned *os.File, so it marks done and the defer becomes a no-op.
	done := false
	defer func() {
		if done {
			return
		}
		if dirFD != rootFD {
			unix.Close(dirFD)
		}
		unix.Close(rootFD)
	}()
	// Intermediate components: each must be a real directory resolved
	// against the previous fd (O_NOFOLLOW refuses a symlink in place).
	// An ELOOP here is an ANCESTOR the walk saw as a plain directory:
	// finalLoop stays false so the caller fails closed instead of
	// re-resolving the whole path to a swapped ancestor's target.
	for _, comp := range comps[:len(comps)-1] {
		fd, err := unix.Openat(dirFD, comp, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err, false
		}
		st, serr := unix.Fstat(fd)
		if serr != nil {
			unix.Close(fd)
			return nil, serr, false
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			return nil, fmt.Errorf("opened path is not a regular file: %s", openPath), false
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
	if err != nil {
		// The base's openat is the final component: an ELOOP there is
		// the in-place symlink the walk admits (finalLoop true).
		return nil, err, err == unix.ELOOP
	}
	if dirFD != rootFD {
		unix.Close(dirFD)
	}
	unix.Close(rootFD)
	done = true // the base fd is owned by the returned *os.File
	return os.NewFile(uintptr(fd), openPath), nil, false
}
