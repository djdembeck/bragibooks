//go:build windows

package api

import (
	"fmt"
	"os"
)

// openInsideRoot opens path for probing. The unix O_NOFOLLOW +
// /proc/self/fd containment check is Linux-specific; on Windows the open
// plus the regular-file check is the available binding (a replaced
// symlink is still opened by its name, as the walk-time check saw it).
func openInsideRoot(path, root string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if info, serr := f.Stat(); serr != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("opened path is not a regular file: %s", path)
	}
	return f, nil
}
