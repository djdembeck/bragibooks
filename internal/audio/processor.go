package audio

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var jobBeginRe = regexp.MustCompile(`job begin:\s+(.+)$`)

// Processor invokes the m4b-merge Rust CLI binary for audiobook processing.
type Processor struct {
	Binary       string
	OutputDir    string
	CompletedDir string
	NumCPUs      int
	PathFormat   string
	LogLevel     string
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

// Run executes m4b-merge with the given input file paths.
// Returns the output .m4b file path, or an error.
func (p *Processor) Run(ctx context.Context, inputPaths []string, asin string) (*ProcessResult, error) {
	args := []string{
		"-o", p.OutputDir,
		"-p", p.PathFormat,
		"--num-cpus", strconv.Itoa(p.NumCPUs),
		"--log-level", p.LogLevel,
	}
	if p.CompletedDir != "" {
		args = append(args, "--completed-directory", p.CompletedDir)
	}
	if asin != "" {
		args = append(args, "-a", asin)
	}
	// m4b-merge (clap) requires one -i per input; a single -i with multiple
	// values errors with "unexpected argument".
	for _, in := range inputPaths {
		args = append(args, "-i", in)
	}

	cmd := exec.CommandContext(ctx, p.Binary, args...)
	// Own process group: ctx cancellation must kill the whole tree, not just
	// the direct child. m4b-merge is a script wrapper here (and may spawn
	// children in general); a grandchild that inherits the stdout pipe would
	// otherwise hold io.ReadAll open long after the child itself is killed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return syscall.ESRCH // nothing further for os/exec to kill
	}
	// Guard against any writer we could not kill: force Wait to return
	// shortly after cancel instead of blocking on inherited pipes.
	cmd.WaitDelay = 5 * time.Second

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("m4b-merge start: %w", err)
	}

	stdoutBytes, _ := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		return &ProcessResult{
			Stdout: string(stdoutBytes),
			Stderr: string(stderrBytes),
		}, fmt.Errorf("m4b-merge: %w: %s", err, stderrBytes)
	}

	result := &ProcessResult{
		Stdout: string(stdoutBytes),
		Stderr: string(stderrBytes),
	}

	// m4b-merge writes its output under -o using a path scheme; discover it
	// by walking the output dir for the newest .m4b written during the run.
	// The 1s margin absorbs clock granularity between start and mtime.
	if out, err := findNewestM4B(p.OutputDir, start.Add(-time.Second)); err != nil {
		result.OutputWarning = "output file discovery failed: " + err.Error()
	} else if out == "" {
		result.OutputWarning = "no output .m4b found in output directory"
	} else {
		result.OutputFile = out
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

// CheckVersion runs the m4b-merge binary with --version and ensures it is
// at least MinimumM4bMergeVersion. Returns an error if the binary cannot
// be found, fails to report a version, or is too old.
func (p *Processor) CheckVersion() error {
	cmd := exec.Command(p.Binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		if ex, ok := err.(*exec.Error); ok {
			return fmt.Errorf("m4b-merge binary not found (%s): %w", ex.Name, ex.Err)
		}
		return fmt.Errorf("m4b-merge --version failed: %w", err)
	}

	version := strings.Fields(strings.TrimSpace(string(out)))
	if len(version) == 0 {
		return fmt.Errorf("m4b-merge --version produced empty output")
	}
	reported := version[len(version)-1] // last token is the version string

	if err := checkSemver(reported, MinimumM4bMergeVersion); err != nil {
		return fmt.Errorf("m4b-merge version %s is too old, need >= %s", reported, MinimumM4bMergeVersion)
	}
	return nil
}

// checkSemver returns nil if got >= want. Both are "MAJOR.MINOR.PATCH" strings.
func checkSemver(got, want string) error {
	parse := func(v string) (int, int, int) {
		parts := strings.Split(v, ".")
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		var patch int
		if len(parts) > 2 {
			patch, _ = strconv.Atoi(parts[2])
		}
		return major, minor, patch
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
