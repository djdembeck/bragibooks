package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// newEngineCommand builds the m4b-merge child for a structured job file.
// preview selects --job-preview (non-mutating) vs. execution. ctx must come
// from the run's request context: cmd.Cancel (the process-group kill) is
// only legal on a CommandContext child.
func newEngineCommand(ctx context.Context, binary, jobFile, logLevel string, preview bool) *exec.Cmd {
	verb := "--machine"
	if preview {
		verb = "--job-preview"
	}
	args := []string{"--job", jobFile, verb, "--log-level", logLevel}
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
	binary, _, _, _, logLevel, _ := p.snapshot()

	if opts.OutputRoot == "" {
		return nil, errors.New("RunJob: output root is empty")
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

	// Job-owned job file inside the claim root: the job's own tree, no
	// shared /tmp naming. Removed in a defer on every path.
	jobFile := filepath.Join(absRoot, ".bragi-job-"+jobID+".json")
	if err := os.WriteFile(jobFile, data, 0o600); err != nil {
		return nil, fmt.Errorf("write job file: %w", err)
	}
	defer os.Remove(jobFile)

	cmd := newEngineCommand(ctx, binary, jobFile, logLevel, false)
	tree := newProcessTree(cmd)
	cmd.Cancel = func() error {
		if err := tree.start(cmd); err != nil {
			return err
		}
		return tree.kill()
	}
	cmd.WaitDelay = 5 * time.Second

	var stdoutBuf, stderrBuf bytes.Buffer
	var stream *lineStreamWriter
	// Machine mode: stdout is the result document, stderr is the log/progress
	// stream. This is the inverse of the legacy CLI invocation, where stdout
	// carried human progress.
	cmd.Stdout = &stdoutBuf
	if opts.OnOutput != nil {
		stream = &lineStreamWriter{onLine: opts.OnOutput}
		cmd.Stderr = io.MultiWriter(&stderrBuf, stream)
	} else {
		cmd.Stderr = &stderrBuf
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("m4b-merge start: %w", err)
	}
	waitErr := cmd.Wait()
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
	return p.finishJobRun(result, doc, opts, absRoot)
}

// finishJobRun validates a parsed machine document and reports the outcome.
// It never deletes recoverable staging on a non-success.
func (p *Processor) finishJobRun(result *JobRunResult, doc machineResult, opts JobRunOptions, root string) (*JobRunResult, error) {
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
	// clean. This is a best-effort cleanup of Bragi's own claim root only.
	if err := removeStagingSiblings(root); err != nil {
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
// misreported document (a swapped-in symlink is the escape vector this
// check exists for).
func validatePublishedArtifact(a mArtifact, root string) (string, error) {
	if a.RelativePath == "" {
		return "", errors.New("empty relative path")
	}
	if filepath.IsAbs(a.RelativePath) {
		return "", errors.New("absolute path from engine")
	}
	for _, seg := range strings.Split(filepath.ToSlash(a.RelativePath), "/") {
		if seg == ".." {
			return "", errors.New("path contains a '..' segment")
		}
	}
	final := filepath.Join(root, filepath.FromSlash(a.RelativePath))
	info, err := os.Lstat(final)
	if err != nil {
		return "", fmt.Errorf("published path missing: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(final)
		if err != nil {
			return "", fmt.Errorf("symlink does not resolve: %w", err)
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(absRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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

// stagingSiblingPattern matches the engine's per-run staging siblings
// (.m4b-merge-staging-<pid>-<nanos>-<n>.<ext>) left in the output tree.
var stagingSiblingPattern = ".m4b-merge-staging-*"

// removeStagingSiblings walks root (the job's claim root, never the source
// tree) and removes the engine's leftover staging siblings. It only touches
// files matching the engine's own prefix, so a user's real output can never
// be removed by cleanup.
func removeStagingSiblings(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(stagingSiblingPattern, d.Name()); ok {
			return os.Remove(path)
		}
		return nil
	})
}
