package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/models"
)

// The Match page renders its Source details panel from a presentation
// report. The pending list can only state what the source itself says (the
// read status), because the catalog selection is chosen in the browser.
// The selection-scoped report is served by GetMatchEvidence.

// matchEvidenceBase returns a source-only report: it makes no claim about
// any catalog selection. Its empty selection can never match a loaded
// selection, so the UI cannot draw verification or comparison marks from
// it alone.
func matchEvidenceBase(sourceRead models.SourceRead) models.MatchEvidence {
	return models.MatchEvidence{
		SourceRead:  sourceRead,
		Selection:   models.MatchEvidenceSelection{BookID: ""},
		Origin:      models.MatchOriginTitle,
		Identifiers: []models.IdentifierEvidence{},
		Comparisons: map[string]models.FieldComparison{},
	}
}

// sourceReadForSnapshot maps extraction completeness onto the UI's read
// states. Only a current-schema, clean pass over every discovered file may
// claim "read"; older schemas cannot express the degraded/truncated
// evidence their rows currently hold.
func sourceReadForSnapshot(snap *audio.SourceMetadata) models.SourceRead {
	if snap == nil {
		return models.SourceRead{Status: models.SourceReadNotRead}
	}
	if snap.Degraded {
		return models.SourceRead{Status: models.SourceReadFailed, Detail: degradedDetail(snap)}
	}
	if snap.Schema != audio.SourceMetadataSchema {
		return models.SourceRead{
			Status: models.SourceReadPartial,
			Detail: fmt.Sprintf("Source metadata was captured with schema %d; reprocess to refresh it.", snap.Schema),
		}
	}
	if !snap.Complete() {
		return models.SourceRead{Status: models.SourceReadPartial, Detail: incompleteDetail(snap)}
	}
	return models.SourceRead{Status: models.SourceReadRead, Detail: "Embedded tags read from all source files."}
}

func degradedDetail(snap *audio.SourceMetadata) string {
	for _, key := range sourceFieldKeys {
		if field, ok := snap.Fields[key]; ok && field.Error != "" {
			return field.Error
		}
	}
	for _, file := range snap.Files {
		if file.Error != "" {
			return file.Error
		}
	}
	return "Source metadata extraction did not produce usable evidence."
}

func incompleteDetail(snap *audio.SourceMetadata) string {
	if snap.Truncated {
		return "Extraction stopped at an internal bound; not every source entry was read."
	}
	errored := 0
	for _, file := range snap.Files {
		if file.Error != "" {
			errored++
		}
	}
	unprobed := snap.FilesTotal - snap.FilesProbed
	if unprobed < 0 {
		unprobed = 0
	}
	return fmt.Sprintf("Extraction incomplete: %d errored, %d unprobed of %d files.", errored, unprobed, snap.FilesTotal)
}

// sourceFieldKeys is the comparison field order; it mirrors the UI's
// EvidenceField union (ASIN and the AudiobookDB IDs are identifiers, not
// compared fields).
var sourceFieldKeys = []string{
	audio.FieldTitle, audio.FieldAuthor, audio.FieldNarrator, audio.FieldDate,
	audio.FieldPublisher, audio.FieldLanguage,
}

var fieldProvenanceLabels = map[string]string{
	audio.FieldTitle:     "title",
	audio.FieldAuthor:    "author",
	audio.FieldNarrator:  "narrator",
	audio.FieldDate:      "date",
	audio.FieldPublisher: "publisher",
	audio.FieldLanguage:  "language",
}

func fieldProvenance(key string, field audio.SourceField) string {
	switch field.Source {
	case audio.SourceFilename:
		return "Source filename"
	case audio.SourceEmbedded:
		return "Embedded " + fieldProvenanceLabels[key] + " tag"
	}
	return "Source metadata"
}

// matchCatalog is the selected catalog record, fetched once per report.
// LookupErr is non-empty when a lookup failed; the report then stays
// truthful (nothing is verified or compared) instead of failing the
// request.
type matchCatalog struct {
	Book      *audiobookdb.Book
	Release   *audiobookdb.Release
	LookupErr string
}

