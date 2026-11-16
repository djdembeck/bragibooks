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
// child is complete at cmd.Start and start records nothing (the unix twin
// of the Windows suspended-start contract: the tree is established the
// moment the process exists).
type posixProcessTree struct {
	cmd *exec.Cmd
}

// newProcessTree must run before cmd.Start: the process-group attribute
// only applies to a not-yet-started command.
func newProcessTree(cmd *exec.Cmd) (processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &posixProcessTree{cmd: cmd}, nil
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
