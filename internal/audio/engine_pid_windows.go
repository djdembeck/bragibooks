//go:build windows

package audio

// verifyEnginePID falls back to the known child PID on Windows: the /proc
// ppid chain is Linux-specific, so a reported PID is not ownership-verified
// there and must not authorize staging cleanup on its own.
func verifyEnginePID(childPID, reportedPID int) int {
	return childPID
}
