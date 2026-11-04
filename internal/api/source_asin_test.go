package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// asinSnap builds a complete single-file snapshot with only the ASIN
// field set to the given values/status/source.
func asinSnap(values []string, fieldStatus, source string) *audio.SourceMetadata {
	return &audio.SourceMetadata{
		Schema: audio.SourceMetadataSchema,
		Fields: map[string]audio.SourceField{
			audio.FieldASIN: {Values: values, Source: source, Status: fieldStatus},
		},
		FilesTotal:  1,
		FilesProbed: 1,
	}
}

// expectASIN asserts the derivation output matches a status plus any set
// expectations; it fails the test on any mismatch.
func expectASIN(t *testing.T, got *models.SourceASIN, wantStatus string) {
	t.Helper()
	if got == nil {
		t.Fatalf("derived SourceASIN is nil; want status %s", wantStatus)
	}
	if got.Status != wantStatus {
		t.Fatalf("status = %q; want %q (got %+v)", got.Status, wantStatus, got)
	}
}

func TestDeriveSourceASINPresentEmbedded(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINPresent)
	if got.Value != "B123456789" || got.Count != 1 || got.Source != audio.SourceEmbedded {
		t.Errorf("present = %+v; want value B123456789 count 1 source embedded", got)
	}
}

func TestDeriveSourceASINPresentFilename(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"B0ABCDEF01"}, audio.StatusPresent, audio.SourceFilename))
	expectASIN(t, got, models.SourceASINPresent)
	if got.Value != "B0ABCDEF01" || got.Count != 1 || got.Source != audio.SourceFilename {
		t.Errorf("present = %+v; want value B0ABCDEF01 count 1 source filename", got)
	}
}

func TestDeriveSourceASINUnknownProvenanceOmitted(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"B123456789"}, audio.StatusPresent, "elsewhere"))
	expectASIN(t, got, models.SourceASINPresent)
	if got.Source != "" {
		t.Errorf("source = %q; want empty for an unrecognized provenance", got.Source)
	}
}

func TestDeriveSourceASINNormalizesCaseAndWhitespace(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"  b123456789 "}, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINPresent)
	if got.Value != "B123456789" {
		t.Errorf("value = %q; want B123456789", got.Value)
	}
}

func TestDeriveSourceASINMalformedSingleValue(t *testing.T) {
	// 8 characters after B (one short of the 9 required).
	got := deriveSourceASIN(asinSnap([]string{"B12345678"}, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("malformed value must not prefill; got value %q", got.Value)
	}
}

func TestDeriveSourceASINCaseDuplicateVariantsCollapse(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"b123456789", "B123456789"}, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINPresent)
	if got.Value != "B123456789" || got.Count != 1 {
		t.Errorf("present = %+v; want single value B123456789 count 1", got)
	}
}

func TestDeriveSourceASINTwoDistinctAmbiguous(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"B123456789", "B987654321"}, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINAmbiguous)
	if got.Count != 2 || got.Value != "" {
		t.Errorf("ambiguous = %+v; want count 2 and no value", got)
	}
}

// 20 raw values (at MaxDistinctValuesPerField) that normalize to one
// distinct ASIN: capped, so no prefill and truncation is flagged.
func TestDeriveSourceASINCappedSingleNormalizedUnknown(t *testing.T) {
	values := make([]string, audio.MaxDistinctValuesPerField)
	for i := range values {
		values[i] = "b123456789"
	}
	got := deriveSourceASIN(asinSnap(values, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINUnknown)
	if !got.CountTruncated {
		t.Errorf("capped single = %+v; want count_truncated", got)
	}
	if got.Value != "" {
		t.Errorf("capped single must not prefill; got value %q", got.Value)
	}
}

// 20 raw values at the cap that normalize to two distinct ASINs:
// ambiguous with the truncation flag set.
func TestDeriveSourceASINCappedTwoNormalizedAmbiguous(t *testing.T) {
	values := make([]string, audio.MaxDistinctValuesPerField)
	for i := range values {
		if i%2 == 0 {
			values[i] = "b123456789"
		} else {
			values[i] = "B987654321"
		}
	}
	got := deriveSourceASIN(asinSnap(values, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINAmbiguous)
	if got.Count != 2 || !got.CountTruncated {
		t.Errorf("capped two = %+v; want count 2 and count_truncated", got)
	}
}

func TestDeriveSourceASINCompleteMissing(t *testing.T) {
	got := deriveSourceASIN(asinSnap(nil, audio.StatusMissing, ""))
	expectASIN(t, got, models.SourceASINMissing)
}

func TestDeriveSourceASINErrorUnknown(t *testing.T) {
	got := deriveSourceASIN(asinSnap(nil, audio.StatusError, ""))
	expectASIN(t, got, models.SourceASINUnknown)
}

func TestDeriveSourceASINIncompleteSingleUnknown(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.FilesProbed = 0
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("incomplete single must not prefill; got value %q", got.Value)
	}
}

func TestDeriveSourceASINProbeErrorUnknown(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.FilesTotal = 2
	snap.FilesProbed = 2
	snap.Files = []audio.SourceFile{
		{File: "1.mp3"},
		{File: "2.mp3", Error: "probe failed"},
	}
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("probe error must not prefill; got value %q", got.Value)
	}
}

