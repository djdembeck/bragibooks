package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bragibooks/bragibooks/internal/config"
)

// Processor invokes the m4b-merge Rust CLI binary for audiobook processing.
// The CLI fields are mutable at runtime: ApplyConfig replaces them from the
// live config after a settings update, and every run snapshots them under
// the read lock so a single job observes one consistent set.
type Processor struct {
	mu           sync.RWMutex
	Binary       string
	OutputDir    string
	CompletedDir string
	NumCPUs      int
	PathFormat   string
	LogLevel     string
	version      string
}

// snapshot returns a consistent copy of the mutable CLI settings (see the
// type comment for the locking contract).
func (p *Processor) snapshot() (binary, outputDir, completedDir, pathFormat, logLevel string, numCPUs int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Binary, p.OutputDir, p.CompletedDir, p.PathFormat, p.LogLevel, p.NumCPUs
}

// ApplyConfig refreshes the mutable m4b-merge settings (binary, output and
// completed directories, CPU count, output scheme, log level) from the live
// config so the next job run uses the new values without a restart. Called
// by the settings handler after a successful update.
func (p *Processor) ApplyConfig(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Binary = cfg.M4bMerge.Binary
	p.OutputDir = cfg.Directories.OutputDir
	p.CompletedDir = cfg.Directories.CompletedDir
	p.NumCPUs = cfg.Processing.NumCPUs
	p.PathFormat = cfg.Processing.PathFormat
	p.LogLevel = cfg.Processing.LogLevel
}

// NewProcessor creates a Processor with the given configuration.
func NewProcessor(binary, outputDir, completedDir string, numCPUs int, pathFormat string, logLevel string) *Processor {
	return &Processor{
		Binary:       binary,
		OutputDir:    outputDir,
		CompletedDir: completedDir,
		NumCPUs:      numCPUs,
		PathFormat:   pathFormat,
		LogLevel:     logLevel,
	}
}

// ProcessResult holds the output of a single m4b-merge run.
type ProcessResult struct {
	OutputFile    string
	Stdout        string
	Stderr        string
	OutputWarning string
}

// stripEscapeSequences removes ANSI/VT100 escape sequences from s so the
// captured m4b-merge output persists and renders as plain text. Only escape
// sequences are removed: newlines, tabs, and all ordinary text — including a
// literal "[32m" that is not preceded by ESC — are preserved.
func stripEscapeSequences(s string) string {
	const esc = 0x1b
	if !strings.ContainsRune(s, esc) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != esc {
			b.WriteByte(s[i])
			continue
		}
		i = escapeSequenceEnd(s, i)
	}
	return b.String()
}

// escapeSequenceEnd returns the index of the last byte of the escape
// sequence starting at s[i] (an ESC byte), so the caller's loop resumes just
// past it. A truncated sequence consumes the remainder of the input.
func escapeSequenceEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return i
	}
	switch s[j] {
	case '[': // CSI: parameter/intermediate bytes, then one final byte.
		for j++; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j
			}
		}
		return len(s) - 1
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC: BEL- or ST-terminated.
		for j++; j < len(s); j++ {
			if s[j] == 0x07 {
				return j
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 1
			}
		}
		return len(s) - 1
	default: // Zero or more intermediate bytes, then one final byte.
		// Non-CSI escapes may carry intermediates: VT100 charset
		// designations such as ESC ( B and ESC ) 0, or ESC # 8.
		for ; j < len(s); j++ {
			if s[j] < 0x20 || s[j] > 0x2f {
				return j
			}
		}
		return len(s) - 1
	}
}

