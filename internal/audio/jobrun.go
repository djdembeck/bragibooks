package audio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// drainTimeout bounds the post-exit wait for the stdout reader: a
// background descendant that inherits the pipe's write side can hold it
// open long after the direct child exits, and an unbounded drain would
// stall the job worker on the inherited descriptor. After the bound, the
// process group is killed (the inherited-fd holder dies with it) AND the
// read end is closed, so the reader's copy errors out even when the
// holder escaped the group (a descendant that started its own session):
// the reader is stoppable on its own, so the drain can never outlive the
// bound.
const drainTimeout = 2 * time.Second

// newEngineCommand builds the m4b-merge child for a structured job file.
// preview selects --job-preview (non-mutating) vs. execution. ctx must come
// from the run's request context: cmd.Cancel (the process-group kill) is
// only legal on a CommandContext child. numCPUs is the configured engine CPU
// limit, forwarded as a standalone flag (the engine's Job struct is
// deny_unknown_fields, so it has no in-file field); 0 is not forwarded, in
// which case the engine applies its own available-parallelism default.
func newEngineCommand(ctx context.Context, binary, jobFile, logLevel string, numCPUs int, preview bool) *exec.Cmd {
	verb := "--machine"
	if preview {
		verb = "--job-preview"
	}
	args := []string{"--job", jobFile, verb, "--log-level", logLevel}
	if numCPUs > 0 {
		args = append(args, "--num-cpus", strconv.Itoa(numCPUs))
	}
	return exec.CommandContext(ctx, binary, args...)
}

// RunJob executes the engine's structured job protocol and consumes its
// single machine result.
//
//	m4b-merge --job <job-owned file> --machine
//
// stdout carries exactly one machine document; stderr carries the engine's
// log/progress stream (routed to OnOutput). The engine OWNS publication: it
// writes each artifact directly to its final location under the job's
// OutputRoot via a staging sibling in the FINAL file's own directory, then an
// atomic no-replace link. Because the staging sibling and the final file are
// in the same directory, publication is same-device by construction and the
// engine's atomic link is the no-clobber guarantee — a collision (the name
// appeared after the engine checked) refuses with exit 1 and leaves the
// existing file untouched. Bragi therefore never relocates, copies, or
// guesses: it validates the machine result and cleans up.
//
// Bragi's responsibilities, in order:
//  1. Write the job file to a job-owned temp name inside OutputRoot (the
//     engine's claim root), so the job's own tree holds it; delete it after.
//  2. Run the engine; capture stdout (machine doc) and stderr (progress).
//  3. Verify the result's schema/resolver identity matches the job's captured
//     identity. A mismatch means the engine changed under the job: refuse,
//     preserve the staging tree, and surface a re-preview/requeue message.
//  4. On "succeeded": validate every artifact (relative, no "..", no symlink
//     escape out of OutputRoot, regular file, present) and remove the engine's
//     leftover staging siblings, then report the final paths.
//  5. On any non-success: PRESERVE the staging tree (the engine retained its
//     recoverable staged entry) and surface the diagnosed group errors.
//
// Cancellation is the caller's job (the context kills the process tree); on a
// cancelled run the engine dies mid-staging and the preserved siblings are
// what recovery reads.
type JobRunResult struct {
	// Status is the engine's top-level status: "succeeded",
	// "partial_failure", or "failed".
	Status string
	// OutputFiles are the final, validated produced paths (audio + any
	// sidecars), present only when every group succeeded.
	OutputFiles []string
	// PrimaryOutput is the audio artifact's final path ("" if none), for the
	// book's output_file column.
	PrimaryOutput string
	// GroupErrors carries the engine's diagnosed per-group error(s).
	GroupErrors []string
	// MachineJSON is the raw stdout document, persisted to the job log.
	MachineJSON string
	// Stderr is the stripped progress stream (the job log body).
	Stderr string
	// StagingPreserved is the OutputRoot when a non-success left recoverable
	// staging behind ("" on a clean success, where Bragi removed it).
	StagingPreserved string
	// ExitCode is the engine's process exit code (0 success, nonzero failure).
	ExitCode int
}

// JobRunOptions carries per-run inputs. OutputRoot is the engine's claim root
// AND the final destination: the engine publishes under it. Identity is the
// schema/resolver pair the job was captured under; the result must report it.
// OnOutput, when set, receives stripped engine stderr lines as they arrive.
type JobRunOptions struct {
	OutputRoot string
	Identity   EngineIdentity
	OnOutput   func(line string)
}

