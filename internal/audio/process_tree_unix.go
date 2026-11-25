//go:build !windows

package audio

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// processTree lets cancellation kill the whole spawned tree, not just the
// direct child: m4b-merge may be a script wrapper that spawns children,
// and a grandchild that inherits the stdout pipe would otherwise hold it
// open long after the direct child is killed, stalling the run past its
// deadline.
type processTree interface {
	// start confines the RUNNING child to the tree. The caller must run
	// it immediately after cmd.Start has succeeded, so every descendant
	// the child spawns joins the tree from the moment the process exists:
	// a cancel-time assignment would only confine processes born after
	// the cancel, leaving earlier grandchildren (and their inherited
	// pipes) alive.
	start(cmd *exec.Cmd) error
	// kill terminates the whole tree. os.ErrProcessDone means the tree is
	// already gone: nothing for os/exec to do.
	kill() error
	// finish releases the kill target; call after cmd.Wait has reaped the
	// child.
	finish()
}

// posixProcessTree puts the child in its own process group and kills the
// group. On unix the group is set by the start attribute, so confining the
// child is complete at cmd.Start and start records the child's identity
// (the unix twin of the Windows suspended-start contract: the tree is
// established the moment the process exists).
type posixProcessTree struct {
	cmd      *exec.Cmd
	finished atomic.Bool
	// childStart is the child's /proc start time (field 22), captured
	// after Start, before the child can be reaped. kill verifies the
	// process at the kill target is STILL this child: a cancel landing
	// after cmd.Wait has reaped the child (before finish ran) would
	// otherwise SIGKILL a RECYCLED pgid that happens to exist.
	childStart string
}

// newProcessTree must run before cmd.Start: the process-group attribute
// only applies to a not-yet-started command.
func newProcessTree(cmd *exec.Cmd) (processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &posixProcessTree{cmd: cmd}, nil
}

func (p *posixProcessTree) start(cmd *exec.Cmd) error {
	p.childStart = procStartTime(cmd.Process.Pid)
	return nil
}

// procStartTime reads /proc/<pid>/stat field 22 (starttime, in clock
// ticks since boot). "" when /proc is absent (macOS), the process is
// already gone, or its stat line is unparseable: on such hosts the
// identity check below degrades to the pre-check behavior (attempt the
// kill; ESRCH means the group is gone), while on Linux the recorded and
// read times still guard against signaling a recycled pgid.
func procStartTime(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(data)
	// The comm field (in parens) can contain spaces and parens: anchor
	// on the LAST ')' and count fields after it.
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+1 >= len(s) {
		return ""
	}
	fields := strings.Fields(s[i+1:])
	// Fields after ')': state(1), ppid(2), ..., starttime(20).
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// kill terminates the whole tree. A cancel after cmd.Wait has reaped the
// child (finish already ran) is a no-op: the group is gone, so a late
// Kill on the recycled pgid would misfire on an unrelated group. A
// cancel in the narrow window BEFORE finish ran is equally safe on hosts
// with /proc: the kill target's start time is verified against the
// recorded one, and a recycled pgid carrying a DIFFERENT non-empty start
// time is treated as already done. When EITHER start time is unreadable
// (no /proc, as on macOS — the recorded baseline would be empty too, and
// a vanished process also reads empty), identity cannot be verified in
// either direction, so the check falls back to attempting the kill and
// reading ESRCH as "already gone", the pre-check behavior.
// os.ErrProcessDone means the tree is already gone: nothing for os/exec
// to do.
func (p *posixProcessTree) kill() error {
	if p.finished.Load() {
		return os.ErrProcessDone
	}
	if pid := p.cmd.Process.Pid; pid > 0 {
		if st := procStartTime(pid); st != "" && p.childStart != "" && st != p.childStart {
			// The process at the kill target is not (or no longer)
			// this child: signaling it could hit a recycled group.
			// An empty current start time (process gone) or an empty
			// recorded baseline (unreadable at start) both fall through
			// to the kill attempt below, where ESRCH reads as "gone".
			return os.ErrProcessDone
		}
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func (p *posixProcessTree) finish() {
	p.finished.Store(true)
}

var _ processTree = (*posixProcessTree)(nil)
