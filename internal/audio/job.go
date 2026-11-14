package audio

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file is Bragi's typed mirror of the m4b-merge engine's structured
// job protocol (schema_version 1, resolver_version "1") and its machine
// execution result. The engine is the authority for the wire contract; these
// types must stay field-for-field compatible with the pinned engine image.
// The job is always input_supplied: Bragi never performs a worker-time
// catalog lookup (that is the point of the authoritative snapshot cutover),
// so input_provider is deliberately not expressible here.

// JobSchemaVersion is the only protocol version Bragi supports.
const JobSchemaVersion = 1

// AcceptedEngineSchemaVersion is the job schema_version the worker requires
// a published result to report.
const AcceptedEngineSchemaVersion = 1

// EngineIdentity is the schema/resolver pair that names the naming semantics
// a job was captured under. It is snapshotted at queue time and re-checked
// against the live engine before execution: a changed resolver must never
// silently run a job under different naming semantics.
type EngineIdentity struct {
	SchemaVersion   int    `json:"schema_version"`
	ResolverVersion string `json:"resolver_version"`
}

// JobChapter mirrors engine JobChapter: one explicit chapter for the
// chapters write policy, integer-millisecond times.
type JobChapter struct {
	Title          string `json:"title"`
	StartMillis    uint64 `json:"start_millis"`
	DurationMillis uint64 `json:"duration_millis"`
}