func (p *Processor) RunJob(ctx context.Context, jobID string, jf JobFile, opts JobRunOptions) (*JobRunResult, error) {
	binary, _, _, _, logLevel, numCPUs := p.snapshot()

	if opts.OutputRoot == "" {
		return nil, errors.New("RunJob: output root is empty")
	}
	// Fail-closed gates on the EXACT executable this run will launch, BEFORE
	// the mutating command starts: the startup gate covers first boot; these
	// catch a binary that was swapped, removed, re-pointed, or re-built after
	// startup. The same snapshot call that produced `binary` above is used
	// throughout, so a single job observes one consistent executable,
	// destination, and format (AGENTS: one settings snapshot per run).
	//
	// 1. Version floor: the binary is at least the minimum protocol build.
	if _, err := p.CheckVersionFor(ctx, binary); err != nil {
		return nil, fmt.Errorf("pre-launch engine gate: %w", err)
	}
	// 2. Captured schema/resolver identity: the job was captured under a
	// specific naming-identity pair, and the binary about to run it must
	// REPORT that exact pair. A changed resolver (engine re-pointed to a new
	// build between capture and run) must be refused before media/source
	// mutation, not only after the engine has already published: the
	// post-result identity check below stays as the defense against the
	// engine lying in its result document.
	if caps, err := p.ProbeCapabilitiesFor(ctx, binary); err != nil {
		return nil, fmt.Errorf("pre-launch engine gate: probe capabilities: %w", err)
	} else if caps.SchemaVersion != opts.Identity.SchemaVersion || caps.ResolverVersion != opts.Identity.ResolverVersion {
		return nil, fmt.Errorf("pre-launch engine gate: engine reports schema %d / resolver %s, job was captured under schema %d / resolver %s; re-preview and requeue the book under the current engine",
			caps.SchemaVersion, caps.ResolverVersion, opts.Identity.SchemaVersion, opts.Identity.ResolverVersion)
	}

	absRoot, err := filepath.Abs(opts.OutputRoot)
	if err != nil {
		return nil, fmt.Errorf("abs output root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create output root %s: %w", absRoot, err)
	}

	data, err := JobFileJSON(jf)
	if err != nil {
		return nil, fmt.Errorf("serialize job: %w", err)
	}

	// Baseline of staging that pre-dates THIS run (any pid): the engine
	// names its staging siblings with its own process pid, which the OS
	// recycles, so cleanup must not treat a recycled pid's name as this
	// run's residue (see removeStagingSiblings).
	preExisting := existingStagingSiblings(absRoot)

	// Job-owned manifest in a job-scoped 0700 directory under the
	// system temp root, OUT of the shared writable claim root: a
	// principal with write (rename) rights on the claim root could
	// replace the directory the engine later opens by pathname — 0700
	// protects the directory's contents, not the directory entry itself.
	// The system temp root is not writable by the output root's
	// principals, so the entry sits behind a directory they cannot
	// rename. The directory is removed in a defer on every path.
	jobDir := filepath.Join(os.TempDir(), "bragi-job-"+jobID)
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		if os.IsExist(err) {
			// Residue from a crashed run: the directory is named by
			// jobID, so removing and recreating cannot touch a live
			// run's manifest. os.Remove fails on a non-empty directory,
			// keeping the check fail-closed.
			if rerr := os.Remove(jobDir); rerr != nil {
				return nil, fmt.Errorf("create job dir %s: %w", jobDir, rerr)
			}
			if err = os.Mkdir(jobDir, 0o700); err != nil {
				return nil, fmt.Errorf("create job dir %s: %w", jobDir, err)
			}
		} else {
			return nil, fmt.Errorf("create job dir %s: %w", jobDir, err)
		}
	}
	defer os.Remove(jobDir)

	// O_EXCL on the create is the symlink guard: a pre-existing path at
	// this name (regular file OR symlink) makes the open fail instead of
	// letting a write follow the link to a target outside the job dir --
	// 0o600 only protects a file Bragi itself created.
	jobFile := filepath.Join(jobDir, ".bragi-job-"+jobID+".json")
	jobFileHandle, err := os.OpenFile(jobFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("write job file: %w", err)
	}
	if _, werr := jobFileHandle.Write(data); werr != nil {
		jobFileHandle.Close()
		os.Remove(jobFile)
		return nil, fmt.Errorf("write job file: %w", werr)
	}
	created, statErr := jobFileHandle.Stat()
	if cerr := jobFileHandle.Close(); cerr != nil {
		os.Remove(jobFile)
		return nil, fmt.Errorf("write job file: %w", cerr)
	}
	defer os.Remove(jobFile)

	// Same-uid re-check against the entry Bragi just created: the 0700
	// directory excludes other principals, and this closes the residual
	// same-user window between close and the engine's open of --job.
	if statErr == nil {
		launched, lerr := os.Stat(jobFile)
		if lerr != nil {
			return nil, fmt.Errorf("job file vanished before launch: %w", lerr)
		}
		if launched.Size() != created.Size() || !launched.ModTime().Equal(created.ModTime()) {
			return nil, fmt.Errorf("job file %q was modified between write and launch", jobFile)
		}
	}

	cmd := newEngineCommand(ctx, binary, jobFile, logLevel, numCPUs, false)
	tree, err := newProcessTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("confine process tree: %w", err)
	}
	// Cancel only kills: the child is confined to the tree the moment
	// Start returns (below), so by cancel time the whole tree is already
	// inside it.
	cmd.Cancel = func() error {
		return tree.kill()
	}
	// os/exec enforces WaitDelay as an I/O-completion bound measured from
	// the child's exit: with a background descendant holding the
	// inherited stdout, the I/O goroutine's copy runs past a too-short
	// bound and cmd.Wait returns "WaitDelay expired before I/O complete"
	// (not an exit outcome). The bounded drain below kills the group up
	// to drainTimeout after the child exits, so the bound must outlive
	// it.
	cmd.WaitDelay = 5*time.Second + drainTimeout

	var stdoutBuf, stderrBuf bytes.Buffer
	var stream *lineStreamWriter
	// Machine mode: stdout is the result document, stderr is the log/progress
	// stream. This is the inverse of the legacy CLI invocation, where stdout
	// carried human progress.
	//
	// stdout is a pipe drained by the reader goroutine below, not a buffer:
	// the reader parses the machine document the moment its first line is
	// available and, if it carries a pid, verifies that pid against the
	// direct child's LIVE /proc ppid chain (unix) and stores the verified
	// pid in earlyPID. The verification is BEST-EFFORT and runs as early
	// as the pid is readable — on the first complete document line
	// (ReadString on '\n'), not at document EOF — so it lands while the
	// engine is still observable. This is what lets a wrapped (non-exec)
	// run keep its engine's staging files: with a wrapper the wrapper
	// reaps the engine before the wrapper itself exits, so a verification
	// after cmd.Wait cannot read the engine's /proc entry and falls back
	// to the wrapper's pid — leaving the engine's staging siblings
	// behind. The RESIDUAL RACE IS ACCEPTED: if the wrapper reaps the
	// engine before the reader runs the verification, the /proc chain is
	// already gone and the fallback to the wrapper PID is the correct
	// degraded behavior (a pid that cannot be proven to belong to this
	// run's tree must not authorize cleanup). The post-Wait drain wait is
	// bounded (drainTimeout) precisely to give the reader its one chance
	// to verify before that fallback is committed. earlyPID stays 0 when
	// the document reports no pid or the pid cannot be proven to belong
	// to this run's tree; the post-Wait resolution then falls back to
	// the known child pid.
	stdoutRead, stdoutWrite, pipeErr := os.Pipe()
	if pipeErr != nil {
		return nil, fmt.Errorf("stdout pipe: %w", pipeErr)
	}
	cmd.Stdout = stdoutWrite
	// closeRead closes the pipe's read end exactly once: the reader's
	// deferred close and the drain-timeout close share it, so a timeout
	// that lands while the reader is still in flight never double-closes
	// the fd (a second Close would only return EBADF, but the fd is then
	// gone from the process's table regardless of which caller asked).
	var closeReadOnce sync.Once
	closeRead := func() { closeReadOnce.Do(func() { _ = stdoutRead.Close() }) }
	// 0 = the read-time verification found nothing usable (the post-Wait
	// resolution falls back to the known child pid).
	var earlyPID atomic.Int64
	var earlyWG sync.WaitGroup
	earlyWG.Add(1)
	go func() {
		defer earlyWG.Done()
		defer closeRead()
		r := bufio.NewReader(stdoutRead)
		// Read the machine document (one line of JSON) as it streams in and
		// verify its pid against the LIVE tree the moment the line is
		// complete. ReadString returns the partial line on EOF, so a
		// document written without a trailing newline is still parsed.
		line, lerr := r.ReadString('\n')
		if doc, perr := parseMachineResult([]byte(strings.TrimSpace(line))); perr == nil && doc.Pid != nil && *doc.Pid > 0 {
			earlyPID.Store(int64(verifyEnginePID(cmd.Process.Pid, *doc.Pid)))
		}
		stdoutBuf.WriteString(line)
		if lerr == nil || lerr == io.EOF {
			// Drain anything after the document line (a clean document
			// writes nothing more), then let the pipe close normally.
			if _, derr := io.Copy(&stdoutBuf, r); derr != nil && derr != io.EOF {
				_ = derr
			}
		}
	}()
	if opts.OnOutput != nil {
		stream = &lineStreamWriter{onLine: opts.OnOutput}
		cmd.Stderr = io.MultiWriter(&stderrBuf, stream)
	} else {
		cmd.Stderr = &stderrBuf
	}

	if err := cmd.Start(); err != nil {
		tree.finish()
		// Release the reader (the child never inherited the write side).
		_ = stdoutWrite.Close()
		earlyWG.Wait()
		return nil, fmt.Errorf("m4b-merge start: %w", err)
	}
	// Constrain the child to the tree NOW, before it can spawn
	// descendants that would escape a cancel-time assignment. A failed
	// confinement aborts the run: an unconfined m4b-merge tree must not
	// run.
	if err := tree.start(cmd); err != nil {
		tree.finish()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		// The killed child released its write side; close ours too and
		// drain the reader before returning.
		_ = stdoutWrite.Close()
		earlyWG.Wait()
		return nil, fmt.Errorf("confine process tree: %w", err)
	}
	waitErr := cmd.Wait()
	// Close the write side so the reader sees EOF: a run that exits without
	// writing a document (crash, OOM, kill before output) would otherwise
	// leave the reader blocked in ReadString forever. cmd.Wait has reaped
	// the child, so no further writes from the direct child are possible.
	_ = stdoutWrite.Close()
	// Bounded drain: the pid verification is best-effort, so the reader
	// gets its one chance to parse the document line and run it before
	// the fallback is committed — but an INHERITED write side held by a
	// background descendant (the direct child exits without killing its
	// group) must not stall the worker. On the bound, kill the process
	// group: the holder dies with it, the reader sees EOF, and a
	// document without a trailing newline is still parsed from the
	// partial line.
	drainDone := make(chan struct{})
	go func() { earlyWG.Wait(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(drainTimeout):
		// Kill the group first (the primary release: the inherited-fd
		// holder dies with it). Then close the read end: a holder that
		// escaped the group (its own session, SIGKILL ignored) keeps the
		// write side open, so without this the reader would sit in its
		// copy until that holder exits — the <drainDone> below would
		// block past the bound. The close makes the copy error and the
		// reader return; closeRead is idempotent with the reader's own
		// deferred close.
		_ = tree.kill()
		closeRead()
		<-drainDone
	}
	tree.finish()
	if stream != nil {
		stream.Flush()
	}

	result := &JobRunResult{
		MachineJSON: stdoutBuf.String(),
		Stderr:      stripEscapeSequences(stderrBuf.String()),
	}

	var execErr *exec.ExitError
	if waitErr != nil {
		if !errors.As(waitErr, &execErr) {
			// Not a run outcome: a start/pipe failure.
			return result, fmt.Errorf("m4b-merge: %w", waitErr)
		}
		result.ExitCode = execErr.ExitCode()
	}

	if strings.TrimSpace(result.MachineJSON) == "" {
		// No machine document at all (crash, OOM, kill before output).
		return result, fmt.Errorf("m4b-merge exited %d without a machine result: %s", result.ExitCode, result.Stderr)
	}
	doc, perr := parseMachineResult([]byte(result.MachineJSON))
	if perr != nil {
		return result, fmt.Errorf("m4b-merge exited %d with an unreadable machine result: %w", result.ExitCode, perr)
	}
	// Staging siblings are named with the ENGINE process's pid; the pid
	// plus the pre-run snapshot (preExisting) are what scope cleanup to
	// this run (see removeStagingSiblings). With a wrapper (non-exec)
	// command, cmd.Process.Pid is the WRAPPER's pid, so the engine's own
	// pid (reported by the machine document) is what the staging naming
	// used — a success scan by the wrapper pid would miss the engine's
	// siblings. Fall back to the direct child's pid when the document
	// does not report one (a bare engine IS the direct child).
	//
	// The reported pid authorizes deletion, so it is ownership-verified:
	// it must be this run's child or a verified descendant (the /proc ppid
	// chain on unix); anything else falls back to the known child pid
	// rather than deleting siblings it does not own. The verification
	// happened in the reader goroutine at document-read time (earlyPID) —
	// while the process tree is still alive — because a wrapped run's
	// wrapper reaps the engine before it exits, so a post-Wait /proc read
	// would find the engine gone and fall back to the wrapper's pid.
	// earlyPID already carries the verified pid, or 0 when the document
	// reported none / it was unverifiable (the child-pid fallback).
	engPID := cmd.Process.Pid
	if early := int(earlyPID.Load()); early > 0 {
		engPID = early
	}
	return p.finishJobRun(result, doc, opts, absRoot, engPID, preExisting)
}