func (c *matchCatalog) usable() bool {
	return c != nil && c.LookupErr == "" && c.Book != nil
}

// buildMatchEvidence derives the presentation report for one selection.
// malformed marks a stored payload that does not decode: the row exists but
// its contents cannot be trusted.
func buildMatchEvidence(
	snap *audio.SourceMetadata,
	catalog *matchCatalog,
	malformed bool,
	selection models.MatchEvidenceSelection,
) models.MatchEvidence {
	if malformed {
		report := matchEvidenceBase(models.SourceRead{
			Status: models.SourceReadFailed,
			Detail: "Stored source metadata could not be read.",
		})
		report.Selection = selection
		return report
	}
	if snap == nil {
		report := matchEvidenceBase(models.SourceRead{Status: models.SourceReadNotRead})
		report.Selection = selection
		return report
	}

	report := matchEvidenceBase(sourceReadForSnapshot(snap))
	report.Selection = selection
	complete := report.SourceRead.Status == models.SourceReadRead
	report.Identifiers = buildIdentifiers(snap, catalog, selection, complete)
	report.Origin = originFor(report.Identifiers)
	if complete {
		report.Comparisons = buildComparisons(snap, catalog)
	}
	return report
}

// originFor describes how the selected record was identified. Only a
// verified relationship may promote the origin: an edition relationship
// outranks a book relationship, because it is also proof of the book.
func originFor(identifiers []models.IdentifierEvidence) string {
	bookVerified := false
	for _, id := range identifiers {
		if id.State != models.IdentifierVerified {
			continue
		}
		if id.Scope == models.IdentifierScopeEdition {
			return models.MatchOriginEditionID
		}
		bookVerified = true
	}
	if bookVerified {
		return models.MatchOriginBookID
	}
	return models.MatchOriginTitle
}

// distinctValues trims, dedupes, and preserves first-seen order.
func distinctValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func asinProvenance(field audio.SourceField) string {
	if field.Source == audio.SourceFilename {
		return "Source filename"
	}
	return "Embedded ASIN tag"
}

func buildIdentifiers(
	snap *audio.SourceMetadata,
	catalog *matchCatalog,
	selection models.MatchEvidenceSelection,
	complete bool,
) []models.IdentifierEvidence {
	out := make([]models.IdentifierEvidence, 0, 3)

	if values, truncated := sourceValuesByField(snap, audio.FieldAudiobookdbReleaseID); len(values) > 0 {
		for _, value := range values {
			out = append(out, resolveReleaseID(value, catalog, selection, complete, truncated))
		}
	}
	if values, truncated := sourceValuesByField(snap, audio.FieldAudiobookdbBookID); len(values) > 0 {
		for _, value := range values {
			out = append(out, resolveBookID(value, catalog, selection, complete, truncated))
		}
	}
	if field, ok := snap.Fields[audio.FieldASIN]; ok && field.Status == audio.StatusPresent {
		values := normalizeASINValues(field.Values)
		if len(values) > 1 {
			for _, value := range values {
				out = append(out, models.IdentifierEvidence{
					Identifier: value,
					Provenance: asinProvenance(field),
					State:      models.IdentifierUnresolved,
					Scope:      models.IdentifierScopeBook,
					Reason:     "Multiple distinct ASINs were found; no single identifier is complete evidence.",
				})
			}
		} else if len(values) == 1 {
			out = append(out, resolveASIN(values[0], field, catalog, selection, complete, field.Truncated))
		}
	}
	return out
}

// sourceValuesByField returns a present field's normalized values and
// whether the per-field cap dropped further values. A missing or errored
// field has no identifier claim.
func sourceValuesByField(snap *audio.SourceMetadata, key string) ([]string, bool) {
	field, ok := snap.Fields[key]
	if !ok || field.Status != audio.StatusPresent {
		return nil, false
	}
	return distinctValues(field.Values), field.Truncated
}

// untrustedIdentifierReason explains why a found identifier cannot be
// treated as complete evidence.
func untrustedIdentifierReason(complete, truncated bool) string {
	if truncated {
		return "The source identifier list was truncated; no retained value is complete evidence."
	}
	if !complete {
		return "Source extraction was incomplete; the identifier is not complete evidence."
	}
	return ""
}

