package audio

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	OutputFile string
	Stdout     string
	Stderr     string
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
	args = append(args, "-i")
	args = append(args, inputPaths...)

	cmd := exec.CommandContext(ctx, p.Binary, args...)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

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

	return result, nil
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