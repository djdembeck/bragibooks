package models

// SourceRead status values carried by MatchEvidence.SourceRead.
const (
	SourceReadNotRead = "not_read"
	SourceReadRead    = "read"
	SourceReadPartial = "partial"
	SourceReadFailed  = "failed"
)

// MatchOrigin values: how the reported catalog selection was identified.
const (
	MatchOriginTitle     = "title"
	MatchOriginManual    = "manual"
	MatchOriginBookID    = "book_id"
	MatchOriginEditionID = "edition_id"
)

// Identifier resolution states.
const (
	IdentifierVerified     = "verified"
	IdentifierConflict     = "conflict"
	IdentifierUnresolved   = "unresolved"
	IdentifierNotFound     = "not_found"
	IdentifierLookupFailed = "lookup_failed"
)

// Identifier scopes: a book relationship or a specific edition.
const (
	IdentifierScopeBook    = "book"
	IdentifierScopeEdition = "edition"
)

// Field comparison states.
const (
	ComparisonMatch         = "match"
	ComparisonClose         = "close"
	ComparisonDifferent     = "different"
	ComparisonUnavailable   = "unavailable"
	ComparisonNotComparable = "not_comparable"
)

// MatchEvidence is the optional Match presentation report. It is derived
// server-side from the book's own source-metadata snapshot and, for a
// selected catalog record, its catalog values. It never feeds the save
// path: books.asin and the matched catalog columns are untouched.
//
// A report with an empty Selection.BookID is source-only: it states what
// the extraction pass read, but makes no claim about any catalog
// selection. Only a report whose selection matches the loaded selection
// may introduce verification or comparison marks (see web/src/lib/
// matchEvidence.ts).
type MatchEvidence struct {
	SourceRead  SourceRead                 `json:"sourceRead"`
	Selection   MatchEvidenceSelection     `json:"selection"`
	Origin      string                     `json:"origin"` // title | manual | book_id | edition_id
	Identifiers []IdentifierEvidence       `json:"identifiers"`
	Comparisons map[string]FieldComparison `json:"comparisons"`
}

// SourceRead reports whether the source's embedded metadata was read.
type SourceRead struct {
	Status string `json:"status"` // not_read | read | partial | failed
	Detail string `json:"detail"`
}

// MatchEvidenceSelection is the catalog record the report describes.
// ReleaseID is null when the selection has no edition.
type MatchEvidenceSelection struct {
	BookID    string  `json:"bookId"`
	ReleaseID *string `json:"releaseId"`
}

// IdentifierEvidence is one identifier found in the source and its
// relationship to the selected catalog record.
type IdentifierEvidence struct {
	Identifier string `json:"identifier"`
	Provenance string `json:"provenance"`
	State      string `json:"state"` // verified | conflict | unresolved | not_found | lookup_failed
	Scope      string `json:"scope"` // book | edition
	BookID     string `json:"bookId,omitempty"`
	ReleaseID  string `json:"releaseId,omitempty"`
	Reason     string `json:"reason"`
}

// FieldComparison is one independently read source value next to the
// selected catalog value.
type FieldComparison struct {
	State      string            `json:"state"` // match | close | different | unavailable | not_comparable
	Source     *string           `json:"source"`
	Selected   *string           `json:"selected"`
	Provenance string            `json:"provenance"`
	Reason     string            `json:"reason"`
	Durations  *RuntimeDurations `json:"durations,omitempty"`
}

// RuntimeDurations carries the two measured runtimes for a numeric
// comparison. Precision describes the measurement resolution, not its
// display.
type RuntimeDurations struct {
	Source  DurationReading `json:"source"`
	Catalog DurationReading `json:"catalog"`
}

// DurationReading is one measured runtime in milliseconds.
type DurationReading struct {
	Milliseconds int64  `json:"milliseconds"`
	Precision    string `json:"precision"` // exact | minute-rounded
}
