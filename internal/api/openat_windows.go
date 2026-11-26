//go:build windows

package api

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// openPartInDir opens a folder part for probing, binding the probe to
// the file at open time. Windows has no openat in the standard library;
// the part is opened by its full path, and the binding is the handle's
// resolved NT device path (the same resolution proberPath uses), which
// a reparse-point swap after the open cannot repoint. The opened
// descriptor's OWN type (fstat of the handle) is the verdict. dir is
// the directory row's verified descriptor, accepted for signature
// consistency with the Unix implementation; the part's name is joined
// to the row's pathname, which the walk confined.
func openPartInDir(dir *os.File, name, root string) (*os.File, error) {
	full := filepath.Join(dir.Name(), name)
	h, err := windows.Open(full, windows.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	osf := os.NewFile(uintptr(h), full)
	fi, serr := osf.Stat()
	if serr != nil {
		osf.Close()
		return nil, serr
	}
	if !fi.Mode().IsRegular() {
		osf.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", name)
	}
	final, ok := finalPathByHandle(osf)
	if !ok {
		osf.Close()
		return nil, fmt.Errorf("resolve opened path for containment: %s", full)
	}
	resolvedRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		osf.Close()
		return nil, rerr
	}
	if !withinResolvedRoot(resolvedRoot, final) {
		osf.Close()
		return nil, fmt.Errorf("opened file escapes input root: %s", final)
	}
	return osf, nil
}