func TestDeriveSourceASINIncompleteTwoAmbiguous(t *testing.T) {
	snap := asinSnap([]string{"B123456789", "B987654321"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.FilesProbed = 0
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINAmbiguous)
	if got.Count != 2 {
		t.Errorf("incomplete ambiguous count = %d; want 2", got.Count)
	}
	if !got.CountTruncated {
		t.Errorf("incomplete ambiguous = %+v; want count_truncated (count is not exhaustive)", got)
	}
}

func TestDeriveSourceASINTruncatedSnapshotUnknown(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.Truncated = true
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("truncated snapshot must not prefill; got value %q", got.Value)
	}
}

func TestDeriveSourceASINCompleteMissingField(t *testing.T) {
	snap := &audio.SourceMetadata{
		Schema:      audio.SourceMetadataSchema,
		FilesTotal:  1,
		FilesProbed: 1,
		Fields:      map[string]audio.SourceField{},
	}
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINMissing)
}

func TestDeriveSourceASINIncompleteMissingFieldUnknown(t *testing.T) {
	snap := &audio.SourceMetadata{
		Schema:      audio.SourceMetadataSchema,
		FilesTotal:  2,
		FilesProbed: 1,
		Fields:      map[string]audio.SourceField{},
	}
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
}

func TestDeriveSourceASINSchemaMismatchUnknown(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.Schema = audio.SourceMetadataSchema + 1
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("schema mismatch must not prefill; got value %q", got.Value)
	}
}

func TestDeriveSourceASINNil(t *testing.T) {
	if got := deriveSourceASIN(nil); got != nil {
		t.Fatalf("nil snapshot must derive nil; got %+v", got)
	}
}

// seedBookWithASINMetadata inserts a book with the given id/status/asin
// and a book_source_metadata row carrying snap's JSON payload.
func seedBookWithASINMetadata(t *testing.T, h *Handler, id int64, status, asin string, snap *audio.SourceMetadata) {
	t.Helper()
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, asin, status) VALUES (?, ?, NULLIF(?, ''), ?)",
		id, fmt.Sprintf("Book %d", id), asin, status,
	); err != nil {
		t.Fatalf("seed book %d: %v", id, err)
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot for book %d: %v", id, err)
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO book_source_metadata (book_id, payload) VALUES (?, ?)",
		id, payload,
	); err != nil {
		t.Fatalf("seed metadata for book %d: %v", id, err)
	}
}

func TestListBooksPendingIncludesSourceASIN(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "pending", "LEGACY12345",
		asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded))
	seedBookWithASINMetadata(t, h, 2, "pending", "",
		asinSnap([]string{"B123456789", "B987654321"}, audio.StatusPresent, audio.SourceEmbedded))

	req := httptest.NewRequest("GET", "/api/books?status=pending", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 2 {
		t.Fatalf("books = %d; want 2", len(resp.Books))
	}
	byID := map[int64]*models.BookWithPeople{}
	for i := range resp.Books {
		byID[resp.Books[i].ID] = &resp.Books[i]
	}

	present := byID[1]
	if present == nil || present.SourceASIN == nil {
		t.Fatalf("book 1 missing or without source_asin; books: %+v", resp.Books)
	}
	if present.SourceASIN.Status != models.SourceASINPresent ||
		present.SourceASIN.Value != "B123456789" ||
		present.SourceASIN.Count != 1 ||
		present.SourceASIN.Source != audio.SourceEmbedded {
		t.Errorf("book 1 source_asin = %+v; want present B123456789 count 1 embedded", present.SourceASIN)
	}
	// books.asin must remain untouched by the projection.
	if !present.ASIN.Valid || present.ASIN.String != "LEGACY12345" {
		t.Errorf("book 1 asin = %v; want LEGACY12345 untouched", present.ASIN)
	}

	amb := byID[2]
	if amb == nil || amb.SourceASIN == nil {
		t.Fatalf("book 2 missing or without source_asin; books: %+v", resp.Books)
	}
	if amb.SourceASIN.Status != models.SourceASINAmbiguous || amb.SourceASIN.Count != 2 {
		t.Errorf("book 2 source_asin = %+v; want ambiguous count 2", amb.SourceASIN)
	}
	if amb.ASIN.Valid {
		t.Errorf("book 2 asin = %v; want NULL untouched", amb.ASIN)
	}
}

func TestListBooksDoneOmitsSourceASIN(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "done", "",
		asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded))

	req := httptest.NewRequest("GET", "/api/books?status=done", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("books = %d; want 1", len(resp.Books))
	}
	if resp.Books[0].SourceASIN != nil {
		t.Errorf("done list must omit source_asin; got %+v", resp.Books[0].SourceASIN)
	}
	// Raw payload must never leak: the wire body is just the contract.
	if contains := w.Body.String(); len(contains) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("re-decode wire body: %v", err)
		}
		if _, ok := raw["raw_tags"]; ok {
			t.Errorf("wire response leaks raw source metadata payload")
		}
	}
}

func TestListBooksPendingMalformedMetadataRow(t *testing.T) {
	h := newTestHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book 1', 'pending')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO book_source_metadata (book_id, payload) VALUES (1, 'not-json')",
	); err != nil {
		t.Fatalf("seed malformed metadata: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/books?status=pending", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("books = %d; want 1", len(resp.Books))
	}
	if resp.Books[0].SourceASIN != nil {
		t.Errorf("malformed payload must leave source_asin nil; got %+v", resp.Books[0].SourceASIN)
	}
}
