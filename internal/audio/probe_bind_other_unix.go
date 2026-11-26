//go:build !windows && !linux

package audio

// devFDName is the child-side name of an inherited descriptor on
// macOS and most BSDs: /dev/fd resolves a child's own fd table, so
// ffprobe's open of /dev/fd/3 hits the descriptor the parent handed
// over via cmd.ExtraFiles, not a name lookup.
const devFDName = "/dev/fd"

// devFDAvailable is always true on macOS and the BSDs: a verified
// descriptor is always probed through /dev/fd/3 (bindProbe hands the
// descriptor to the child and the child reads the inherited fd 3).
// The hand-off is fail-closed: if the child cannot resolve /dev/fd/3
// (no fdescfs), ffprobe fails to open it and the probe errors,
// degrading the row. A verified descriptor must not silently fall
// back to the mutable pathname.
var devFDAvailable = func() bool { return true }