// Run executes m4b-merge with the given input file paths.
// Returns the output .m4b file path, or an error.
//
// jobID scopes output discovery to this run: m4b-merge writes its result
// under -o using a -p path scheme (no per-run output dir flag, and the
// scheme tree is shared across concurrent workers), so discovering "the
// newest .m4b in OutputDir" misattributes other workers' outputs. We
// therefore write each job into its own subtree, OutputDir/<jobID>.tmp/
// (reusing the -p scheme tree inside it), and only on success move the
// discovered file to its final location under OutputDir. This uses the
// existing m4b-merge flags only; no CLI surface is changed.
func (p *Processor) Run(ctx context.Context, jobID string, inputPaths []string, asin string) (*ProcessResult, error) {
	binary, outputRoot, completedDir, pathFormat, logLevel, numCPUs := p.snapshot()

	// A relative output root (a valid dev configuration) must be
	// absolutized before discovery and the final move: findNewestM4B
	// returns absolute paths, so with a relative root filepath.Rel fails,
	// OutputFile stays empty, and the per-job cleanup below deletes the
	// produced .m4b. Empty stays empty.
	if outputRoot != "" {
		outputRoot, _ = filepath.Abs(outputRoot)
	}

	outputDir := outputRoot
	workDir := ""
	// An empty output root (misconfiguration or a test processor) would put
	// the per-job subtree in cwd; in that case fall back to unscoped
	// discovery as before.
	if jobID != "" && outputRoot != "" {
		workDir = filepath.Join(outputRoot, jobID+".tmp")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create job output dir %s: %w", workDir, err)
		}
		outputDir = workDir
	}

	args := []string{
		"-o", outputDir,
		"-p", pathFormat,
		"--num-cpus", strconv.Itoa(numCPUs),
		"--log-level", logLevel,
	}
	if completedDir != "" {
		args = append(args, "--completed-directory", completedDir)
	}
	if asin != "" {
		args = append(args, "-a", asin)
	}
	// m4b-merge (clap) requires one -i per input; a single -i with multiple
	// values errors with "unexpected argument".
	for _, in := range inputPaths {
		args = append(args, "-i", in)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	// Own process group: ctx cancellation must kill the whole tree, not just
	// the direct child. m4b-merge is a script wrapper here (and may spawn
	// children in general); a grandchild that inherits the stdout pipe would
	// otherwise hold io.ReadAll open long after the child itself is killed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				// Already gone: nothing for os/exec to do.
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	// Guard against any writer we could not kill: force Wait to return
	// shortly after cancel instead of blocking on inherited pipes.
	cmd.WaitDelay = 5 * time.Second

	// os/exec owns the drain: with buffers wired directly as the child's
	// stdout/stderr, exec internally reads both streams concurrently and
	// guarantees they are fully drained (and the buffers valid) once
	// Wait returns. Manual StdoutPipe/StderrPipe + io.Copy goroutines
	// raced with Wait closing the read ends and lost large stderr output.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
		return nil, fmt.Errorf("m4b-merge start: %w", err)
	}

	waitErr := cmd.Wait()

	// m4b-merge colorizes its tracing output unconditionally (no TTY check,
	// no --no-color flag), so the pipes receive ANSI SGR sequences. Strip
	// them here, at the single point both streams enter the app: processJob
	// persists Stdout as processing_jobs.output (rendered verbatim in the
	// job-log <pre>) and embeds Stderr in the persisted error message.
	result := &ProcessResult{
		Stdout: stripEscapeSequences(stdoutBuf.String()),
		Stderr: stripEscapeSequences(stderrBuf.String()),
	}

	if waitErr != nil {
		// No output was produced: the per-job subtree is garbage and safe
		// to drop. (A relocation failure, by contrast, keeps the subtree —
		// there the .m4b is the only completed artifact; see below.)
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
		return result, fmt.Errorf("m4b-merge: %w: %s", waitErr, result.Stderr)
	}

	// m4b-merge writes its output under -o using a path scheme; discovery is
	// scoped to this job's subtree. The 1s margin absorbs clock granularity
	// between start and mtime.
	if out, err := findNewestM4B(outputDir, start.Add(-time.Second)); err != nil {
		result.OutputWarning = "output file discovery failed: " + err.Error()
	} else if out == "" {
		result.OutputWarning = "no output .m4b found in output directory"
	} else if workDir != "" {
		// Relocate into the shared output tree, keeping the scheme-relative
		// path (e.g. Author/Title.m4b under {author}/{title}). The .m4b was
		// found only inside the per-job subtree: on relocation failure the
		// subtree is kept (recovery possible) and the run reports a real
		// failure instead of succeeding with no output file.
		rel, err := filepath.Rel(workDir, out)
		if err != nil {
			return result, fmt.Errorf("resolve output file %s relative to job dir %s: %w", out, workDir, err)
		}
		finalPath := filepath.Join(outputRoot, rel)
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
			return result, fmt.Errorf("create output path for %s: %w", finalPath, err)
		}
		if err := os.Rename(out, finalPath); err != nil {
			if err2 := os.MkdirAll(filepath.Dir(finalPath), 0o755); err2 == nil {
				// Cross-device rename: copy then delete.
				if data, rdErr := os.ReadFile(out); rdErr == nil {
					err = os.WriteFile(finalPath, data, 0o644)
					if err == nil {
						os.Remove(out)
					}
				} else {
					err = rdErr
				}
			}
			if err != nil {
				return result, fmt.Errorf("move output to final location %s: %w (job output kept at %s)", finalPath, err, workDir)
			}
		}
		result.OutputFile, _ = filepath.Abs(finalPath)
	} else {
		result.OutputFile = out
	}

	// Publication succeeded: the .m4b is at its final location, so the
	// per-job subtree is garbage to clean up.
	if workDir != "" {
		_ = os.RemoveAll(workDir)
	}

	return result, nil
}