func resolveReleaseID(
	value string,
	catalog *matchCatalog,
	selection models.MatchEvidenceSelection,
	complete bool,
	truncated bool,
) models.IdentifierEvidence {
	evidence := models.IdentifierEvidence{
		Identifier: value,
		Provenance: "Embedded AudiobookDB release ID tag",
		Scope:      models.IdentifierScopeEdition,
	}
	if reason := untrustedIdentifierReason(complete, truncated); reason != "" {
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = reason
		return evidence
	}
	if !catalog.usable() {
		evidence.State = models.IdentifierLookupFailed
		evidence.Reason = "AudiobookDB lookup failed, so the release ID could not be resolved."
		return evidence
	}
	if selection.ReleaseID == nil || *selection.ReleaseID == "" {
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = "No edition is selected."
		return evidence
	}
	if !strings.EqualFold(value, *selection.ReleaseID) {
		evidence.State = models.IdentifierConflict
		evidence.Reason = "The source release ID points to a different edition."
		return evidence
	}
	switch catalogEditionMembership(catalog.Book, *selection.ReleaseID) {
	case editionMembershipDisproven:
		evidence.State = models.IdentifierConflict
		evidence.Reason = "The selected edition does not belong to the selected book."
		return evidence
	case editionMembershipUnknown:
		// The expanded book did not list its releases, so nothing proves the
		// relationship. Never promote an unproven relationship to verified
		// edition identity (which would render the green exact-edition label).
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = "The selected book's releases were not expanded, so the source release ID could not be verified."
		return evidence
	}
	evidence.State = models.IdentifierVerified
	evidence.BookID = selection.BookID
	evidence.ReleaseID = *selection.ReleaseID
	evidence.Reason = "Resolved release belongs to this book."
	return evidence
}

// editionMembership is a tri-state check: only a positively listed release
// proves the edition-to-book relationship. An absent or unexpanded
// releases list is unknown, not proof.
type editionMembership int

const (
	editionMembershipUnknown editionMembership = iota
	editionMembershipProven
	editionMembershipDisproven
)

func catalogEditionMembership(book *audiobookdb.Book, releaseID string) editionMembership {
	if book == nil || len(book.Releases) == 0 {
		return editionMembershipUnknown
	}
	for _, release := range book.Releases {
		if strings.EqualFold(release.ID, releaseID) {
			return editionMembershipProven
		}
	}
	return editionMembershipDisproven
}

func resolveBookID(
	value string,
	catalog *matchCatalog,
	selection models.MatchEvidenceSelection,
	complete bool,
	truncated bool,
) models.IdentifierEvidence {
	evidence := models.IdentifierEvidence{
		Identifier: value,
		Provenance: "Embedded AudiobookDB book ID tag",
		Scope:      models.IdentifierScopeBook,
	}
	if reason := untrustedIdentifierReason(complete, truncated); reason != "" {
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = reason
		return evidence
	}
	if !catalog.usable() {
		evidence.State = models.IdentifierLookupFailed
		evidence.Reason = "AudiobookDB lookup failed, so the book ID could not be resolved."
		return evidence
	}
	if !strings.EqualFold(value, selection.BookID) {
		evidence.State = models.IdentifierConflict
		evidence.Reason = "The source book ID points to a different catalog record."
		return evidence
	}
	evidence.State = models.IdentifierVerified
	evidence.BookID = selection.BookID
	evidence.Reason = "Resolved book is the selected catalog book."
	return evidence
}

