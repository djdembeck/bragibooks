package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"time"
)

// Capabilities is the engine's --capabilities document: the token vocabulary
// the path resolver accepts (with human labels and examples for the UI
// picker) plus the supported field/cover/chapter policy values. Bragi owns
// NO token registry of its own: the picker, the examples, and the
// save-time format validation all derive from this. A stale local registry
// was the source of the {asin} drift this cutover removes.
type Capabilities struct {
	SchemaVersion     int              `json:"schema_version"`
	ResolverVersion   string           `json:"resolver_version"`
	DefaultPathFormat string           `json:"default_path_format"`
	PathTokens        []PathToken      `json:"path_tokens"`
	FieldPolicies     FieldPolicies    `json:"field_policies"`
	CoverPolicies     PolicyVariantSet `json:"cover_policies"`
	ChapterPolicies   PolicyVariantSet `json:"chapter_policies"`
}

// PathToken is one supported path placeholder with the text Bragi shows for
// it. Name is the token (without braces); Value/Example are the engine's own
// description and sample.
type PathToken struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Example string `json:"example"`
}

// FieldPolicies reports the scalar write-policy vocabulary and the writable
// scalar fields.
type FieldPolicies struct {
	Values       []string `json:"values"`
	ScalarFields []string `json:"scalar_fields"`
}

// PolicyVariantSet reports the allowed variant strings for the cover and
// chapter policies (the object forms are additional engine features not
// surfaced as a vocabulary).
type PolicyVariantSet struct {
	Constraint string   `json:"constraint"`
	Variants   []string `json:"variants"`
}

// pathTokenSet returns the set of accepted token names.
func (c *Capabilities) pathTokenSet() map[string]bool {
	set := make(map[string]bool, len(c.PathTokens))
	for _, t := range c.PathTokens {
		set[t.Name] = true
	}
	return set
}

// capabilitiesProbeTimeout bounds a single --capabilities probe like the
// version probe: a hanging binary must not stall the caller.
const capabilitiesProbeTimeout = 5 * time.Second

// ProbeCapabilities runs the engine's --capabilities and parses the
// document. It reuses the binary-snapshot discipline of the version probe
// (executed outside the write lock, compared back against the binary it ran).
// Returns the parsed capabilities plus the reported version is NOT included
// here; callers pair it with the version check. A probe failure returns an
// error naming why, so a misconfigured or too-old binary is visible at
// startup rather than failing the first job.
func (p *Processor) ProbeCapabilities(ctx context.Context) (*Capabilities, error) {
	p.mu.RLock()
	binary := p.Binary
	p.mu.RUnlock()

	if binary == "" {
		return nil, fmt.Errorf("m4b-merge binary is not set")
	}

	cmd := exec.CommandContext(ctx, binary, "--capabilities")
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
		return nil, fmt.Errorf("m4b-merge --capabilities start: %w", err)
	}
	waitErr := cmd.Wait()
	tree.finish()
	if waitErr != nil {
		return nil, fmt.Errorf("m4b-merge --capabilities: %w%s", waitErr, formattedStderr(stripEscapeSequences(stderrBuf.String())))
	}

	var caps Capabilities
	if err := json.Unmarshal(stdoutBuf.Bytes(), &caps); err != nil {
		return nil, fmt.Errorf("parse capabilities: %w", err)
	}
	if len(caps.PathTokens) == 0 {
		return nil, fmt.Errorf("capabilities reported no path tokens (engine too old?)")
	}
	return &caps, nil
}

// CheckCapabilities runs the engine version gate and the capabilities probe
// together and verifies the protocol Bragi depends on: the accepted schema
// version, a non-empty token vocabulary, the canonical {book_id} token in
// supplied mode (#480), and the plural people tokens (#479). Version output
// alone is not enough to assume protocol support, so the capabilities are
// asserted explicitly. Returns the parsed capabilities on success.
func (p *Processor) CheckCapabilities(ctx context.Context) (*Capabilities, error) {
	if _, err := p.CheckVersion(); err != nil {
		return nil, err
	}
	caps, err := p.ProbeCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	if caps.SchemaVersion != AcceptedEngineSchemaVersion {
		return nil, fmt.Errorf("engine schema_version %d is unsupported, need %d", caps.SchemaVersion, AcceptedEngineSchemaVersion)
	}
	tokens := caps.pathTokenSet()
	if !tokens["book_id"] {
		return nil, fmt.Errorf("engine capabilities lack the {book_id} token; an m4b-merge >= 2.2.0 is required for the canonical book-id naming token in supplied mode")
	}
	if !tokens["authors"] || !tokens["narrators"] {
		return nil, fmt.Errorf("engine capabilities lack plural people tokens; an m4b-merge >= 2.1.1 is required")
	}
	// A missing probe must never be silently swallowed into a start: log so
	// an operator sees a capability that regressed at boot.
	log.Printf("engine capabilities verified: schema %d, resolver %s, %d tokens", caps.SchemaVersion, caps.ResolverVersion, len(caps.PathTokens))
	return caps, nil
}
