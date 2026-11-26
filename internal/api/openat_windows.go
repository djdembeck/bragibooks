//go:build windows

package api

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// openPartInDir opens a folder part for probing, binding the probe to
// the file at open time. Windows has no openat in the standard library,
// so the part is opened by its full path. The part is opened relative to
// the verified directory's RESOLVED location, not the row's original
// name. dir is the directory descriptor openInsideRoot verified, and its
// handle's resolved NT device path (the same resolution proberPath uses)
// is the base the part name joins onto. Joining to the row's pathname
// would follow a directory swap (rename, junction) that landed after the
// walk; resolving the handle cannot be repointed by a swap after the
// open. If the directory handle cannot be resolved, the part is refused:
// the row's pathname is a name-based fallback that was never verified at
// open time. The opened descriptor's OWN type (fstat of the handle) is
// the verdict, and the part's resolved final path must stay inside the
// input root.
func openPartInDir(dir *os.File, name, root string) (*os.File, error) {
	dirPath, ok := finalPathByHandle(dir)
	if !ok {
		return nil, fmt.Errorf("resolve directory path for part open: %s", dir.Name())
	}
	full := filepath.Join(normalizeDrivePath(dirPath), name)
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
	final = normalizeDrivePath(final)
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
