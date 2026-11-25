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

// finalPathByHandle resolves the normalized final path of the file the
// handle refers to (GetFinalPathNameByHandle). It is the handle-bound
// form of a pathname: a reparse point swapped after the open cannot
// repoint it. The \\?\UNC\ host form is converted back to the
// \\host\share form that ffprobe accepts. ok is false when the
// resolution fails. Shared with openInsideRoot, which uses the same
// resolution to PROVE containment (it refuses on failure, where
// proberPath falls back).
func finalPathByHandle(f *os.File) (string, bool) {
	// Two calls: the first sizes the buffer, the second fills it.
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), nil, 0, fileNameNormalized)
	if err != nil || n == 0 {
		return "", false
	}
	buf := make([]uint16, n)
	written, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], n, fileNameNormalized)
	if err != nil {
		return "", false
	}
	buf = buf[:written]
	// FILE_NAME_NORMALIZED strips the handle's file name, so the result
	// points at the same file even when casing differs from the original
	// pathname (NTFS is case-insensitive).
	s := windows.UTF16ToString(buf)
	return normalizeFinalPath(s), true
}

// normalizeFinalPath converts a GetFinalPathNameByHandle result into the
// form the containment check and the probe accept. The conversion is pure
// string handling (the syscall stays in finalPathByHandle), so the tests
// run it directly.
func normalizeFinalPath(s string) string {
	if strings.HasPrefix(s, `\\?\UNC\`) {
		// Slice past the ENTIRE `\\?\UNC\` prefix (all 8 chars), not just
		// `\\?\UNC`: keeping the trailing backslash would leave THREE
		// leading backslashes (\\ + \server\share\…), and the containment
		// check in openInsideRoot compares against a root with exactly
		// two, so every handle on a UNC share would read as outside.
		s = `\\` + s[len(`\\?\UNC\`):]
	}
	return s
}

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
	if s, ok := finalPathByHandle(f); ok {
		return s
	}
	return original
}