func resolveASIN(
	value string,
	field audio.SourceField,
	catalog *matchCatalog,
	selection models.MatchEvidenceSelection,
	complete bool,
	truncated bool,
) models.IdentifierEvidence {
	evidence := models.IdentifierEvidence{
		Identifier: value,
		Provenance: asinProvenance(field),
		Scope:      models.IdentifierScopeBook,
	}
	if reason := untrustedIdentifierReason(complete, truncated); reason != "" {
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = reason
		return evidence
	}
	if !catalog.usable() {
		evidence.State = models.IdentifierLookupFailed
		evidence.Reason = "AudiobookDB lookup failed, so the ASIN could not be verified."
		return evidence
	}
	catalogASIN := catalogBookASIN(catalog.Book)
	if catalogASIN == "" {
		evidence.State = models.IdentifierUnresolved
		evidence.Reason = "The selected catalog book has no ASIN to check."
		return evidence
	}
	if !strings.EqualFold(value, catalogASIN) {
		evidence.State = models.IdentifierConflict
		evidence.Reason = "The source ASIN points to a different catalog record."
		return evidence
	}
	evidence.State = models.IdentifierVerified
	evidence.BookID = selection.BookID
	evidence.Reason = "The source ASIN matches the selected catalog book."
	return evidence
}

var asinURLPattern = regexp.MustCompile(`(?:dp|gp/product)/([A-Za-z0-9]{10})`)

