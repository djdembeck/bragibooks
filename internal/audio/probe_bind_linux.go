//go:build linux

package audio

// devFDName is unused on Linux: the probe path is
// /proc/<pid>/fd/<n>, which resolves the descriptor in the PARENT's
// fd table. The child (ffprobe) cannot open that path (a different
// pid), so no descriptor hand-off is needed.
const devFDName = "/dev/fd"

// devFDAvailable is always false on Linux: the probe path is
// /proc/<pid>/fd/<n>, which resolves the descriptor in the PARENT's
// fd table. The child (ffprobe) cannot open that path (a different
// pid), so the descriptor is never handed over.
var devFDAvailable = func() bool { return false }