// SuppliedMetadata is the effective supported metadata for a job, in the
// engine's input_supplied shape (field-for-field; the engine's SuppliedInput
// is deny_unknown_fields, so this mirror must never gain a field the engine
// does not accept). People are ORDERED arrays: the engine joins them with
// ", " (comma-space) and orders segments by array position, so the order
// Bragi captured is the order the output is named with.
//
// BookID is the canonical AudiobookDB book id that feeds the {book_id}
// naming token (#480). Since engine 2.2.0 supplied jobs accept it directly:
// it overrides no catalog fetch (there is none in supplied mode) and the
// provider mode keeps its catalog id authoritative.
type SuppliedMetadata struct {
	Title          string   `json:"title"`
	Subtitle       *string  `json:"subtitle,omitempty"`
	Authors        []string `json:"authors"`
	Narrators      []string `json:"narrators,omitempty"`
	SeriesName     *string  `json:"series_name,omitempty"`
	SeriesPosition *string  `json:"series_position,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Genres         []string `json:"genres,omitempty"`
	Year           *int     `json:"year,omitempty"`
	BookID         *string  `json:"book_id,omitempty"`
}

// WritePolicies names the per-atom operation for the scalar metadata fields.
// The engine DEFAULT is "preserve" (leave the source's tag untouched), so the
// all-supported-available write policy the snapshot cutover relies on must be
// set EXPLICITLY: every supported field is "write" (the tag comes from the
// effective supplied metadata, the same values that name the output path).
type WritePolicies struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Authors     string `json:"authors"`
	Narrators   string `json:"narrators"`
	Series      string `json:"series"`
	Description string `json:"description"`
	Genres      string `json:"genres"`
	Year        string `json:"year"`
}

// AllSupportedWritePolicyFor derives the all-supported-available write
// policy for one snapshot: every field that carries a value is written from
// the snapshot (the tag comes from the effective supplied metadata, the same
// values that name the output path), and a field with no value keeps the
// source tag (preserve). The engine rejects a write whose effective value is
// blank, so an unconditional all-write policy fails for partial snapshots.
func AllSupportedWritePolicyFor(m SuppliedMetadata) WritePolicies {
	p := WritePolicies{
		Title:       "preserve",
		Subtitle:    "preserve",
		Authors:     "preserve",
		Narrators:   "preserve",
		Series:      "preserve",
		Description: "preserve",
		Genres:      "preserve",
		Year:        "preserve",
	}
	if strings.TrimSpace(m.Title) != "" {
		p.Title = "write"
	}
	if m.Subtitle != nil && strings.TrimSpace(*m.Subtitle) != "" {
		p.Subtitle = "write"
	}
	if len(m.Authors) > 0 {
		p.Authors = "write"
	}
	if len(m.Narrators) > 0 {
		p.Narrators = "write"
	}
	if m.SeriesName != nil && strings.TrimSpace(*m.SeriesName) != "" {
		p.Series = "write"
	}
	if m.Description != nil && strings.TrimSpace(*m.Description) != "" {
		p.Description = "write"
	}
	if len(m.Genres) > 0 {
		p.Genres = "write"
	}
	if m.Year != nil {
		p.Year = "write"
	}
	return p
}

// JobFile is the complete structured job handed to the engine. OutputRoot is
// the engine's claim root AND the final destination: the engine publishes
// every artifact directly under it with atomic no-replace links. Cover and
// Chapters are json.RawMessage because they are struct-tagged policy enums:
// cover "clear" (the supplied default) or {"write":"<abs path>"} for a
// staged local file; chapters {"write":{"chapters":[...]}} (an empty list is
// an explicit clear, a populated list embeds the selected release's
// chapters).
type JobFile struct {
	SchemaVersion int              `json:"schema_version"`
	Inputs        []string         `json:"inputs"`
	InputSupplied SuppliedMetadata `json:"input_supplied"`
	OutputRoot    string           `json:"output_root"`
	PathFormat    string           `json:"path_format"`
	Policies      WritePolicies    `json:"-"`
	CompletedDir  *string          `json:"completed_directory,omitempty"`
	Cover         json.RawMessage  `json:"cover,omitempty"`
	Chapters      json.RawMessage  `json:"chapters,omitempty"`
}

// JobFileJSON serializes a job for the engine. The WritePolicies fields
// serialize as the engine's flat top-level policy keys (title, subtitle,
// authors, ...) via an embedded map, which is exactly the shape its
// deny_unknown_fields Job struct expects.
func JobFileJSON(jf JobFile) ([]byte, error) {
	// Build the flat policy object.
	flat := map[string]string{
		"title":       jf.Policies.Title,
		"subtitle":    jf.Policies.Subtitle,
		"authors":     jf.Policies.Authors,
		"narrators":   jf.Policies.Narrators,
		"series":      jf.Policies.Series,
		"description": jf.Policies.Description,
		"genres":      jf.Policies.Genres,
		"year":        jf.Policies.Year,
	}
	// Marshal the non-policy fields into a map, then add the flat keys.
	var base map[string]any
	b, err := json.Marshal(struct {
		SchemaVersion int              `json:"schema_version"`
		Inputs        []string         `json:"inputs"`
		InputSupplied SuppliedMetadata `json:"input_supplied"`
		OutputRoot    string           `json:"output_root"`
		PathFormat    string           `json:"path_format"`
		CompletedDir  *string          `json:"completed_directory,omitempty"`
		Cover         json.RawMessage  `json:"cover,omitempty"`
		Chapters      json.RawMessage  `json:"chapters,omitempty"`
	}{
		jf.SchemaVersion, jf.Inputs, jf.InputSupplied, jf.OutputRoot,
		jf.PathFormat, jf.CompletedDir, jf.Cover, jf.Chapters,
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &base); err != nil {
		return nil, err
	}
	for k, v := range flat {
		base[k] = v
	}
	return json.Marshal(base)
}

// machineResult is the engine's single machine-execution document: one
// status plus every produced artifact per group, with diagnosed errors. The
// worker validates and publishes from this, never by scanning for the
// newest file.
type machineResult struct {
	SchemaVersion   int    `json:"schema_version"`
	ResolverVersion string `json:"resolver_version"`
	Status          string `json:"status"`
	// Error is the setup-level diagnosed error when the run failed before
	// producing any group outcome.
	Error  *string  `json:"error"`
	Groups []mGroup `json:"groups"`
}

type mGroup struct {
	Group      string      `json:"group"`
	Outcome    string      `json:"outcome"`
	Artifacts  []mArtifact `json:"artifacts"`
	FilesMoved bool        `json:"files_moved"`
	Error      *string     `json:"error"`
}

type mArtifact struct {
	RelativePath string `json:"relative_path"`
	Kind         string `json:"kind"`
	Exists       bool   `json:"exists"`
}

// jobPreviewResult is the engine's non-mutating preview document: the exact
// sanitized relative path the run WOULD produce plus the effective metadata
// it resolved. Preview is exact in supplied mode (no catalog request).
type jobPreviewResult struct {
	SchemaVersion   int              `json:"schema_version"`
	ResolverVersion string           `json:"resolver_version"`
	PathFormat      string           `json:"path_format"`
	RelativePath    string           `json:"relative_path"`
	Metadata        *PreviewMetadata `json:"metadata"`
}

// PreviewMetadata is the effective resolved metadata the preview reports. It
// mirrors the engine's BookMetadata projection for the supported fields.
type PreviewMetadata struct {
	MetadataID     string   `json:"metadata_id"`
	BookID         string   `json:"book_id"`
	Title          string   `json:"title"`
	Subtitle       *string  `json:"subtitle"`
	Authors        []string `json:"authors"`
	Narrators      []string `json:"narrators"`
	SeriesName     *string  `json:"series_name"`
	SeriesPosition *string  `json:"series_position"`
	Description    string   `json:"description"`
	Genres         []string `json:"genres"`
	Year           *int     `json:"year"`
}

// JobPreviewResponse is the JSON shape served to the Match and Settings
// preview surfaces. RelativePath is the path execution will produce
// (sanitized); it is distinct from a successful filesystem publication.
type JobPreviewResponse struct {
	Engine       EngineIdentity   `json:"engine"`
	RelativePath string           `json:"relative_path"`
	Metadata     *PreviewMetadata `json:"metadata"`
}

// parseMachineResult decodes and sanity-checks an engine machine result.
func parseMachineResult(b []byte) (machineResult, error) {
	var r machineResult
	if err := json.Unmarshal(b, &r); err != nil {
		return machineResult{}, fmt.Errorf("parse machine result: %w", err)
	}
	return r, nil
}

// parsePreviewResult decodes an engine preview result.
func parsePreviewResult(b []byte) (jobPreviewResult, error) {
	var r jobPreviewResult
	if err := json.Unmarshal(b, &r); err != nil {
		return jobPreviewResult{}, fmt.Errorf("parse preview result: %w", err)
	}
	return r, nil
}
