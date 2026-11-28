package audio

import (
	"reflect"
	"testing"
)

func TestNormalizeSelectableFieldsTrimsDedupesAndRejectsUnknown(t *testing.T) {
	got, err := NormalizeSelectableFields([]string{" title ", "cover", "subtitle", "subtitle"})
	if err != nil {
		t.Fatalf("NormalizeSelectableFields: %v", err)
	}
	want := []string{"title", "cover", "subtitle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeSelectableFields = %v, want %v", got, want)
	}
	// Case is significant: an uppercase field is unknown, not "year".
	if _, err := NormalizeSelectableFields([]string{"YEAR"}); err == nil {
		t.Fatal("uppercase field accepted; the vocabulary is case-sensitive")
	}

	if _, err := NormalizeSelectableFields([]string{"title", "isbn"}); err == nil {
		t.Fatal("unknown field accepted, want an error naming it")
	} else if err.Error() == "" {
		t.Fatal("error is empty")
	}
}

func TestNormalizeSelectableFieldsEmptyIsExplicitEmpty(t *testing.T) {
	got, err := NormalizeSelectableFields(nil)
	if err != nil {
		t.Fatalf("NormalizeSelectableFields(nil): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("empty input = %#v, want a non-nil empty slice (explicit write-nothing)", got)
	}
}

func TestWritePolicyForNilSelectionIsAllPreserve(t *testing.T) {
	// WritePolicyFor handles ONLY explicit selections: a nil (default)
	// selection must NOT be routed through it — that would silently turn the
	// all-supported-available default into "write nothing". The function's
	// documented contract is that a nil input yields no writes at all.
	m := SuppliedMetadata{Title: "T", Authors: []string{"A"}}
	got := WritePolicyFor(nil, m)
	for name, v := range map[string]string{
		"title": got.Title, "subtitle": got.Subtitle, "authors": got.Authors,
		"narrators": got.Narrators, "series": got.Series, "description": got.Description,
		"genres": got.Genres, "year": got.Year,
	} {
		if v != WritePolicyValuePreserve {
			t.Fatalf("nil selection %s = %q, want preserve (a nil selection must write nothing)", name, v)
		}
	}
}

func TestWritePolicyForWritesSelectedWithValuesPreservesRest(t *testing.T) {
	subtitle := "S"
	m := SuppliedMetadata{
		Title:     "T",
		Subtitle:  &subtitle,
		Authors:   []string{"A"},
		Narrators: []string{"N"},
		Year:      new(2020),
	}
	got := WritePolicyFor([]string{"title", "cover", "narrators"}, m)
	want := WritePolicies{
		Title:       WritePolicyValueWrite,
		Subtitle:    WritePolicyValuePreserve,
		Authors:     WritePolicyValuePreserve,
		Narrators:   WritePolicyValueWrite,
		Series:      WritePolicyValuePreserve,
		Description: WritePolicyValuePreserve,
		Genres:      WritePolicyValuePreserve,
		Year:        WritePolicyValuePreserve,
	}
	if got != want {
		t.Fatalf("WritePolicyFor = %+v, want %+v", got, want)
	}
}

func TestWritePolicyForSelectedButUnavailableDegradesToPreserve(t *testing.T) {
	// A selected field with no value must preserve (the engine refuses blank
	// writes); it must never become clear.
	got := WritePolicyFor([]string{"title", "authors", "description"}, SuppliedMetadata{})
	if got.Title != WritePolicyValuePreserve || got.Authors != WritePolicyValuePreserve || got.Description != WritePolicyValuePreserve {
		t.Fatalf("unavailable selected fields = %+v, want all preserve", got)
	}
}

func TestWritePolicyForNoneSelectedAllPreserve(t *testing.T) {
	// An explicit empty selection (the UI's "nothing checked") is a valid
	// selection: no embedded mutation. It must behave like the nil case
	// above — everything preserves — even when the snapshot carries values.
	m := SuppliedMetadata{Title: "T", Authors: []string{"A"}}
	got := WritePolicyFor([]string{}, m)
	for name, v := range map[string]string{
		"title": got.Title, "subtitle": got.Subtitle, "authors": got.Authors,
		"narrators": got.Narrators, "series": got.Series, "description": got.Description,
		"genres": got.Genres, "year": got.Year,
	} {
		if v != WritePolicyValuePreserve {
			t.Fatalf("empty selection %s = %q, want preserve", name, v)
		}
	}
}

func TestValidateWriteSelectionForEngine(t *testing.T) {
	caps := &Capabilities{
		FieldPolicies: FieldPolicies{
			ScalarFields: []string{"title", "subtitle", "authors", "narrators", "series", "description", "genres", "year"},
		},
	}
	if err := ValidateWriteSelectionForEngine([]string{"title", "cover", "year"}, caps); err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
	if err := ValidateWriteSelectionForEngine([]string{"title", "isbn"}, caps); err == nil {
		t.Fatal("field missing from the engine's scalar_fields accepted")
	}
	if err := ValidateWriteSelectionForEngine([]string{"title"}, nil); err == nil {
		t.Fatal("nil capabilities accepted, want a nameable failure")
	}
}

func TestValidateCapabilitiesRejectsMissingScalarFields(t *testing.T) {
	p := &Processor{}
	good := &Capabilities{
		SchemaVersion:   AcceptedEngineSchemaVersion,
		ResolverVersion: PinnedEngineResolverVersion,
		PathTokens:      []PathToken{{Name: "book_id"}, {Name: "authors"}, {Name: "narrators"}},
		FieldPolicies: FieldPolicies{
			Values:       []string{WritePolicyValueWrite, WritePolicyValuePreserve, WritePolicyValueClear},
			ScalarFields: SupportedWriteFields,
		},
		// The real engine vocabulary, verbatim: a written cover is
		// "write_url" (or the object form in constraint text), never
		// bare "write". A fixture that invents "write" is why the boot
		// gate regressed into rejecting the pinned engine.
		CoverPolicies:   PolicyVariantSet{Variants: []string{"write_url", "preserve", "clear"}},
		ChapterPolicies: PolicyVariantSet{Variants: []string{"write", "preserve", "clear"}},
	}
	if _, err := p.validateCapabilities(good); err != nil {
		t.Fatalf("valid capabilities rejected: %v", err)
	}
	dropped := *good
	dropped.FieldPolicies.ScalarFields = []string{"title", "authors", "narrators", "series", "description", "genres", "year"}
	if _, err := p.validateCapabilities(&dropped); err == nil {
		t.Fatal("engine without the subtitle scalar field accepted; the write selection would silently ignore it")
	}
	// An engine that dropped "preserve" from a policy path would boot, then
	// reject every unselected-field / preserve job at run time: the gate must
	// name that at startup.
	noCoverPreserve := *good
	noCoverPreserve.CoverPolicies.Variants = []string{"write_url", "clear"}
	if _, err := p.validateCapabilities(&noCoverPreserve); err == nil {
		t.Fatal("engine without cover preserve accepted; a preserved cover would fail at job time")
	}
	noChapterPreserve := *good
	noChapterPreserve.ChapterPolicies.Variants = []string{"write", "clear"}
	if _, err := p.validateCapabilities(&noChapterPreserve); err == nil {
		t.Fatal("engine without chapter preserve accepted; a preserved chapter list would fail at job time")
	}
	// An engine that lists every scalar field but drops one of the policy
	// VALUES Bragi emits passes the field checks and only rejects the job
	// at run time: the gate must name the missing value at startup.
	noFieldValues := *good
	noFieldValues.FieldPolicies.Values = []string{WritePolicyValueWrite, WritePolicyValuePreserve}
	if _, err := p.validateCapabilities(&noFieldValues); err == nil {
		t.Fatal("engine without the clear field-policy value accepted; a cleared field would fail at job time")
	}
	// A missing resolver_version decodes as "" and must be refused at the
	// gate: it would pass every later identity comparison against a job
	// captured under a newer resolver.
	emptyResolver := *good
	emptyResolver.ResolverVersion = ""
	if _, err := p.validateCapabilities(&emptyResolver); err == nil {
		t.Fatal("engine with a missing resolver_version accepted; the identity comparison would be meaningless")
	}
}
