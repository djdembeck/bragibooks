//go:build !windows

package api

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openPartInDir opens a folder part RELATIVE to the verified directory
// descriptor dir, binding the part to the file at open time. The parent
// is anchored to the directory fd, so a parent-directory swap cannot
// redirect the open the way a full-path open by name can; the O_NOFOLLOW
// flag refuses a final-component symlink. The open is nonblocking
// (a writerless FIFO at the name would stall the worker until a reader
// appears that never will), and the opened descriptor's OWN type
// (fstat of the fd) is the verdict, so a special-file swap lands on the
// descriptor, not a pre-open stat. The fd is created close-on-exec: when
// a probe subprocess execs, the fd closes automatically, so it is not
// passed to the ffprobe process. dir must be a directory row's
// verified descriptor (allowDir in openInsideRoot).
func openPartInDir(dir *os.File, name, root string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	fi, serr := f.Stat()
	if serr != nil {
		f.Close()
		return nil, serr
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", name)
	}
	// On Linux this re-reads the resolved target (/proc/self/fd/<fd>) so
	// the descriptor's file is verified to be the one the type check
	// accepted and to stay inside the input root; on other Unix platforms
	// there is no procfs and it returns "", and the O_NOFOLLOW openat
	// against the verified directory fd IS the binding.
	if target, cerr := openRootFinalCheck(f); cerr != nil {
		f.Close()
		return nil, cerr
	} else if target != "" && !rootContains(root, target) {
		f.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", target)
	}
	return f, nil
}
