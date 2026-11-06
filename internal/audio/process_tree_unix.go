//go:build !windows

package audio

import (
	"os"
	"os/exec"
	"syscall"
)

// processTree lets cancellation kill the whole spawned tree, not just the
// direct child: m4b-merge may be a script wrapper that spawns children, and
// a grandchild that inherits the stdout pipe would otherwise hold it open
// long after the direct child is killed, stalling the run past its deadline.
type processTree interface {
	// start records the platform kill target for a running cmd.
	start(cmd *exec.Cmd) error
	// kill terminates the whole tree. os.ErrProcessDone means the tree is
	// already gone: nothing for os/exec to do.
	kill() error
	// finish releases the kill target; call after cmd.Wait has reaped the
	// child.
	finish()
}

// posixProcessTree puts the child in its own process group and kills the
// group.
type posixProcessTree struct {
	cmd *exec.Cmd
}

// newProcessTree must run before cmd.Start: the process-group attribute
// only applies to a not-yet-started command.
func newProcessTree(cmd *exec.Cmd) processTree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &posixProcessTree{cmd: cmd}
}

func (p *posixProcessTree) start(cmd *exec.Cmd) error {
	return nil
}

func (p *posixProcessTree) kill() error {
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func (p *posixProcessTree) finish() {
}

var _ processTree = (*posixProcessTree)(nil)
