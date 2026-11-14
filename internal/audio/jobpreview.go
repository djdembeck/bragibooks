package audio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// Preview runs the engine's non-mutating job preview
// (--job <file> --job-preview) for a supplied-metadata snapshot and the
// chosen path format. It resolves the exact sanitized relative output path
// and the effective metadata, creates no directories, writes no media, and
// reserves no names. In supplied mode the preview is EXACT: no catalog
// request is made, so it is safe to call with the catalog unavailable.
//
// The job file is written to a temp file in the OS temp dir and removed after
// the probe. OutputRoot is a placeholder path the engine never touches during
// preview.
func (p *Processor) Preview(ctx context.Context, meta SuppliedMetadata, pathFormat string) (*JobPreviewResponse, error) {
	binary, _, _, _, logLevel, _ := p.snapshot()

	jf := JobFile{
		SchemaVersion: JobSchemaVersion,
		Inputs:        []string{"/nonexistent/input.m4a"},
		InputSupplied: meta,
		OutputRoot:    os.TempDir(),
		PathFormat:    NormalizeEnginePathFormat(pathFormat),
		Policies:      AllSupportedWritePolicyFor(meta),
	}
	data, err := JobFileJSON(jf)
	if err != nil {
		return nil, fmt.Errorf("serialize preview job: %w", err)
	}

	f, err := os.CreateTemp("", "bragi-preview-*.json")
	if err != nil {
		return nil, fmt.Errorf("create preview job file: %w", err)
	}
	probeFile := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(probeFile)
		return nil, fmt.Errorf("write preview job file: %w", err)
	}
	f.Close()
	defer os.Remove(probeFile)

	// The preview job file lives in a temp dir the client should never see:
	// the engine's stderr (and any start failure) can echo the path, so every
	// user-facing error from this function is redacted of it and its parent.
	redact := func(s string) string {
		s = strings.ReplaceAll(s, probeFile, "<temp>")
		if td := os.TempDir(); td != "" {
			s = strings.ReplaceAll(s, td, "<temp>")
		}
		return s
	}

	// The preview is non-mutating, so the CPU limit is irrelevant here: 0
	// lets the engine apply its own default.
	cmd := newEngineCommand(ctx, binary, probeFile, logLevel, 0, true)
	tree := newProcessTree(cmd)
	cmd.Cancel = func() error {
		if err := tree.start(cmd); err != nil {
			return err
		}
		return tree.kill()
	}
	cmd.WaitDelay = 5 * time.Second
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("m4b-merge preview start: %s", redact(err.Error()))
	}
	waitErr := cmd.Wait()
	tree.finish()
	if waitErr != nil {
		return nil, fmt.Errorf("m4b-merge --job-preview: %s", redact(formattedStderr(stripEscapeSequences(stderrBuf.String()))))
	}

	doc, err := parsePreviewResult(stdoutBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s", redact(err.Error()))
	}
	return &JobPreviewResponse{
		Engine: EngineIdentity{
			SchemaVersion:   doc.SchemaVersion,
			ResolverVersion: doc.ResolverVersion,
		},
		RelativePath: doc.RelativePath,
		Metadata:     doc.Metadata,
	}, nil
}