// findNewestM4B walks dir recursively for *.m4b files and returns the absolute
// path of the newest one by mtime, ignoring files older than since. Returns
// "" (no error) when no qualifying file exists.
func findNewestM4B(dir string, since time.Time) (string, error) {
	var newest string
	var newestMtime time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".m4b") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(since) {
			return nil
		}
		if newest == "" || info.ModTime().After(newestMtime) {
			newest = path
			newestMtime = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", dir, err)
	}
	if newest == "" {
		return "", nil
	}
	return filepath.Abs(newest)
}

// MinimumM4bMergeVersion is the minimum required m4b-merge version.
const MinimumM4bMergeVersion = "1.0.0"

// Version runs the m4b-merge binary with --version and returns the last
// whitespace-separated token of its output as the reported version.
func (p *Processor) Version() (string, error) {
	binary, _, _, _, _, _ := p.snapshot()
	cmd := exec.Command(binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		if ex, ok := err.(*exec.Error); ok {
			return "", fmt.Errorf("m4b-merge binary not found (%s): %w", ex.Name, ex.Err)
		}
		return "", fmt.Errorf("m4b-merge --version failed: %w", err)
	}

	version := strings.Fields(strings.TrimSpace(string(out)))
	if len(version) == 0 {
		return "", fmt.Errorf("m4b-merge --version produced empty output")
	}
	return version[len(version)-1], nil // last token is the version string
}

// CheckVersion runs the m4b-merge binary with --version and ensures it is
// at least MinimumM4bMergeVersion. Returns the reported version and an
// error if the binary cannot be found, fails to report a version, or is
// too old.
func (p *Processor) CheckVersion() (string, error) {
	reported, err := p.Version()
	if err != nil {
		return "", err
	}
	if err := checkSemver(reported, MinimumM4bMergeVersion); err != nil {
		return "", fmt.Errorf("m4b-merge version %s is too old, need >= %s", reported, MinimumM4bMergeVersion)
	}
	return reported, nil
}

// StoreVersion records the last successfully reported m4b-merge version
// for display in the settings page.
func (p *Processor) StoreVersion(v string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version = v
}

// versionProbeTimeout bounds a single "--version" probe: a hanging binary
// must not stall the caller.
const versionProbeTimeout = 5 * time.Second

