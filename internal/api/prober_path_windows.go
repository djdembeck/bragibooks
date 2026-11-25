//go:build windows

package api

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// FILE_NAME_NORMALIZED (0x1) from winbase.h: resolve to the normalized
// NT path of the file the handle refers to.
const fileNameNormalized = 0x1

// proberPath returns the pathname to hand to the probe for f: the file's
// NT device path (\Device\HarddiskVolumeN\… or \\?\C:\…), resolved from
// the open handle via GetFinalPathNameByHandle. Unlike the original
// pathname, it is bound to the file the open inside openInsideRoot
// verified: a replaced symlink (or any rename) after the check cannot
// repoint the probe, because the device path was resolved before the
// swap. The caller must keep f open for the probe's duration. If the
// resolution fails, the original pathname is used and the open-time
// regular-file check is the only binding, as before.
func proberPath(f *os.File, original string) string {
	// Two calls: the first sizes the buffer, the second fills it.
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), nil, 0, fileNameNormalized)
	if err != nil || n == 0 {
		return original
	}
	buf := make([]uint16, n)
	written, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], n, fileNameNormalized)
	if err != nil {
		return original
	}
	buf = buf[:written]
	// FILE_NAME_NORMALIZED strips the handle's file name, so the result
	// points at the same file even when casing differs from `original`
	// (NTFS is case-insensitive). A \\?\UNC\ host path is converted back
	// to the \\host\share form that ffprobe accepts.
	s := windows.UTF16ToString(buf)
	if strings.HasPrefix(s, `\\?\UNC\`) {
		s = `\\` + s[len(`\\?\UNC`):]
	}
	return s
}
