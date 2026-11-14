package audio

import (
	"fmt"
	"strings"
)

// SupportedWriteFields is the set of logical scalar metadata fields Bragi's
// per-field write selection (#518) accepts, in the engine's capability
// document's scalar_fields order. It mirrors WritePolicies field for field;
// the boot capability gate (validateCapabilities) asserts the live engine
// supports every one of them, so a saved selection can never be silently
// ignored by an engine that dropped a field.
var SupportedWriteFields = []string{
	"title", "subtitle", "authors", "narrators", "series", "description", "genres", "year",
}

// FieldCover is the selection member for the cover write policy. Cover is a
// separate engine policy path (local-file embedding), not one of the scalar
// field policies, but it is selectable alongside them in the same setting.
const FieldCover = "cover"

// SelectableFields is the complete vocabulary the write-selection setting
// accepts: the scalar fields plus the cover. Chapters are a separate engine
// policy with their own setting (the chapter write policy), never a member
// of this list.
var SelectableFields = append(append([]string{}, SupportedWriteFields...), FieldCover)

// WritePolicyValue is the per-field policy vocabulary the engine accepts for
// Bragi's derived job policies. Bragi only ever derives write or preserve:
// explicit clear is the per-book editor's operation (B3), never something a
// settings selection implies.
const (
	WritePolicyValueWrite    = "write"
	WritePolicyValuePreserve = "preserve"
)

// ChaptersPolicyValueWrite / ...Preserve are the derived chapter policy
// strings frozen into a job: write embeds the selected release's chapters
// (or the engine's resolved baseline) and writes the chapters.txt sidecar;
// preserve skips all chapter mutation (single natively-copyable input only —
// the engine rejects it with an explicit diagnosed error otherwise).
const (
	ChaptersPolicyValueWrite    = "write"
	ChaptersPolicyValuePreserve = "preserve"
)

// NormalizeSelectableFields validates an explicit write selection: trims
// each entry, rejects a field the vocabulary does not know (naming it), and
// de-dupes. The empty result is a VALID explicit selection ("write nothing")
// and is returned as a non-nil empty slice: the distinction between an
// explicit empty selection and the default (nil = all-supported-available)
// is load-bearing and must survive every round trip.
func NormalizeSelectableFields(fields []string) ([]string, error) {
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		name := strings.TrimSpace(f)
		if name == "" {
			continue
		}
		if !fieldVocabulary[name] {
			return nil, fmt.Errorf("unsupported metadata field %q (supported: %s)", name, strings.Join(SelectableFields, ", "))
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

var fieldVocabulary = func() map[string]bool {
	m := make(map[string]bool, len(SelectableFields))
	for _, f := range SelectableFields {
		m[f] = true
	}
	return m
}()

// ValidateWriteSelectionForEngine checks an explicit write selection against
// the verified engine capabilities: every scalar field in the selection must
// appear in the engine's scalar_fields (the engine is the authority for what
// it can write), and the capability document must be present. This is the
// boot gate that makes a field dropped by an unsupported or re-pointed
// engine fail startup with a nameable diagnosis instead of being silently
// ignored by every job. Cover is not checked here: it is a separate engine
// policy path with no scalar_fields entry, and SelectableFields already
// constrains it.
func ValidateWriteSelectionForEngine(fields []string, caps *Capabilities) error {
	if caps == nil {
		return fmt.Errorf("engine capabilities not verified")
	}
	supported := make(map[string]bool, len(caps.FieldPolicies.ScalarFields))
	for _, f := range caps.FieldPolicies.ScalarFields {
		supported[f] = true
	}
	for _, f := range fields {
		name := strings.TrimSpace(f)
		if name == "" {
			continue
		}
		if name == FieldCover {
			continue
		}
		if !supported[name] {
			return fmt.Errorf("engine does not support metadata field %q (engine scalar fields: %s)", name, strings.Join(caps.FieldPolicies.ScalarFields, ", "))
		}
	}
	return nil
}

// WritePolicyFor derives the per-scalar-field engine policy for one snapshot
// under an EXPLICIT selection: every selected field that carries a value in
// the snapshot is written, and everything else — deselected, or selected but
// unavailable — is preserved. Writing a blank value is refused by the engine
// at job validation, so a selected-but-unavailable value degrades to
// preserve, never to an inferred clear.
//
// A nil selection is NOT handled here: it is the default
// all-supported-available baseline and derives via AllSupportedWritePolicyFor.
func WritePolicyFor(selection []string, m SuppliedMetadata) WritePolicies {
	selected := make(map[string]bool, len(selection))
	for _, f := range selection {
		if f != FieldCover {
			selected[f] = true
		}
	}
	p := WritePolicies{
		Title:       WritePolicyValuePreserve,
		Subtitle:    WritePolicyValuePreserve,
		Authors:     WritePolicyValuePreserve,
		Narrators:   WritePolicyValuePreserve,
		Series:      WritePolicyValuePreserve,
		Description: WritePolicyValuePreserve,
		Genres:      WritePolicyValuePreserve,
		Year:        WritePolicyValuePreserve,
	}
	if selected["title"] && strings.TrimSpace(m.Title) != "" {
		p.Title = WritePolicyValueWrite
	}
	if selected["subtitle"] && m.Subtitle != nil && strings.TrimSpace(*m.Subtitle) != "" {
		p.Subtitle = WritePolicyValueWrite
	}
	if selected["authors"] && len(m.Authors) > 0 {
		p.Authors = WritePolicyValueWrite
	}
	if selected["narrators"] && len(m.Narrators) > 0 {
		p.Narrators = WritePolicyValueWrite
	}
	if selected["series"] && m.SeriesName != nil && strings.TrimSpace(*m.SeriesName) != "" {
		p.Series = WritePolicyValueWrite
	}
	if selected["description"] && m.Description != nil && strings.TrimSpace(*m.Description) != "" {
		p.Description = WritePolicyValueWrite
	}
	if selected["genres"] && len(m.Genres) > 0 {
		p.Genres = WritePolicyValueWrite
	}
	if selected["year"] && m.Year != nil {
		p.Year = WritePolicyValueWrite
	}
	return p
}
