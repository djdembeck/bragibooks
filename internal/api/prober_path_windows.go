//go:build windows

package api

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandle flags from winbase.h: FILE_NAME_NORMALIZED
// (0x0) normalizes the returned path, and VOLUME_NAME_DOS (0x0) selects
// the DOS device path: \\?\C:\… for a local drive, \\?\UNC\host\share…
// for a share. VOLUME_NAME_GUID (0x1) would return the \\?\Volume{GUID}\…
// form instead, which cannot be compared against a drive-letter root.
const finalPathFlags = 0x0

// rawPathByHandle resolves the final path of the file the handle refers
// to (GetFinalPathNameByHandle) in the DOS device form the syscall
// returns: \\?\C:\… for a local drive, \\?\UNC\host\share\… for a
// share. It is the handle-bound form of a pathname: a reparse point
// swapped after the open cannot repoint it. ok is false when the
// resolution fails. Shared with openInsideRoot and openPartInDir, which
// use the same resolution to PROVE containment (they refuse on failure,
// where proberPath reports ok=false and the caller degrades the row
// instead of falling back to the un-checked original pathname).
func rawPathByHandle(f *os.File) (string, bool) {
	// Two calls: the first sizes the buffer, the second fills it.
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), nil, 0, finalPathFlags)
	if err != nil || n == 0 {
		return "", false
	}
	buf := make([]uint16, n)
	written, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], n, finalPathFlags)
	if err != nil {
		return "", false
	}
	buf = buf[:written]
	// FILE_NAME_NORMALIZED returns the handle's normalized name, so the
	// result points at the same file even when casing differs from the
	// original pathname (NTFS is case-insensitive).
	return windows.UTF16ToString(buf), true
}

// normalizeFinalPath converts a GetFinalPathNameByHandle result into the
// form the probe accepts. The conversion is pure string handling (the
// syscall stays in rawPathByHandle), so the tests run it directly.
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
// swap. The caller must keep f open for the probe's duration. ok is
// false when the resolution fails: the original pathname is NOT a valid
// fallback (it was never approved for reopening by the open-time
// containment check), so the caller must degrade the row rather than send
// an un-checked pathname to the extractor. isDir is accepted for
// signature consistency with the other platforms; the handle's final
// path resolves the descriptor regardless of its type.
func proberPath(f *os.File, original string, isDir bool) (string, bool) {
	p, ok := rawPathByHandle(f)
	return normalizeFinalPath(p), ok
}
