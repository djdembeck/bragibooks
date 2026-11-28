package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
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
	return p.ProbeCapabilitiesFor(ctx, binary)
}

// ProbeCapabilitiesFor runs --capabilities against the EXACT binary path
// named, so a caller can probe a candidate binary before it becomes live.
func (p *Processor) ProbeCapabilitiesFor(ctx context.Context, binary string) (*Capabilities, error) {
	if binary == "" {
		return nil, fmt.Errorf("m4b-merge binary is not set")
	}

	// A documented probe timeout is only real if it is applied here: a
	// hanging --capabilities must die on its own clock, not keep the caller
	// (settings save, pre-launch gate) blocked for the caller's whole context.
	ctx, cancel := context.WithTimeout(ctx, capabilitiesProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--capabilities")
	tree, err := newProcessTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("confine process tree: %w", err)
	}
	cmd.Cancel = func() error {
		return tree.kill()
	}
	cmd.WaitDelay = 5 * time.Second

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		tree.finish()
		return nil, fmt.Errorf("m4b-merge --capabilities start: %w", err)
	}
	// Constrain the child to the tree NOW, before it can spawn
	// descendants that would escape a cancel-time assignment.
	if err := tree.start(cmd); err != nil {
		tree.finish()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("confine process tree: %w", err)
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
	return p.validateCapabilities(caps)
}

// CheckCapabilitiesFor runs the same version + capabilities + protocol gate
// as CheckCapabilities, but against the EXACT binary path named. The settings
// save uses it before accepting a re-pointed engine binary: the path being
// saved is the one that must pass, not whatever p.Binary holds today.
func (p *Processor) CheckCapabilitiesFor(ctx context.Context, binary string) (*Capabilities, error) {
	if _, err := p.CheckVersionFor(ctx, binary); err != nil {
		return nil, err
	}
	caps, err := p.ProbeCapabilitiesFor(ctx, binary)
	if err != nil {
		return nil, err
	}
	return p.validateCapabilities(caps)
}

// validateCapabilities asserts the protocol Bragi depends on: the accepted
// schema version, a non-empty token vocabulary, the canonical {book_id}
// token in supplied mode (#480), and the plural people tokens (#479).
func (p *Processor) validateCapabilities(caps *Capabilities) (*Capabilities, error) {
	if caps.SchemaVersion != AcceptedEngineSchemaVersion {
		return nil, fmt.Errorf("engine schema_version %d is unsupported, need %d", caps.SchemaVersion, AcceptedEngineSchemaVersion)
	}
	// The resolver identity is part of the protocol: an empty (missing)
	// resolver_version decodes as "" and would pass every identity
	// comparison against a job captured under a later resolver, so the
	// gate must require the pinned identity explicitly. A mismatch means
	// the binary speaks different naming semantics than the snapshots
	// already in the queue — refuse startup, do not guess.
	if caps.ResolverVersion != PinnedEngineResolverVersion {
		return nil, fmt.Errorf("engine resolver_version %q is unsupported, need %q", caps.ResolverVersion, PinnedEngineResolverVersion)
	}
	tokens := caps.pathTokenSet()
	if !tokens["book_id"] {
		return nil, fmt.Errorf("engine capabilities lack the {book_id} token; an m4b-merge >= 2.2.0 is required for the canonical book-id naming token in supplied mode")
	}
	if !tokens["authors"] || !tokens["narrators"] {
		return nil, fmt.Errorf("engine capabilities lack plural people tokens; an m4b-merge >= 2.1.1 is required")
	}
	// The per-field write selection (#518) renders its checkbox list from
	// this vocabulary: a supported field the engine dropped from
	// scalar_fields would be saved into the settings and silently ignored
	// by every job, so the boot gate asserts every Bragi-supported field
	// is a capability. An engine without the scalar policy document at all
	// is the same regression: a selection could not be honored.
	if len(caps.FieldPolicies.ScalarFields) == 0 {
		return nil, fmt.Errorf("engine capabilities carry no scalar_fields; an m4b-merge >= 2.2.0 is required for the per-field metadata write selection")
	}
	// The scalar policy vocabulary must include every value Bragi emits
	// into a job's write policies: write (selected fields with values),
	// preserve (deselected / unavailable), and clear (the per-book
	// editor's explicit clears). Listing scalar fields alone is not
	// enough: an engine that advertised the fields but dropped one of
	// these values would pass this gate and only reject the job at run
	// time.
	for _, v := range []string{WritePolicyValueWrite, WritePolicyValuePreserve, WritePolicyValueClear} {
		if !policyVariant(caps.FieldPolicies.Values, v) {
			return nil, fmt.Errorf("engine field_policies do not advertise %q; a job with that write policy cannot be honored (field policy values: %s)", v, strings.Join(caps.FieldPolicies.Values, ", "))
		}
	}
	for _, f := range SupportedWriteFields {
		found := false
		for _, sf := range caps.FieldPolicies.ScalarFields {
			if sf == f {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("engine capabilities lack scalar field %q; the per-field metadata write selection cannot be honored (engine scalar fields: %s)", f, strings.Join(caps.FieldPolicies.ScalarFields, ", "))
		}
	}
	// A preserved cover and a cleared cover each serialize a distinct
	// policy string into the job file, so both advertised cover variants
	// are required: an engine that dropped one would boot cleanly (and
	// accept a binary swap) only to reject the job at run time.
	//
	// A WRITTEN cover is the exception: Bragi never serializes it as the
	// bare string "write". It emits "write_url" (a write_url pin with no
	// URL) or the object form {"write": "/absolute/path"} (a staged local
	// cover file), and the engine advertises only "write_url" in
	// cover_policies.variants while naming the object form in the
	// constraint text. Requiring "write" therefore demands a spelling the
	// engine cannot advertise and fails every boot on the pinned
	// m4b-merge v2.2.x.
	for _, v := range []string{WritePolicyValuePreserve, WritePolicyValueClear} {
		if !policyVariant(caps.CoverPolicies.Variants, v) {
			return nil, fmt.Errorf("engine cover_policies do not advertise %q; a job with that cover policy cannot be honored (cover variants: %s)", v, strings.Join(caps.CoverPolicies.Variants, ", "))
		}
	}
	for _, v := range []string{WritePolicyValueWrite, WritePolicyValuePreserve} {
		if !policyVariant(caps.ChapterPolicies.Variants, v) {
			return nil, fmt.Errorf("engine chapter_policies do not advertise %q; a job with that chapter policy cannot be honored (chapter variants: %s)", v, strings.Join(caps.ChapterPolicies.Variants, ", "))
		}
	}
	// A missing probe must never be silently swallowed into a start: log so
	// an operator sees a capability that regressed at boot.
	log.Printf("engine capabilities verified: schema %d, resolver %s, %d tokens, %d scalar fields", caps.SchemaVersion, caps.ResolverVersion, len(caps.PathTokens), len(caps.FieldPolicies.ScalarFields))
	return caps, nil
}

// policyVariant reports whether the engine's advertised variant list for a
// policy path includes the named policy string.
func policyVariant(variants []string, want string) bool {
	for _, v := range variants {
		if v == want {
			return true
		}
	}
	return false
}