// catalogBookASIN mirrors the UI's extractAsin: an amazon/asin external
// relation first, then an ASIN embedded in its URL.
func catalogBookASIN(book *audiobookdb.Book) string {
	if book == nil {
		return ""
	}
	for _, external := range book.External {
		if external.Type != "amazon" && external.Type != "asin" {
			continue
		}
		if strings.TrimSpace(external.ID) != "" {
			return strings.TrimSpace(external.ID)
		}
		if match := asinURLPattern.FindStringSubmatch(external.URL); len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

func buildComparisons(snap *audio.SourceMetadata, catalog *matchCatalog) map[string]models.FieldComparison {
	comparisons := make(map[string]models.FieldComparison, 7)
	lookupFailed := catalog == nil || catalog.LookupErr != ""
	for _, key := range sourceFieldKeys {
		comparisons[key] = compareTextField(key, snap, catalog, lookupFailed)
	}
	comparisons["runtime"] = compareRuntime(snap, catalog, lookupFailed)
	return comparisons
}

func compareTextField(
	key string,
	snap *audio.SourceMetadata,
	catalog *matchCatalog,
	lookupFailed bool,
) models.FieldComparison {
	field := snap.Fields[key]
	provenance := fieldProvenance(key, field)
	selectedValue := ""
	if catalog != nil && !lookupFailed {
		selectedValue = catalogTextValue(key, catalog)
	}

	if key == audio.FieldAuthor || key == audio.FieldNarrator {
		return compareCredits(key, field, selectedValue, catalog, provenance, lookupFailed)
	}

	values := distinctValues(field.Values)
	if field.Status != audio.StatusPresent || len(values) == 0 {
		return comparisonUnavailable("", selectedValue, provenance, missingValueReason(field))
	}
	if lookupFailed {
		return comparisonUnavailable(joinValues(values), "", provenance, "AudiobookDB lookup failed, so the catalog value is unavailable.")
	}
	if field.Truncated {
		return models.FieldComparison{
			State:      models.ComparisonNotComparable,
			Source:     new(joinValues(values)),
			Selected:   nullable(selectedValue),
			Provenance: provenance,
			Reason:     "The source value list was truncated, so it is not complete evidence.",
		}
	}
	if len(values) > 1 {
		return models.FieldComparison{
			State:      models.ComparisonNotComparable,
			Source:     new(joinValues(values)),
			Selected:   nullable(selectedValue),
			Provenance: provenance,
			Reason:     "The source has multiple distinct values for this field.",
		}
	}

	sourceValue := values[0]
	if key == audio.FieldDate {
		return compareDates(sourceValue, selectedValue, provenance)
	}
	if selectedValue == "" {
		return comparisonUnavailable(sourceValue, "", provenance, "The selected catalog record has no value for this field.")
	}
	if normalizeText(sourceValue) == normalizeText(selectedValue) {
		return comparisonMatch(sourceValue, selectedValue, provenance, sameValueReason(key))
	}
	return comparisonDifferent(sourceValue, selectedValue, provenance, differReason(key))
}

func compareCredits(
	key string,
	field audio.SourceField,
	selectedValue string,
	catalog *matchCatalog,
	provenance string,
	lookupFailed bool,
) models.FieldComparison {
	values := distinctValues(field.Values)
	if field.Status != audio.StatusPresent || len(values) == 0 {
		return comparisonUnavailable("", selectedValue, provenance, missingValueReason(field))
	}
	sourceDisplay := joinValues(values)
	if lookupFailed {
		return comparisonUnavailable(sourceDisplay, "", provenance, "AudiobookDB lookup failed, so the catalog credits are unavailable.")
	}
	if field.Truncated {
		return models.FieldComparison{
			State:      models.ComparisonNotComparable,
			Source:     new(sourceDisplay),
			Selected:   nullable(selectedValue),
			Provenance: provenance,
			Reason:     "The source credit list was truncated, so it is not a complete credit set.",
		}
	}
	if selectedValue == "" {
		return comparisonUnavailable(sourceDisplay, "", provenance, "The selected catalog record has no credits for this role.")
	}
	sourceCredits := creditSet(values)
	catalogCredits := catalogCreditSet(catalog, key)
	if creditSetsEqual(sourceCredits, catalogCredits) {
		return comparisonMatch(sourceDisplay, selectedValue, provenance, "Same complete credit set.")
	}
	return comparisonDifferent(sourceDisplay, selectedValue, provenance, "Credits differ.")
}

// catalogTextValue mirrors the client's buildUpdate fallbacks so the
// comparison names the value the Save path would persist.
func catalogTextValue(key string, catalog *matchCatalog) string {
	book := catalog.Book
	var release *audiobookdb.Release
	if catalog.usable() {
		release = catalog.Release
	}
	if book == nil {
		return ""
	}
	switch key {
	case audio.FieldTitle:
		if release != nil && strings.TrimSpace(release.Title) != "" {
			return strings.TrimSpace(release.Title)
		}
		return strings.TrimSpace(book.Title)
	case audio.FieldAuthor, audio.FieldNarrator:
		role := "author"
		if key == audio.FieldNarrator {
			role = "narrator"
		}
		return strings.Join(catalogPeople(book, release, role), ", ")
	case audio.FieldDate:
		if release != nil && release.ReleaseDate != nil && strings.TrimSpace(*release.ReleaseDate) != "" {
			return strings.TrimSpace(*release.ReleaseDate)
		}
		if book.OriginallyPublishedAt != nil {
			return strings.TrimSpace(*book.OriginallyPublishedAt)
		}
		return ""
	case audio.FieldPublisher:
		if release != nil {
			return strings.TrimSpace(release.Publisher.Name)
		}
		return ""
	case audio.FieldLanguage:
		if release != nil {
			return strings.TrimSpace(release.Language.Title)
		}
		return ""
	}
	return ""
}

// catalogPeople mirrors the UI's rolePeople: book people first, then
// release people, deduped by person ID (then by name for ID-less credits).
func catalogPeople(book *audiobookdb.Book, release *audiobookdb.Release, role string) []string {
	var out []string
	seenIDs := map[string]bool{}
	seenNames := map[string]bool{}
	add := func(people []audiobookdb.PersonRoleRelation) {
		for _, entry := range people {
			if !strings.EqualFold(entry.Role.Name, role) {
				continue
			}
			name := strings.TrimSpace(entry.Person.Name)
			if name == "" {
				continue
			}
			id := strings.TrimSpace(entry.Person.ID)
			if id != "" {
				if seenIDs[id] {
					continue
				}
				seenIDs[id] = true
			} else if seenNames[strings.ToLower(name)] {
				continue
			}
			seenNames[strings.ToLower(name)] = true
			out = append(out, name)
		}
	}
	if book != nil {
		add(book.People)
	}
	if release != nil {
		add(release.People)
	}
	return out
}

func catalogCreditSet(catalog *matchCatalog, key string) map[string]bool {
	role := "author"
	if key == audio.FieldNarrator {
		role = "narrator"
	}
	var book *audiobookdb.Book
	var release *audiobookdb.Release
	if catalog != nil && catalog.usable() {
		book = catalog.Book
		release = catalog.Release
	}
	return creditSet(catalogPeople(book, release, role))
}

var creditSeparator = regexp.MustCompile(`(?i)\s*(?:,|;|&|\band\b)\s*`)

// creditSet splits multi-credit tags ("A & B", "A; B") into normalized
// names so a single tag can match the catalog's separate people.
func creditSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range creditSeparator.Split(value, -1) {
			name := normalizeText(part)
			if name != "" {
				out[name] = true
			}
		}
	}
	return out
}

func creditSetsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

func normalizeText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

var datePrefix = regexp.MustCompile(`^(\d{4})(?:-(\d{1,2})(?:-(\d{1,2}))?)?`)

type dateParts struct {
	year, month, day int
	hasMonth, hasDay bool
}

func parseDatePrefix(value string) (dateParts, bool) {
	match := datePrefix.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return dateParts{}, false
	}
	year, err := strconv.Atoi(match[1])
	if err != nil {
		return dateParts{}, false
	}
	parts := dateParts{year: year}
	if match[2] != "" {
		parts.month, _ = strconv.Atoi(match[2])
		parts.hasMonth = true
	}
	if match[3] != "" {
		parts.day, _ = strconv.Atoi(match[3])
		parts.hasDay = true
	}
	return parts, true
}

func compareDates(sourceValue, selectedValue, provenance string) models.FieldComparison {
	if selectedValue == "" {
		return comparisonUnavailable(sourceValue, "", provenance, "The selected catalog record has no release date.")
	}
	source, sourceOK := parseDatePrefix(sourceValue)
	catalog, catalogOK := parseDatePrefix(selectedValue)
	if !sourceOK || !catalogOK {
		if normalizeText(sourceValue) == normalizeText(selectedValue) {
			return comparisonMatch(sourceValue, selectedValue, provenance, "Same release date.")
		}
		return comparisonDifferent(sourceValue, selectedValue, provenance, "Release dates differ.")
	}
	if source.year != catalog.year {
		return comparisonDifferent(sourceValue, selectedValue, provenance, "Release years differ.")
	}
	if source.hasMonth && catalog.hasMonth {
		if source.month != catalog.month {
			return comparisonDifferent(sourceValue, selectedValue, provenance, "Release months differ.")
		}
		if source.hasDay && catalog.hasDay {
			if source.day != catalog.day {
				return comparisonDifferent(sourceValue, selectedValue, provenance, "Release dates differ.")
			}
			return comparisonMatch(sourceValue, selectedValue, provenance, "Same release date.")
		}
		return models.FieldComparison{
			State:      models.ComparisonNotComparable,
			Source:     new(sourceValue),
			Selected:   new(selectedValue),
			Provenance: provenance,
			Reason:     "Source and catalog dates have different precision.",
		}
	}
	return models.FieldComparison{
		State:      models.ComparisonNotComparable,
		Source:     new(sourceValue),
		Selected:   new(selectedValue),
		Provenance: provenance,
		Reason:     "Source and catalog dates have different precision.",
	}
}

// runtimeCloseToleranceMs is the widest difference still reported as
// "close": a measured runtime is a sum of probed files, so a minute of
// chapters or a rounding difference is not a disagreement about the
// edition.
const runtimeCloseToleranceMs = 60_000

func compareRuntime(snap *audio.SourceMetadata, catalog *matchCatalog, lookupFailed bool) models.FieldComparison {
	provenance := "Measured from probed source files"
	sourceMs := int64(math.Round(snap.DurationSeconds * 1000))
	if sourceMs <= 0 {
		return comparisonUnavailable("", "", provenance, "The source duration was not measured.")
	}
	if lookupFailed {
		return comparisonUnavailable(formatDurationMs(sourceMs), "", provenance, "AudiobookDB lookup failed, so the catalog runtime is unavailable.")
	}
	catalogMs := catalogRuntimeMs(catalog)
	if catalogMs <= 0 {
		return comparisonUnavailable(formatDurationMs(sourceMs), "", provenance, "The selected catalog release has no runtime.")
	}

	comparison := models.FieldComparison{
		Source:     new(formatDurationMs(sourceMs)),
		Selected:   new(formatDurationMs(catalogMs)),
		Provenance: provenance,
		Durations: &models.RuntimeDurations{
			Source:  models.DurationReading{Milliseconds: sourceMs, Precision: "exact"},
			Catalog: models.DurationReading{Milliseconds: catalogMs, Precision: "exact"},
		},
	}
	diff := sourceMs - catalogMs
	if diff < 0 {
		diff = -diff
	}
	switch {
	case diff == 0:
		comparison.State = models.ComparisonMatch
		comparison.Reason = "Same measured runtime."
	case diff <= runtimeCloseToleranceMs:
		comparison.State = models.ComparisonClose
		comparison.Reason = "Runtimes are within one minute."
	default:
		comparison.State = models.ComparisonDifferent
		comparison.Reason = "Runtimes differ."
	}
	return comparison
}

