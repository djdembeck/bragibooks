//go:build windows

package audio

// devFDName is unused on Windows: the probe path is the handle's NT
// device path, resolved before any swap can repoint it. ffprobe opens
// it by name, so no descriptor hand-off is needed.
const devFDName = "/dev/fd"

// devFDAvailable is always false on Windows: the probe path is the
// handle's NT device path, resolved before any swap can repoint it.
// ffprobe opens it by name, so no descriptor hand-off is needed.
var devFDAvailable = func() bool { return false }
