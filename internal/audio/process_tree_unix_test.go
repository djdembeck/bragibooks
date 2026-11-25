//go:build !windows

package audio

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnGroupedHelper starts a long-lived process in its OWN process group
// (so the negative pid -P is a live, signalable group) and waits until it
// is in /proc. Returns the command (to inspect/wait on) and the pid.
func spawnGroupedHelper(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	helper := exec.Command("sleep", "30")
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	for range 50 {
		if _, err := os.Stat("/proc/" + strconv.Itoa(helper.Process.Pid)); err == nil {
			return helper, helper.Process.Pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper pid %d never appeared in /proc", helper.Process.Pid)
	return helper, 0
}

// helperAlive reports whether the process is still RUNNING: an unreaped
// child of the test process stays in /proc as a zombie (state "Z") after
// it dies, and a zombie is dead for signal purposes.
func helperAlive(t *testing.T, pid int) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(string(data), ')')
	return i >= 0 && i+1 < len(data) && data[i+1] == ' ' && data[i+2] != 'Z'
}

// TestProcessTreeKillRecycledPgidIsNotSignaled guards the identity check in
// posixProcessTree.kill: a kill target whose /proc start time DIFFERS from
// the recorded child start time is a recycled pgid (or not this child at
// all) and must be reported as already done WITHOUT being signaled. The
// helper is a live process in its own (signalable) group, so a misfire is
// observable: the signal would kill it.
func TestProcessTreeKillRecycledPgidIsNotSignaled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("recycled-pgid identity is verified via /proc, linux only")
	}
	helper, pid := spawnGroupedHelper(t)
	currentStart := procStartTime(pid)
	if currentStart == "" {
		t.Fatalf("helper pid %d has no readable /proc start time", pid)
	}
	// A recorded baseline that provably differs from the kill target's
	// start time: the target is NOT this run's child.
	tree := &posixProcessTree{
		cmd:        &exec.Cmd{Process: helper.Process},
		childStart: currentStart + "-recycled",
	}
	err := tree.kill()
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill = %v; want os.ErrProcessDone for a kill target with a mismatched start time", err)
	}
	// No signal was delivered: the (live, grouped) helper survived.
	if !helperAlive(t, pid) {
		t.Fatal("kill target with a mismatched start time was signaled (helper is gone)")
	}
}

// TestProcessTreeKillMatchingPgidStillSignaled is the negative control:
// when the recorded baseline MATCHES the kill target's start time, the
// identity check passes and the group IS signaled (the harness's signal
// path works, so the non-signal above is the check, not a dead path).
func TestProcessTreeKillMatchingPgidStillSignaled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("recycled-pgid identity is verified via /proc, linux only")
	}
	helper, pid := spawnGroupedHelper(t)
	tree := &posixProcessTree{
		cmd:        &exec.Cmd{Process: helper.Process},
		childStart: procStartTime(pid),
	}
	if err := tree.kill(); err != nil {
		t.Fatalf("kill = %v; want the group signaled for a matching start time", err)
	}
	// SIGKILL is asynchronous; allow it to land.
	for range 50 {
		if !helperAlive(t, pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("kill target with a matching start time was not signaled (helper is alive)")
}