// UpdateVersion re-snapshots and stores the m4b-merge version. The binary
// is probed with "--version" OUTSIDE the write lock (a subprocess running
// under p.mu would block every run, ApplyConfig, and version reader for
// the whole probe; the timeout above guarantees it always finishes). The
// store is a compare-and-update under the write lock: it only applies the
// result if Binary still matches the binary that was executed, so an
// interleaved reconfiguration can never pair a stale version with a new
// binary. It returns the version that resulted from the probe
// ("unknown" when the binary cannot report one), which the caller pairs
// with its own binary snapshot.
func (p *Processor) UpdateVersion() string {
	p.mu.RLock()
	binary := p.Binary
	p.mu.RUnlock()

	if binary == "" {
		// Nothing to execute; never spawn the bare "" command.
		return p.storeLinkedVersion(binary, "unknown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()

	// Execute, retrying the transient ETXTBSY: Linux refuses to exec a
	// file while ANY fd in the process is open for writing it, so a
	// sibling thread's fork can carry the writer's O_CLOEXEC fd past the
	// writer's close and tax a freshly written binary until that
	// inherited fd dies (golang/go#22315). Each attempt is a fresh cmd,
	// because a failed Start marks the cmd consumed; the probe's context
	// deadline bounds the whole retry sequence.
	var (
		reported  string
		stdoutBuf bytes.Buffer
	)
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(50*attempt) * time.Millisecond)
		}
		cmd := exec.CommandContext(ctx, binary, "--version")
		// Own process group: ctx cancellation must kill the whole tree,
		// not just the direct child. m4b_merge_binary is user-settable
		// and may be a script wrapper that spawns children in general; a
		// grandchild that inherits the stdout pipe would otherwise hold
		// the probe open long after the child itself is killed, stalling
		// the settings request past the deadline.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
				if err == syscall.ESRCH {
					// Already gone: nothing for os/exec to do.
					return os.ErrProcessDone
				}
				return err
			}
			return nil
		}
		// Guard against any writer we could not kill: force Wait to
		// return shortly after cancel instead of blocking on inherited
		// pipes.
		cmd.WaitDelay = 5 * time.Second
		// os/exec owns the drain: with buffers wired directly as the
		// child's stdout/stderr, exec internally reads both streams
		// concurrently and guarantees they are fully drained (and the
		// buffers valid) once Run returns.
		stdoutBuf.Reset()
		var stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Run()
		switch {
		case err == nil:
			version := strings.Fields(strings.TrimSpace(stdoutBuf.String()))
			if len(version) > 0 {
				reported = version[len(version)-1] // last token is the version string
			}
		case !errors.Is(err, syscall.ETXTBSY) || attempt >= 2:
			// Missing binary, permission denied, nonzero exit,
			// timeout, or empty output: record why the probe
			// produced no version.
			log.Printf("Warning: m4b-merge version probe for binary %s failed: %v%s",
				binary, err, formattedStderr(strings.TrimSpace(stderrBuf.String())))
		default:
			continue // transient ETXTBSY: let a later attempt retry
		}
		break
	}
	if reported == "" {
		reported = "unknown"
	}
	return p.storeLinkedVersion(binary, reported)
}

// formattedStderr appends the child's stderr as a suffix to a log line.
func formattedStderr(stderr string) string {
	if stderr == "" {
		return ""
	}
	return "; stderr: " + stderr
}

// storeLinkedVersion stores version under the write lock only if the
// binary field still matches the value the version was captured from,
// so a reconfiguration that landed while the probe ran is never
// shadowed by a result taken against the previous binary. It returns
// the version now in effect (which may be a concurrent writer's) so
// the result always corresponds to a consistent binary/version pair.
func (p *Processor) storeLinkedVersion(snapshotBinary, version string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Binary != snapshotBinary {
		return p.version
	}
	p.version = version
	return p.version
}

// ReadVersion returns the last recorded m4b-merge version, or "" if none
// has been stored.
func (p *Processor) ReadVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// checkSemver returns nil if got >= want. Both are "MAJOR.MINOR.PATCH" strings.
// Short forms ("2", "v2", "1.0") are tolerated: missing components count as 0,
// and a leading "v" is ignored.
func checkSemver(got, want string) error {
	parse := func(v string) (int, int, int) {
		parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
		atoiPart := func(i int) int {
			if i >= len(parts) {
				return 0
			}
			n, _ := strconv.Atoi(parts[i])
			return n
		}
		return atoiPart(0), atoiPart(1), atoiPart(2)
	}

	gMaj, gMin, gPat := parse(got)
	wMaj, wMin, wPat := parse(want)

	if gMaj > wMaj {
		return nil
	}
	if gMaj < wMaj {
		return fmt.Errorf("%s < %s", got, want)
	}
	if gMin > wMin {
		return nil
	}
	if gMin < wMin {
		return fmt.Errorf("%s < %s", got, want)
	}
	if gPat < wPat {
		return fmt.Errorf("%s < %s", got, want)
	}
	return nil
}
