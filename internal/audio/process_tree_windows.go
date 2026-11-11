//go:build windows

package audio

import (
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
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

// windowsProcessTree confines the child — and every process it spawns — to
// a job object that kills its entire tree when the job handle closes.
type windowsProcessTree struct {
	job    windows.Handle
	proc   windows.Handle
	done   bool
	closed bool
}

func newProcessTree(cmd *exec.Cmd) processTree {
	return &windowsProcessTree{}
}

func (w *windowsProcessTree) start(cmd *exec.Cmd) error {
	if w.proc != 0 {
		return os.ErrProcessDone
	}
	proc, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	w.proc = proc
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		windows.CloseHandle(proc)
		return err
	}
	var limits windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
	limits.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectBasicLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		windows.CloseHandle(proc)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		windows.CloseHandle(proc)
		return err
	}
	w.job = job
	return nil
}

func (w *windowsProcessTree) kill() error {
	if w.done {
		return os.ErrProcessDone
	}
	w.done = true
	// Closing the job handle kills the whole tree (kill-on-close).
	if w.job == 0 {
		return os.ErrProcessDone
	}
	return windows.CloseHandle(w.job)
}

func (w *windowsProcessTree) finish() {
	if !w.closed {
		w.closed = true
		if w.proc != 0 {
			windows.CloseHandle(w.proc)
			w.proc = 0
		}
		if w.job != 0 {
			windows.CloseHandle(w.job)
			w.job = 0
		}
	}
}

var _ processTree = (*windowsProcessTree)(nil)