func catalogRuntimeMs(catalog *matchCatalog) int64 {
	if catalog == nil || catalog.Release == nil {
		return 0
	}
	if catalog.Release.RuntimeLengthMs > 0 {
		return int64(catalog.Release.RuntimeLengthMs)
	}
	if catalog.Release.RuntimeLengthSec > 0 {
		return int64(catalog.Release.RuntimeLengthSec) * 1000
	}
	return 0
}

func formatDurationMs(milliseconds int64) string {
	minutes := (milliseconds + 30_000) / 60_000
	if minutes <= 0 {
		return "Under 1 min"
	}
	hours := minutes / 60
	remaining := minutes % 60
	switch {
	case hours == 0:
		return fmt.Sprintf("%dm", remaining)
	case remaining == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, remaining)
	}
}

func missingValueReason(field audio.SourceField) string {
	if field.Status == audio.StatusError && field.Error != "" {
		return "Source extraction could not read this field: " + field.Error
	}
	return "The source has no value for this field."
}

func sameValueReason(key string) string {
	switch key {
	case audio.FieldTitle:
		return "Same title."
	case audio.FieldPublisher:
		return "Same publisher."
	case audio.FieldLanguage:
		return "Same language."
	}
	return "Same value after case and whitespace normalization."
}

func differReason(key string) string {
	switch key {
	case audio.FieldTitle:
		return "Titles differ."
	case audio.FieldPublisher:
		return "Publishers differ."
	case audio.FieldLanguage:
		return "Languages differ."
	}
	return "Values differ."
}

func comparisonMatch(source, selected, provenance, reason string) models.FieldComparison {
	return models.FieldComparison{
		State: models.ComparisonMatch, Source: new(source), Selected: new(selected),
		Provenance: provenance, Reason: reason,
	}
}

func comparisonDifferent(source, selected, provenance, reason string) models.FieldComparison {
	return models.FieldComparison{
		State: models.ComparisonDifferent, Source: new(source), Selected: new(selected),
		Provenance: provenance, Reason: reason,
	}
}

func comparisonUnavailable(source, selected, provenance, reason string) models.FieldComparison {
	return models.FieldComparison{
		State: models.ComparisonUnavailable, Source: nullable(source), Selected: nullable(selected),
		Provenance: provenance, Reason: reason,
	}
}

func nullable(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return new(value)
}

func joinValues(values []string) string {
	return strings.Join(values, ", ")
}

