package audio

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

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
func NewProcessor(binary, outputDir, completedDir string, numCPUs int, pathFormat string) *Processor {
	return &Processor{
		Binary:       binary,
		OutputDir:    outputDir,
		CompletedDir: completedDir,
		NumCPUs:      numCPUs,
		PathFormat:   pathFormat,
		LogLevel:     "info",
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
func (p *Processor) Run(ctx context.Context, inputPaths []string) (*ProcessResult, error) {
	args := []string{
		"-o", p.OutputDir,
		"-p", p.PathFormat,
		"--num-cpus", strconv.Itoa(p.NumCPUs),
		"--log-level", p.LogLevel,
	}
	if p.CompletedDir != "" {
		args = append(args, "--completed-directory", p.CompletedDir)
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

	return &ProcessResult{
		Stdout: string(stdoutBytes),
		Stderr: string(stderrBytes),
	}, nil
}