// finishJobRun validates a parsed machine document and reports the outcome.
// It never deletes recoverable staging on a non-success.
func (p *Processor) finishJobRun(result *JobRunResult, doc machineResult, opts JobRunOptions, root string, engPID int, preExisting map[string]bool) (*JobRunResult, error) {
	result.Status = doc.Status
	for _, g := range doc.Groups {
		if g.Error != nil && *g.Error != "" {
			result.GroupErrors = append(result.GroupErrors, *g.Error)
		}
	}
	// A setup-level diagnosed error (the run failed before any group ran) is
	// the most useful thing the operator can have; surface it first.
	if doc.Error != nil && *doc.Error != "" {
		result.GroupErrors = append([]string{*doc.Error}, result.GroupErrors...)
	}

	// Identity gate: the result must report the protocol the job was captured
	// under. A mismatch means the engine changed under the job; a stale
	// snapshot must never run under new naming semantics. Preserve staging.
	if doc.SchemaVersion != opts.Identity.SchemaVersion || doc.ResolverVersion != opts.Identity.ResolverVersion {
		result.StagingPreserved = root
		return result, fmt.Errorf(
			"engine resolver changed (result schema %d / resolver %q, job captured %d / %q); re-preview and requeue",
			doc.SchemaVersion, doc.ResolverVersion, opts.Identity.SchemaVersion, opts.Identity.ResolverVersion,
		)
	}

	if doc.Status != "succeeded" {
		// partial_failure and failed both preserve recoverable staging and
		// surface the diagnosed errors; the engine already retained them.
		result.StagingPreserved = root
		if len(result.GroupErrors) == 0 {
			result.GroupErrors = []string{result.Stderr}
		}
		return result, fmt.Errorf("engine run %s (exit %d)", doc.Status, result.ExitCode)
	}
	if result.ExitCode != 0 {
		// The machine document says succeeded but the process exited
		// nonzero: a contradiction the document alone cannot settle. Treat
		// it as a failure (the artifact stays recoverable in staging).
		result.StagingPreserved = root
		if len(result.GroupErrors) == 0 {
			result.GroupErrors = []string{result.Stderr}
		}
		return result, fmt.Errorf("engine exited %d with a succeeded document", result.ExitCode)
	}

	// Success: validate every artifact, then remove leftover staging siblings.
	var audioPath string
	for _, g := range doc.Groups {
		for _, a := range g.Artifacts {
			final, verr := validatePublishedArtifact(a, root)
			if verr != nil {
				return result, fmt.Errorf("validate artifact %q: %w", a.RelativePath, verr)
			}
			result.OutputFiles = append(result.OutputFiles, final)
			if a.Kind == "audio" && audioPath == "" {
				audioPath = final
			}
		}
	}
	if audioPath == "" {
		return result, fmt.Errorf("engine succeeded but enumerated no audio artifact")
	}
	result.PrimaryOutput = audioPath

	// On a clean success the engine's staging siblings are pure residue (the
	// final files are already claimed); remove them so the output tree stays
	// clean. Cleanup is scoped to THIS engine process's staging prefix: the
	// pid in the name is what separates this run's residue from a
	// concurrently running job's in-flight staging in the same root.
	// Best-effort, Bragi's own claim root only.
	if err := removeStagingSiblings(root, engPID, preExisting); err != nil {
		// Cleanup failure after a validated success is logged, not fatal:
		// the output is already published and correct.
		log.Printf("remove staging siblings under %s: %v", root, err)
	}
	return result, nil
}