// attachSourceProjections batch-loads book_source_metadata for the given
// books and sets each book's source-only projections: SourceASIN (the
// narrow pending-queue prefill) and MatchEvidence (the Match read-state
// banner). Best-effort: a missing row leaves both nil, a malformed payload
// yields an explicit failed read report, and a query failure logs and
// leaves everything nil — it never fails the list.
//
// Element addresses are stable for the caller's slice: `&books[i]` points
// into the caller's backing array, so the writes below are visible to the
// caller without a second pass. Do NOT pass a copy of the slice here.
func (h *Handler) attachSourceProjections(books []models.BookWithPeople) {
	if len(books) == 0 {
		return
	}

	placeholders := make([]string, len(books))
	args := make([]any, len(books))
	byID := make(map[int64]*models.BookWithPeople, len(books))
	for i := range books {
		placeholders[i] = "?"
		args[i] = books[i].ID
		byID[books[i].ID] = &books[i]
	}

	rows, err := h.svc.DB.Query(
		fmt.Sprintf("SELECT book_id, payload FROM book_source_metadata WHERE book_id IN (%s)", strings.Join(placeholders, ",")),
		args...,
	)
	if err != nil {
		log.Printf("attach source projections: query book_source_metadata: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var bookID int64
		var payload []byte
		if err := rows.Scan(&bookID, &payload); err != nil {
			log.Printf("attach source projections: scan row: %v", err)
			continue
		}
		book, ok := byID[bookID]
		if !ok {
			continue
		}
		var snap audio.SourceMetadata
		if err := json.Unmarshal(payload, &snap); err != nil {
			report := matchEvidenceBase(models.SourceRead{
				Status: models.SourceReadFailed,
				Detail: "Stored source metadata could not be read.",
			})
			book.MatchEvidence = &report
			continue
		}
		book.SourceASIN = deriveSourceASIN(&snap)
		report := matchEvidenceBase(sourceReadForSnapshot(&snap))
		book.MatchEvidence = &report
	}
	if err := rows.Err(); err != nil {
		log.Printf("attach source projections: read rows: %v", err)
	}
}

// GetMatchEvidence handles GET /api/books/{id}/match-evidence with the
// selected catalog ids. It returns the selection-scoped presentation
// report: identifiers resolved against the selected record and per-field
// source-vs-catalog comparisons. A failed catalog lookup degrades the
// report instead of failing the request; the UI then shows no false
// verification.
func (h *Handler) GetMatchEvidence(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(chiURLParam(r, "id"), 10, 64)
	if err != nil || bookID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid book id")
		return
	}

	catalogBookID := strings.TrimSpace(r.URL.Query().Get("book_id"))
	if catalogBookID == "" {
		writeError(w, http.StatusBadRequest, "book_id parameter is required")
		return
	}
	releaseID := strings.TrimSpace(r.URL.Query().Get("release_id"))

	var exists int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = ?", bookID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query book: %v", err))
		return
	}
	if exists == 0 {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}

	snap, malformed := loadSourceMetadataSnapshot(h.svc.DB, bookID)
	selection := models.MatchEvidenceSelection{BookID: catalogBookID}
	if releaseID != "" {
		selection.ReleaseID = &releaseID
	}

	report := buildMatchEvidence(snap, h.fetchMatchCatalog(r.Context(), catalogBookID, releaseID), malformed, selection)
	writeJSON(w, http.StatusOK, report)
}

// loadSourceMetadataSnapshot reads one book's stored snapshot. A missing
// row is (nil, false); an undecodable payload is (nil, true).
func loadSourceMetadataSnapshot(db *sql.DB, bookID int64) (*audio.SourceMetadata, bool) {
	var payload []byte
	err := db.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = ?", bookID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		log.Printf("match evidence for book %d: read snapshot: %v", bookID, err)
		return nil, true
	}
	var snap audio.SourceMetadata
	if err := json.Unmarshal(payload, &snap); err != nil {
		log.Printf("match evidence for book %d: decode snapshot: %v", bookID, err)
		return nil, true
	}
	return &snap, false
}

func (h *Handler) fetchMatchCatalog(ctx context.Context, catalogBookID, releaseID string) *matchCatalog {
	if h.svc.AudiobookDB == nil {
		return &matchCatalog{LookupErr: "AudiobookDB is not configured."}
	}
	client := h.getAudiobookDB()
	if client == nil {
		return &matchCatalog{LookupErr: "AudiobookDB is not configured."}
	}

	book, err := client.GetBook(ctx, catalogBookID, "external,people,releases")
	if err != nil {
		log.Printf("match evidence: audiobookdb book %s: %v", catalogBookID, err)
		return &matchCatalog{LookupErr: "AudiobookDB book lookup failed."}
	}
	catalog := &matchCatalog{Book: book}
	if releaseID == "" {
		return catalog
	}
	release, err := client.GetRelease(ctx, releaseID, "people,publisher,language")
	if err != nil {
		log.Printf("match evidence: audiobookdb release %s: %v", releaseID, err)
		catalog.LookupErr = "AudiobookDB release lookup failed."
		return catalog
	}
	catalog.Release = release
	return catalog
}
