//go:build !windows

package audio

import (
	"os"
	"strconv"
	"strings"
)

// ppidOf reads the parent PID of pid from /proc/<pid>/stat, or 0 when the
// process is gone or its stat line is unparseable. The comm field (in
// parens) can contain spaces and parens, so the fields are anchored on the
// LAST ')' and the ppid is the second field after it (state, ppid).
func ppidOf(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+1 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// isDescendantOf reports whether pid is equal to, or a descendant of,
// ancestor, by walking the /proc ppid chain. The walk is bounded so a
// pathological (or self-referential) chain cannot loop forever; a chain
// that ends at init (pid <= 1) without reaching ancestor is not a
// descendant.
func isDescendantOf(pid, ancestor int) bool {
	if pid == ancestor {
		return true
	}
	cur := pid
	for range 64 {
		if cur <= 1 {
			return false
		}
		parent := ppidOf(cur)
		if parent == 0 {
			return false // process gone: cannot prove descent
		}
		if parent == ancestor {
			return true
		}
		cur = parent
	}
	return false
}

// verifyEnginePID returns the engine-reported PID only when it is the
// spawned child or a verified descendant of it (via the /proc ppid chain);
// otherwise it falls back to the known child PID. The reported PID
// authorizes staging cleanup, so a PID that cannot be proven to belong to
// this run's tree must not delete siblings it does not own. A PID whose
// process is already gone is unverifiable and falls back (conservative).
func verifyEnginePID(childPID, reportedPID int) int {
	if reportedPID <= 0 {
		return childPID
	}
	if isDescendantOf(reportedPID, childPID) {
		return reportedPID
	}
	return childPID
}