// validatePublishedArtifact enforces the containment contract for one
// machine-result artifact that the engine published under root: relative, no
// absolute path, no ".." segment, and a regular file on disk. The engine
// already validated its own paths, so this is a defense against a tampered or
// misreported document.
//
// Containment is checked COMPONENT BY COMPONENT: each directory segment
// between root and the file is lstat'd, and a symlinked component that
// resolves outside the canonical root is rejected. Checking only the final
// path is not sufficient — a symlinked intermediate directory makes the
// final path a regular file from Lstat's point of view while its bytes live
// outside the root.
func validatePublishedArtifact(a mArtifact, root string) (string, error) {
	if a.RelativePath == "" {
		return "", errors.New("empty relative path")
	}
	if filepath.IsAbs(a.RelativePath) {
		return "", errors.New("absolute path from engine")
	}
	segs := strings.Split(filepath.ToSlash(a.RelativePath), "/")
	for _, seg := range segs {
		if seg == ".." {
			return "", errors.New("path contains a '..' segment")
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("output root does not resolve: %w", err)
	}

	// Walk every intermediate directory, rejecting any component that is a
	// symlink escaping the root.
	prefix := canonicalRoot
	for _, seg := range segs[:len(segs)-1] {
		prefix = filepath.Join(prefix, seg)
		info, err := os.Lstat(prefix)
		if err != nil {
			return "", fmt.Errorf("published path missing: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(prefix)
			if err != nil {
				return "", fmt.Errorf("symlink does not resolve: %w", err)
			}
			if !withinRoot(canonicalRoot, resolved) {
				return "", fmt.Errorf("path component %q escapes output root", seg)
			}
		} else if !info.IsDir() {
			return "", fmt.Errorf("path component %q is not a directory", seg)
		}
	}

	// Final component.
	final := filepath.Join(prefix, segs[len(segs)-1])
	info, err := os.Lstat(final)
	if err != nil {
		return "", fmt.Errorf("published path missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(final)
		if err != nil {
			return "", fmt.Errorf("symlink does not resolve: %w", err)
		}
		if !withinRoot(canonicalRoot, resolved) {
			return "", fmt.Errorf("symlink escapes output root: %s", resolved)
		}
		if info, err = os.Stat(resolved); err != nil {
			return "", err
		}
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	return final, nil
}

// withinRoot reports whether resolved lies inside canonicalRoot (both
// absolute, both symlink-resolved).
func withinRoot(canonicalRoot, resolved string) bool {
	rel, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// stagingSiblingPrefix matches the engine's per-run staging siblings
// (.m4b-merge-staging-<pid>-<nanos>-<n>.<ext>) left in the output tree. The
// pid is the engine process's own.
const stagingSiblingPrefix = ".m4b-merge-staging-"

// existingStagingSiblings records every staging-prefix file already under
// root (any pid): the set that pre-dates this run. The engine names staging
// with its own process pid, which the OS recycles — without this baseline,
// a fresh run whose pid was reused would destroy a previously FAILED run's
// recoverable staging in the same root during its success cleanup.
func existingStagingSiblings(root string) map[string]bool {
	existing := make(map[string]bool)
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		// A walk error (e.g. a permission failure on a subdirectory)
		// leaves the baseline INCOMPLETE: log it so an under-counted
		// baseline — which cleanup later treats as "not pre-existing" —
		// is observable rather than silently destructive.
		if err != nil {
			log.Printf("audio: staging baseline walk under %s: %v", root, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), stagingSiblingPrefix) {
			existing[path] = true
		}
		return nil
	})
	return existing
}

// removeStagingSiblings walks root (the job's claim root, never the source
// tree) and removes this run's leftover staging siblings: files named with
// the engine prefix followed by this run's engine pid AND absent from
// preExisting (the pre-run snapshot). The pid match keeps a concurrent
// run's in-flight staging untouched; the pre-run snapshot keeps a
// recycled-pid name from destroying an earlier failed run's recoverable
// staging in the same root. It only touches files matching this run's own
// prefix, so a user's real output can never be removed by cleanup.
func removeStagingSiblings(root string, engPID int, preExisting map[string]bool) error {
	prefix := stagingSiblingPrefix + strconv.Itoa(engPID) + "-"
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), prefix) && !preExisting[path] {
			return os.Remove(path)
		}
		return nil
	})
}
