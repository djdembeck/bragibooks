//go:build windows

package audio

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree lets cancellation kill the whole spawned tree, not just the
// direct child: m4b-merge may be a script wrapper that spawns children, and
// a grandchild that inherits the stdout pipe would otherwise hold it open
// long after the direct child is killed, stalling the run past its deadline.
type processTree interface {
	// start confines the RUNNING child to the tree. The caller must run
	// it immediately after cmd.Start has succeeded. On Windows the child
	// is launched SUSPENDED (see newProcessTree), so start is the atomic
	// launch+assignment step: the process cannot spawn a single
	// instruction — let alone a grandchild that escapes the job — until
	// start has assigned it to the job and resumed it.
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
//
// exec.CommandContext can invoke kill (Cancel) CONCURRENTLY with the
// caller's start, so the handles and their liveness flags are owned by a
// single mutex: start's open/assign/resume and kill's close/terminate
// serialize, every close is followed by a zeroing under the same lock,
// and a handle is closed at most once.
type windowsProcessTree struct {
	mu sync.Mutex // guards job/proc/done/closed
	// job is the kill-on-close job (created before the command starts);
	// proc the child's process handle (opened by start, killed/closed by
	// kill or finish). Both zero mean "nothing to release".
	job    windows.Handle
	proc   windows.Handle
	done   bool
	closed bool
}

// newProcessTree establishes the kill-on-close job object BEFORE the
// command starts and suspends the child's launch: the child is created in
// a suspended state (CREATE_SUSPENDED) and only resumed by start, AFTER
// it is assigned to the job. Launching unsuspended and assigning
// afterwards would leave a window in which a script wrapper can spawn
// grandchildren that never join the job (they inherit the parent's job
// membership at creation), escaping cancellation and its stdout pipe.
// On failure the job (if any) is closed and the caller must not start the
// command unconfined.
func newProcessTree(cmd *exec.Cmd) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	var limits windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
	limits.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectBasicLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("set job object limits: %w", err)
	}
	// Suspend the child's primary thread at creation so start can assign
	// it to the job before it executes (see the start contract).
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &windowsProcessTree{job: job}, nil
}

// start assigns the SUSPENDED child to the job and resumes it: the
// launch+assignment the suspended start makes atomic. Every step after
// OpenProcess is best-effort toward resuming; a failure leaves the child
// suspended (it cannot escape the tree unassigned, and kill/finish and
// the caller's cmd.Process.Kill terminate a suspended process) and
// returns the first error.
func (w *windowsProcessTree) start(cmd *exec.Cmd) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.proc != 0 {
		// Already confined: the tree is established.
		return nil
	}
	if w.closed {
		// finish ran first (a Start failure path): nothing to confine.
		return os.ErrProcessDone
	}
	// AssignProcessToJobObject requires the process handle to carry
	// PROCESS_SET_QUOTA in addition to PROCESS_TERMINATE.
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open suspended child: %w", err)
	}
	w.proc = proc
	if err := windows.AssignProcessToJobObject(w.job, proc); err != nil {
		// The child is suspended and OUTSIDE the job: kill it directly.
		windows.TerminateProcess(proc, 1)
		windows.CloseHandle(proc)
		w.proc = 0
		return fmt.Errorf("assign child to job: %w", err)
	}
	// Resume the primary thread. A kill racing ahead of the resume is
	// harmless: the job's kill-on-close already terminated it, and a
	// failed ResumeThread on a dead process is reported by the caller's
	// Wait, not here.
	thread, terr := openPrimaryThread(cmd.Process.Pid)
	if terr != nil {
		return fmt.Errorf("open child thread: %w", terr)
	}
	defer windows.CloseHandle(thread)
	if _, rerr := windows.ResumeThread(thread); rerr != nil {
		return fmt.Errorf("resume child: %w", rerr)
	}
	return nil
}

// openPrimaryThread opens the primary thread of a just-created
// SUSPENDED child. os.Process does not expose the primary thread id, so
// a toolhelp thread snapshot (filtered to the child's pid) enumerates it:
// a CREATE_SUSPENDED process cannot have spawned a thread on its own, so
// the one it owns at this moment is the one to resume.
func openPrimaryThread(pid int) (windows.Handle, error) {
	te := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("thread snapshot for child %d: %w", pid, err)
	}
	defer windows.CloseHandle(snap)
	// Thread32First initializes the cursor once; each iteration advances
	// with Thread32Next, which also reports exhaustion. Calling First
	// again inside the loop would reset the cursor to the first entry
	// every pass, so the scan would never reach a later thread.
	for err := windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID == uint32(pid) {
			// ResumeThread requires THREAD_SUSPEND_RESUME on the handle;
			// the limited-information right alone would make every
			// resume fail with access denied.
			h, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME|windows.THREAD_QUERY_LIMITED_INFORMATION, false, te.ThreadID)
			if oerr != nil {
				return 0, fmt.Errorf("open child thread %d: %w", te.ThreadID, oerr)
			}
			return h, nil
		}
	}
	return 0, fmt.Errorf("primary thread of child %d not found", pid)
}

func (w *windowsProcessTree) kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return os.ErrProcessDone
	}
	w.done = true
	var err error
	// Closing the job handle kills the whole tree (kill-on-close) —
	// including a suspended child and every assigned descendant.
	if w.job != 0 {
		err = windows.CloseHandle(w.job)
		// Clear the handle after closing: finish must never close the
		// same handle a second time (a double close is undefined on
		// Windows).
		w.job = 0
	}
	// The child is opened but (racing start) not yet in the job: a
	// suspended process is only reachable through its handle. A
	// terminated (post-kill-on-close) process just errors here, which is
	// folded into the job-close result.
	if w.proc != 0 {
		if terr := windows.TerminateProcess(w.proc, 1); err == nil {
			err = terr
		}
		windows.CloseHandle(w.proc)
		w.proc = 0
	}
	return err
}

func (w *windowsProcessTree) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
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
