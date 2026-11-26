//go:build !windows && !linux

package api

import "os"

// openRootFinalCheck has no procfs on this platform: the O_NOFOLLOW open
// of the verified path (see openInsideRoot) is the binding, so the
// descriptor carries no additional target to check. A no-op here keeps
// non-Linux Unix hosts on the same walk-time + open-time checks as
// Linux without a /proc dependency (an ordinary opened file must not
// degrade on macOS).
func openRootFinalCheck(f *os.File) (string, error) { return "", nil }
