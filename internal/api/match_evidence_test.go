package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/models"
)

// TestListBooksPendingIncludesMatchEvidence is the repro+regression for
// issue #513: the pending list never carried match_evidence, so the Match
// page fell back to "Source metadata not read" even when a complete
// snapshot existed. The base report must state the real read status.
func TestListBooksPendingIncludesMatchEvidence(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "pending", "",
		asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded))

	req := httptest.NewRequest("GET", "/api/books?status=pending", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Books []map[string]any `json:"books"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("books = %d; want 1", len(resp.Books))
	}
	raw, ok := resp.Books[0]["match_evidence"]
	if !ok {
		t.Fatalf("pending book has no match_evidence key; keys: %v", mapKeys(resp.Books[0]))
	}
	report, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("match_evidence = %T; want object", raw)
	}
	sourceRead, ok := report["sourceRead"].(map[string]any)
	if !ok {
		t.Fatalf("sourceRead = %#v; want object", report["sourceRead"])
	}
	if sourceRead["status"] != "read" {
		t.Errorf("sourceRead.status = %v; want read", sourceRead["status"])
	}
	// The base list report is source-only: it must not claim a selection.
	selection, ok := report["selection"].(map[string]any)
	if !ok {
		t.Fatalf("selection = %#v; want object", report["selection"])
	}
	if selection["bookId"] != "" || selection["releaseId"] != nil {
		t.Errorf("base selection = %#v; want empty bookId and null releaseId", selection)
	}
	if ids, ok := report["identifiers"].([]any); !ok || len(ids) != 0 {
		t.Errorf("identifiers = %#v; want empty array", report["identifiers"])
	}
	if comparisons, ok := report["comparisons"].(map[string]any); !ok || len(comparisons) != 0 {
		t.Errorf("comparisons = %#v; want empty object", report["comparisons"])
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// completeSourceSnap builds a clean single-file snapshot around the given
// fields. Complete() holds because every discovered file was probed.
func completeSourceSnap(fields map[string]audio.SourceField) *audio.SourceMetadata {
	return &audio.SourceMetadata{
		Schema:      audio.SourceMetadataSchema,
		FilesTotal:  1,
		FilesProbed: 1,
		Fields:      fields,
	}
}

func evidenceSelection(bookID, releaseID string) models.MatchEvidenceSelection {
	selection := models.MatchEvidenceSelection{BookID: bookID}
	if releaseID != "" {
		selection.ReleaseID = new(releaseID)
	}
	return selection
}

// evidenceTestCatalog is a selected book/edition whose values mirror the
// fields the Match page displays.
func evidenceTestCatalog() *matchCatalog {
	return &matchCatalog{
		Book: &audiobookdb.Book{
			ID:                    "book-a",
			Title:                 "The Book",
			OriginallyPublishedAt: new("2020-05-01T00:00:00.000Z"),
			External:              []audiobookdb.ExternalRelation{{Type: "amazon", ID: "B0ABCDEF01"}},
			People: []audiobookdb.PersonRoleRelation{
				{Role: audiobookdb.RoleRef{Name: "Author"}, Person: audiobookdb.PersonRef{ID: "p1", Name: "Ada Author"}},
			},
			Releases: []audiobookdb.IdTitle{{ID: "release-a", Title: "The Book"}},
		},
		Release: &audiobookdb.Release{
			ID:              "release-a",
			Title:           "The Book",
			RuntimeLengthMs: 3_600_000,
			ReleaseDate:     new("2020-05-01T00:00:00.000Z"),
			People: []audiobookdb.PersonRoleRelation{
				{Role: audiobookdb.RoleRef{Name: "Narrator"}, Person: audiobookdb.PersonRef{ID: "p2", Name: "Nia Narrator"}},
			},
			Publisher: audiobookdb.IdName{ID: "pub", Name: "Test Press"},
			Language:  audiobookdb.NameRef{Name: "English"},
		},
	}
}

func TestBuildMatchEvidenceSourceReadStatuses(t *testing.T) {
	tests := []struct {
		name   string
		snap   *audio.SourceMetadata
		status string
		detail string
	}{
		{name: "missing snapshot", snap: nil, status: models.SourceReadNotRead},
		{
			name:   "complete",
			snap:   completeSourceSnap(nil),
			status: models.SourceReadRead,
			detail: "Embedded tags read from all source files.",
		},
		{
			name: "degraded",
			snap: &audio.SourceMetadata{
				Schema:   audio.SourceMetadataSchema,
				Degraded: true,
				Fields: map[string]audio.SourceField{
					audio.FieldTitle: {Status: audio.StatusError, Error: "ffprobe unavailable"},
				},
			},
			status: models.SourceReadFailed,
			detail: "ffprobe unavailable",
		},
		{
			name: "older schema",
			snap: &audio.SourceMetadata{
				Schema:      1,
				FilesTotal:  0,
				FilesProbed: 0,
			},
			status: models.SourceReadPartial,
			detail: "Source metadata was captured with schema 1; reprocess to refresh it.",
		},
		{
			name: "unprobed files",
			snap: &audio.SourceMetadata{
				Schema:      audio.SourceMetadataSchema,
				FilesTotal:  2,
				FilesProbed: 1,
			},
			status: models.SourceReadPartial,
			detail: "Extraction incomplete: 0 errored, 1 unprobed of 2 files.",
		},
		{
			name: "truncated",
			snap: &audio.SourceMetadata{
				Schema:      audio.SourceMetadataSchema,
				FilesTotal:  1,
				FilesProbed: 1,
				Truncated:   true,
			},
			status: models.SourceReadPartial,
			detail: "Extraction stopped at an internal bound; not every source entry was read.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			read := sourceReadForSnapshot(tc.snap)
			if read.Status != tc.status {
				t.Fatalf("status = %q; want %q", read.Status, tc.status)
			}
			if tc.detail != "" && read.Detail != tc.detail {
				t.Errorf("detail = %q; want %q", read.Detail, tc.detail)
			}
		})
	}
}

func TestBuildMatchEvidenceVerifiesSelectionIdentifiers(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN:                 {Values: []string{"b0abcdef01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAudiobookdbBookID:    {Values: []string{"book-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAudiobookdbReleaseID: {Values: []string{"release-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})

	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginEditionID {
		t.Errorf("origin = %q; want edition_id", report.Origin)
	}
	if len(report.Identifiers) != 3 {
		t.Fatalf("identifiers = %d; want 3 — %+v", len(report.Identifiers), report.Identifiers)
	}
	for _, id := range report.Identifiers {
		if id.State != models.IdentifierVerified {
			t.Errorf("identifier %q state = %q; want verified", id.Identifier, id.State)
		}
		if id.BookID != "book-a" {
			t.Errorf("identifier %q bookId = %q; want book-a", id.Identifier, id.BookID)
		}
		if id.Scope == models.IdentifierScopeEdition && id.ReleaseID != "release-a" {
			t.Errorf("edition identifier releaseId = %q; want release-a", id.ReleaseID)
		}
	}
}

func TestBuildMatchEvidenceBookOnlyOrigin(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN: {Values: []string{"B0ABCDEF01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", ""))
	if report.Origin != models.MatchOriginBookID {
		t.Errorf("origin = %q; want book_id", report.Origin)
	}
	if report.Identifiers[0].State != models.IdentifierVerified {
		t.Errorf("ASIN state = %q; want verified", report.Identifiers[0].State)
	}
}

func TestBuildMatchEvidenceIdentifierConflicts(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN:                 {Values: []string{"B999999999"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAudiobookdbBookID:    {Values: []string{"book-b"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAudiobookdbReleaseID: {Values: []string{"release-b"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginTitle {
		t.Errorf("origin = %q; want title (nothing verified)", report.Origin)
	}
	for _, id := range report.Identifiers {
		if id.State != models.IdentifierConflict {
			t.Errorf("identifier %q state = %q; want conflict", id.Identifier, id.State)
		}
	}
}

func TestBuildMatchEvidenceIncompletePassNeverVerifies(t *testing.T) {
	snap := &audio.SourceMetadata{
		Schema:      audio.SourceMetadataSchema,
		FilesTotal:  2,
		FilesProbed: 1,
		Fields: map[string]audio.SourceField{
			audio.FieldASIN:                 {Values: []string{"B0ABCDEF01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
			audio.FieldAudiobookdbReleaseID: {Values: []string{"release-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		},
	}
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginTitle {
		t.Errorf("origin = %q; want title", report.Origin)
	}
	for _, id := range report.Identifiers {
		if id.State != models.IdentifierUnresolved {
			t.Errorf("identifier %q state = %q; want unresolved", id.Identifier, id.State)
		}
	}
	if len(report.Comparisons) != 0 {
		t.Errorf("partial pass produced comparisons: %+v", report.Comparisons)
	}
}

func TestBuildMatchEvidenceCatalogLookupFailure(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN: {Values: []string{"B0ABCDEF01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldTitle: {
			Values: []string{"The Book"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent,
		},
	})
	report := buildMatchEvidence(snap, &matchCatalog{LookupErr: "AudiobookDB book lookup failed."},
		false, evidenceSelection("book-a", "release-a"))
	if len(report.Identifiers) != 1 || report.Identifiers[0].State != models.IdentifierLookupFailed {
		t.Fatalf("identifiers = %+v; want lookup_failed", report.Identifiers)
	}
	comparison := report.Comparisons["title"]
	if comparison.State != models.ComparisonUnavailable {
		t.Errorf("title state = %q; want unavailable", comparison.State)
	}
	if comparison.Reason != "AudiobookDB lookup failed, so the catalog value is unavailable." {
		t.Errorf("title reason = %q", comparison.Reason)
	}
}

func TestBuildMatchEvidenceComparisons(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldTitle:     {Values: []string{"  the   BOOK "}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAuthor:    {Values: []string{"Ada Author"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldNarrator:  {Values: []string{"Nia Narrator & Second Reader"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldDate:      {Values: []string{"2020-05-01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldPublisher: {Values: []string{"Other Press"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldLanguage:  {Values: []string{"english"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	snap.DurationSeconds = 3570

	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))

	if got := report.Comparisons["title"].State; got != models.ComparisonMatch {
		t.Errorf("title = %q; want match", got)
	}
	if got := report.Comparisons["author"].State; got != models.ComparisonMatch {
		t.Errorf("author = %q; want match", got)
	}
	// The catalog has one narrator while the source tag names two: the
	// complete credit sets differ.
	if got := report.Comparisons["narrator"].State; got != models.ComparisonDifferent {
		t.Errorf("narrator = %q; want different", got)
	}
	if got := report.Comparisons["publisher"].State; got != models.ComparisonDifferent {
		t.Errorf("publisher = %q; want different", got)
	}
	if got := report.Comparisons["language"].State; got != models.ComparisonMatch {
		t.Errorf("language = %q; want match", got)
	}
	if got := report.Comparisons["date"].State; got != models.ComparisonMatch {
		t.Errorf("date = %q; want match", got)
	}
	runtime := report.Comparisons["runtime"]
	if runtime.State != models.ComparisonClose {
		t.Errorf("runtime = %q; want close", runtime.State)
	}
	if runtime.Durations == nil || runtime.Durations.Source.Milliseconds != 3_570_000 ||
		runtime.Durations.Catalog.Milliseconds != 3_600_000 {
		t.Errorf("runtime durations = %+v; want source 3570000 catalog 3600000", runtime.Durations)
	}
	if runtime.Source == nil || *runtime.Source != "1h" {
		t.Errorf("runtime source = %v; want 1h", runtime.Source)
	}
}

func TestBuildMatchEvidenceRuntimeDifferent(t *testing.T) {
	snap := completeSourceSnap(nil)
	snap.DurationSeconds = 2 * 60 * 60
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	runtime := report.Comparisons["runtime"]
	if runtime.State != models.ComparisonDifferent {
		t.Errorf("runtime = %q; want different", runtime.State)
	}
	if runtime.Durations == nil {
		t.Error("runtime durations missing; the UI needs them for the numeric difference")
	}
}

func TestBuildMatchEvidenceDatePrecisionMismatch(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldDate: {Values: []string{"2020"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	comparison := report.Comparisons["date"]
	if comparison.State != models.ComparisonNotComparable {
		t.Errorf("date = %q; want not_comparable", comparison.State)
	}
	if comparison.Reason != "Source and catalog dates have different precision." {
		t.Errorf("date reason = %q", comparison.Reason)
	}
}

func TestBuildMatchEvidenceMissingSourceValue(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldTitle: {Status: audio.StatusMissing, Source: audio.SourceEmbedded},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	comparison := report.Comparisons["title"]
	if comparison.State != models.ComparisonUnavailable {
		t.Errorf("title = %q; want unavailable", comparison.State)
	}
	if comparison.Source != nil {
		t.Errorf("title source = %v; want null", *comparison.Source)
	}
	if comparison.Selected == nil || *comparison.Selected != "The Book" {
		t.Errorf("title selected = %v; want The Book", comparison.Selected)
	}
}

func TestBuildMatchEvidenceMultipleSourceValues(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldTitle: {Values: []string{"The Book", "Another Book"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	comparison := report.Comparisons["title"]
	if comparison.State != models.ComparisonNotComparable {
		t.Errorf("title = %q; want not_comparable", comparison.State)
	}
}

func TestBuildMatchEvidenceTruncatedValuesNeverVerify(t *testing.T) {
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN:              {Values: []string{"B0ABCDEF01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent, Truncated: true},
		audio.FieldAudiobookdbBookID: {Values: []string{"book-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent, Truncated: true},
		audio.FieldTitle:             {Values: []string{"The Book"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent, Truncated: true},
	})
	report := buildMatchEvidence(snap, evidenceTestCatalog(), false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginTitle {
		t.Errorf("origin = %q; want title", report.Origin)
	}
	for _, id := range report.Identifiers {
		if id.State != models.IdentifierUnresolved {
			t.Errorf("identifier %q state = %q; want unresolved", id.Identifier, id.State)
		}
	}
	if got := report.Comparisons["title"].State; got != models.ComparisonNotComparable {
		t.Errorf("title = %q; want not_comparable", got)
	}
}

func TestBuildMatchEvidenceSelectedEditionMustBelongToBook(t *testing.T) {
	catalog := evidenceTestCatalog()
	catalog.Book.Releases = []audiobookdb.IdTitle{{ID: "release-other", Title: "Another Edition"}}
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldAudiobookdbReleaseID: {Values: []string{"release-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, catalog, false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginTitle {
		t.Errorf("origin = %q; want title", report.Origin)
	}
	if len(report.Identifiers) != 1 || report.Identifiers[0].State != models.IdentifierConflict {
		t.Fatalf("identifiers = %+v; want a conflict", report.Identifiers)
	}
	if report.Identifiers[0].Reason != "The selected edition does not belong to the selected book." {
		t.Errorf("reason = %q", report.Identifiers[0].Reason)
	}
}

// Regression for the review finding: an empty expanded releases list is
// unknown membership, not proof. A source release ID matching the selection
// must stay unresolved so the UI cannot render green "Exact edition ID".
func TestBuildMatchEvidenceUnexpandedReleasesKeepEditionUnresolved(t *testing.T) {
	catalog := evidenceTestCatalog()
	catalog.Book.Releases = nil
	snap := completeSourceSnap(map[string]audio.SourceField{
		audio.FieldAudiobookdbReleaseID: {Values: []string{"release-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	})
	report := buildMatchEvidence(snap, catalog, false, evidenceSelection("book-a", "release-a"))
	if report.Origin != models.MatchOriginTitle {
		t.Errorf("origin = %q; want title (edition must not be verified)", report.Origin)
	}
	if len(report.Identifiers) != 1 || report.Identifiers[0].State != models.IdentifierUnresolved {
		t.Fatalf("identifiers = %+v; want one unresolved edition identifier", report.Identifiers)
	}
	if report.Identifiers[0].ReleaseID != "" {
		t.Errorf("unresolved edition identifier releaseId = %q; want empty", report.Identifiers[0].ReleaseID)
	}
}

func TestGetMatchEvidenceRequiresCatalogBookID(t *testing.T) {
	h := newTestHandler(t)
	req := newMatchEvidenceRequest(t, "1", "")
	w := httptest.NewRecorder()
	h.GetMatchEvidence(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
}

func TestGetMatchEvidenceMissingBook(t *testing.T) {
	h := newTestHandler(t)
	req := newMatchEvidenceRequest(t, "999", "?book_id=book-a")
	w := httptest.NewRecorder()
	h.GetMatchEvidence(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 — body: %s", w.Code, w.Body.String())
	}
}

func TestGetMatchEvidenceWithoutSnapshot(t *testing.T) {
	h := newTestHandler(t)
	if _, err := h.svc.DB.Exec("INSERT INTO books (id, title, status) VALUES (1, 'Book', 'pending')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := newMatchEvidenceRequest(t, "1", "?book_id=book-a&release_id=release-a")
	w := httptest.NewRecorder()
	h.GetMatchEvidence(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var report models.MatchEvidence
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.SourceRead.Status != models.SourceReadNotRead {
		t.Errorf("sourceRead = %q; want not_read", report.SourceRead.Status)
	}
	if report.Selection.BookID != "book-a" || report.Selection.ReleaseID == nil || *report.Selection.ReleaseID != "release-a" {
		t.Errorf("selection = %+v; want book-a/release-a", report.Selection)
	}
}

func TestGetMatchEvidenceDegradesWithoutCatalog(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "pending", "",
		asinSnap([]string{"B0ABCDEF01"}, audio.StatusPresent, audio.SourceEmbedded))

	req := newMatchEvidenceRequest(t, "1", "?book_id=book-a&release_id=release-a")
	w := httptest.NewRecorder()
	h.GetMatchEvidence(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var report models.MatchEvidence
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.SourceRead.Status != models.SourceReadRead {
		t.Errorf("sourceRead = %q; want read", report.SourceRead.Status)
	}
	if len(report.Identifiers) != 1 || report.Identifiers[0].State != models.IdentifierLookupFailed {
		t.Errorf("identifiers = %+v; want one lookup_failed ASIN", report.Identifiers)
	}
	if report.Comparisons["title"].State != models.ComparisonUnavailable {
		t.Errorf("title = %q; want unavailable", report.Comparisons["title"].State)
	}
}

func TestGetMatchEvidenceResolvesAgainstCatalog(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "pending", "", completeSourceSnap(map[string]audio.SourceField{
		audio.FieldASIN:                 {Values: []string{"B0ABCDEF01"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldAudiobookdbReleaseID: {Values: []string{"release-a"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
		audio.FieldTitle:                {Values: []string{"The Book"}, Source: audio.SourceEmbedded, Status: audio.StatusPresent},
	}))
	installCatalogStub(t, h, evidenceTestCatalog())

	req := newMatchEvidenceRequest(t, "1", "?book_id=book-a&release_id=release-a")
	w := httptest.NewRecorder()
	h.GetMatchEvidence(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var report models.MatchEvidence
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.SourceRead.Status != models.SourceReadRead {
		t.Errorf("sourceRead = %q; want read", report.SourceRead.Status)
	}
	if report.Origin != models.MatchOriginEditionID {
		t.Errorf("origin = %q; want edition_id", report.Origin)
	}
	for _, id := range report.Identifiers {
		if id.State != models.IdentifierVerified {
			t.Errorf("identifier %q = %q; want verified", id.Identifier, id.State)
		}
	}
	if report.Comparisons["title"].State != models.ComparisonMatch {
		t.Errorf("title = %q; want match", report.Comparisons["title"].State)
	}
}

// newMatchEvidenceRequest builds a handler request with the chi route
// parameter and query string the endpoint reads.
func newMatchEvidenceRequest(t *testing.T, id, query string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/books/"+id+"/match-evidence"+query, nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

// installCatalogStub points the handler at a local AudiobookDB stub serving
// the given selected book and release.
func installCatalogStub(t *testing.T, h *Handler, catalog *matchCatalog) {
	t.Helper()
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/books/book-a"):
			_ = json.NewEncoder(w).Encode(catalog.Book)
		case strings.HasSuffix(r.URL.Path, "/releases/release-a"):
			_ = json.NewEncoder(w).Encode(catalog.Release)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := new(atomic.Value)
	client.Store(audiobookdb.NewClient("", server.URL))
	h.svc.AudiobookDB = client
}
