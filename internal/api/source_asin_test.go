package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// sourceMetadataPayloadKeys is the set of top-level JSON keys a stored
// audio.SourceMetadata payload carries. If any of these appears as a key
// on a wire book object, the raw payload leaked into the response.
var sourceMetadataPayloadKeys = map[string]bool{
	"schema":           true,
	"source_kind":      true,
	"files_total":      true,
	"files_probed":     true,
	"truncated":        true,
	"degraded":         true,
	"duration_seconds": true,
	"files":            true,
	"fields":           true,
	"raw_tags":         true,
}

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

// truncatedSnap builds a complete single-file snapshot whose ASIN field
// carries the per-field cap hit.
func truncatedSnap(values []string) *audio.SourceMetadata {
	snap := asinSnap(values, audio.StatusPresent, audio.SourceEmbedded)
	field := snap.Fields[audio.FieldASIN]
	field.Truncated = true
	snap.Fields[audio.FieldASIN] = field
	return snap
}

// A truncated field whose retained values normalize to one ASIN still
// degrades to unknown: the dropped value may have been a differing ASIN.
func TestDeriveSourceASINTruncatedSingleNormalizedUnknown(t *testing.T) {
	got := deriveSourceASIN(truncatedSnap([]string{"b123456789", "B123456789"}))
	expectASIN(t, got, models.SourceASINUnknown)
	if !got.CountTruncated {
		t.Errorf("truncated single = %+v; want count_truncated", got)
	}
	if got.Value != "" {
		t.Errorf("truncated single must not prefill; got value %q", got.Value)
	}
}

// A truncated field retaining two distinct ASINs is ambiguous with the
// truncation flag set.
func TestDeriveSourceASINTruncatedTwoNormalizedAmbiguous(t *testing.T) {
	got := deriveSourceASIN(truncatedSnap([]string{"B123456789", "b987654321"}))
	expectASIN(t, got, models.SourceASINAmbiguous)
	if got.Count != 2 || !got.CountTruncated {
		t.Errorf("truncated two = %+v; want count 2 and count_truncated", got)
	}
	if got.Value != "" {
		t.Errorf("truncated two must not prefill; got value %q", got.Value)
	}
}

// TestDeriveSourceASINFullValueListPrefills is the regression for the
// cap-length inference: a value list of exactly the per-field cap that
// every entry of normalizes to ONE ASIN is complete evidence of that
// ASIN, so the projection must prefill. The old code read
// len(field.Values) >= MaxDistinctValuesPerField as "capped" and degraded
// this to unknown/truncated — a false positive at exactly the cap, which
// 20 distinct raw spellings (case and whitespace variants) reach without
// any value being dropped.
func TestDeriveSourceASINFullValueListPrefills(t *testing.T) {
	values := make([]string, 20)
	for i := range values {
		// Distinct raw strings, one normalized ASIN.
		values[i] = strings.Repeat(" ", i) + "b123456789" + strings.Repeat(" ", 19-i)
	}
	snap := asinSnap(values, audio.StatusPresent, audio.SourceEmbedded)
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINPresent)
	if got.Value != "B123456789" || got.Count != 1 || got.CountTruncated {
		t.Errorf("present = %+v; want value B123456789 count 1 without count_truncated", got)
	}
}

// A list at the cap that normalizes to two ASINs is genuinely ambiguous,
// but its count is not exhaustive: the cap may have dropped a third.
func TestDeriveSourceASINFullValueListTwoNormalizedAmbiguous(t *testing.T) {
	values := make([]string, 20)
	for i := range values {
		if i%2 == 0 {
			values[i] = "b123456789"
		} else {
			values[i] = " B987654321"
		}
	}
	got := deriveSourceASIN(asinSnap(values, audio.StatusPresent, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINAmbiguous)
	if got.Count != 2 {
		t.Errorf("ambiguous count = %d; want 2", got.Count)
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

// TestDeriveSourceASINMissingWithValuesUnknown pins the contradictory
// missing-status-with-retained-values branch: a field marked missing that
// still carries values degrades to unknown and never prefills the retained
// ASIN — a missing claim with evidence is untrusted, not authoritative.
func TestDeriveSourceASINMissingWithValuesUnknown(t *testing.T) {
	got := deriveSourceASIN(asinSnap([]string{"B123456789"}, audio.StatusMissing, audio.SourceEmbedded))
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("missing-with-values must not prefill; got value %q", got.Value)
	}
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

// TestDeriveSourceASINStatFailureFilenameASINUnknown reproduces the
// stat-failed source: the snapshot is degraded, the ASIN field survived
// from the filename as present, and the derivation must not prefill from
// that incomplete evidence.
func TestDeriveSourceASINStatFailureFilenameASINUnknown(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceFilename)
	snap.Degraded = true
	snap.FilesTotal = 0
	snap.FilesProbed = 0
	got := deriveSourceASIN(snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("stat-failed source must not prefill; got value %q", got.Value)
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

// legacyStatFailurePayload is a literal schema-1 stat-failure snapshot, as
// persisted before the Degraded flag existed: no "degraded" key, no files,
// zero counts, and a filename ASIN that survived. Decoded against the
// current struct it is Degraded == false, FilesTotal == FilesProbed == 0,
// and Complete() returns true — the upgrade hole.
const legacyStatFailurePayload = `{
  "schema": 1,
  "source_kind": "unknown",
  "files_total": 0,
  "files_probed": 0,
  "truncated": false,
  "duration_seconds": 0,
  "files": [],
  "fields": {
    "asin": {"values": ["B012345678"], "source": "filename", "status": "present"}
  },
  "raw_tags": []
}`

// TestDeriveSourceASINLegacyPayloadUnknown pins the schema-bump contract: a
// pre-Degraded persisted row must not prefill an ASIN on upgrade. The
// conservative read (any older schema is incomplete evidence) is what keeps
// this correct without guessing from the payload's shape.
func TestDeriveSourceASINLegacyPayloadUnknown(t *testing.T) {
	var snap audio.SourceMetadata
	if err := json.Unmarshal([]byte(legacyStatFailurePayload), &snap); err != nil {
		t.Fatalf("unmarshal legacy payload: %v", err)
	}
	// The decode really does look "complete" to Complete(); only the schema
	// test saves it.
	if !snap.Complete() {
		t.Fatalf("legacy payload should decode as Complete() to exercise the schema gate; got %+v", snap)
	}
	if snap.Schema != 1 {
		t.Fatalf("legacy payload schema = %d; want 1", snap.Schema)
	}
	got := deriveSourceASIN(&snap)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("legacy payload must not prefill; got value %q", got.Value)
	}
}

// TestDeriveSourceASINLegacyPayloadMissingFieldUnknown covers the other
// legacy shape: no ASIN field at all. A schema-1 row must not claim the
// field is genuinely missing either.
func TestDeriveSourceASINLegacyPayloadMissingFieldUnknown(t *testing.T) {
	var snap audio.SourceMetadata
	legacy := `{"schema": 1, "files_total": 0, "files_probed": 0, "fields": {}, "files": [], "raw_tags": []}`
	if err := json.Unmarshal([]byte(legacy), &snap); err != nil {
		t.Fatalf("unmarshal legacy payload: %v", err)
	}
	got := deriveSourceASIN(&snap)
	expectASIN(t, got, models.SourceASINUnknown)
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
	body := w.Body.String()
	var resp ListBooksResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("books = %d; want 1", len(resp.Books))
	}
	if resp.Books[0].SourceASIN != nil {
		t.Errorf("done list must omit source_asin; got %+v", resp.Books[0].SourceASIN)
	}
	// Raw payload must never leak: the wire body is just the contract, and
	// a SourceMetadata payload would only appear nested inside a book.
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("re-decode wire body: %v", err)
	}
	books, ok := raw["books"].([]any)
	if !ok || len(books) != 1 {
		t.Fatalf("wire body books = %v; want exactly one entry", raw["books"])
	}
	book, ok := books[0].(map[string]any)
	if !ok {
		t.Fatalf("wire body books[0] = %T; want an object", books[0])
	}
	for key := range book {
		if _, isPayloadField := sourceMetadataPayloadKeys[key]; isPayloadField {
			t.Errorf("wire response leaks source metadata payload field %q", key)
		}
	}
}

// TestListBooksPendingFullValueListPrefills drives the false positive end
// to end: 20 files carry 20 DISTINCT raw ASIN spellings (whitespace and
// case variants) that all normalize to one ASIN, so the retained list has
// exactly the per-field cap's worth of entries and nothing was dropped.
// The old cap-length inference saw len == 20 and degraded this to
// unknown/truncated; the field's own Truncated flag is false, so the
// projection prefills.
func TestListBooksPendingFullValueListPrefills(t *testing.T) {
	srcDir := t.TempDir()
	ffprobe, respDir := stubFFprobeForMetadata(t)
	for i := range 20 {
		name := fmt.Sprintf("%03d.mp3", i)
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
		// Distinct raw strings (leading/trailing spaces plus case variants),
		// one normalized ASIN after trim+uppercase.
		raw := strings.Repeat(" ", i%5) + "b123456789" + strings.Repeat(" ", i/5)
		if i%2 == 1 {
			raw = strings.ToUpper(raw)
		}
		writeProbeSidecar(t, respDir, name,
			fmt.Sprintf(`{"format": {"duration": "1", "tags": {"audible_asin": "%s"}}, "streams": []}`, raw))
	}

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	book := postBook(t, h, srcDir)

	var snap audio.SourceMetadata
	var payload string
	if err := h.svc.DB.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = ?", book.ID).
		Scan(&payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	asinField := snap.Fields[audio.FieldASIN]
	if len(asinField.Values) != 20 {
		t.Fatalf("asin values = %d; want 20 distinct raw spellings", len(asinField.Values))
	}
	if asinField.Truncated {
		t.Error("asin field truncated = true; nothing was dropped at exactly the cap")
	}
	if !snap.Complete() {
		t.Error("Complete() = false; a complete 20-file pass is complete evidence")
	}

	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?status=pending", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Books) != 1 || resp.Books[0].SourceASIN == nil {
		t.Fatalf("books = %+v; want one book with source_asin", resp.Books)
	}
	got := resp.Books[0].SourceASIN
	if got.Status != models.SourceASINPresent || got.Value != "B123456789" ||
		got.Count != 1 || got.CountTruncated {
		t.Errorf("source_asin = %+v; want present B123456789 count 1 without count_truncated", got)
	}
}

// TestListBooksAllIDsOmitsSourceASIN pins the code path's claim: the bulk
// selection mode returns only ids/total/truncated, so the projection is
// not attached (and cannot leak) there.
func TestListBooksAllIDsOmitsSourceASIN(t *testing.T) {
	h := newTestHandler(t)
	seedBookWithASINMetadata(t, h, 1, "pending", "",
		asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded))

	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?all_ids=1&status=pending", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "source_asin") {
		t.Errorf("all_ids body carries source_asin: %s", body)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("re-decode wire body: %v", err)
	}
	ids, ok := raw["ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != float64(1) {
		t.Fatalf("wire ids = %v; want [1]", raw["ids"])
	}
	for _, key := range []string{"books", "source_asin"} {
		if _, present := raw[key]; present {
			t.Errorf("all_ids body carries %q; want the ids contract only", key)
		}
	}
}

// TestListBooksPendingWireCountTruncatedKey asserts the serialized key, not
// just the Go field: a client reads `count_truncated`, so a renamed tag
// would silently drop the evidence.
func TestListBooksPendingWireCountTruncatedKey(t *testing.T) {
	h := newTestHandler(t)
	// An incomplete pass with two distinct ASINs: ambiguous, and its count
	// is not exhaustive.
	incomplete := asinSnap([]string{"B123456789", "B987654321"}, audio.StatusPresent, audio.SourceEmbedded)
	incomplete.FilesProbed = 0
	seedBookWithASINMetadata(t, h, 1, "pending", "", incomplete)

	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?status=pending", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var raw struct {
		Books []map[string]any `json:"books"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("re-decode wire body: %v", err)
	}
	if len(raw.Books) != 1 {
		t.Fatalf("wire books = %d; want 1", len(raw.Books))
	}
	evidence, ok := raw.Books[0]["source_asin"].(map[string]any)
	if !ok {
		t.Fatalf("wire source_asin = %T; want an object", raw.Books[0]["source_asin"])
	}
	if got, ok := evidence["count_truncated"].(bool); !ok || !got {
		t.Errorf("wire count_truncated = %v; want true", evidence["count_truncated"])
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

// TestSourceMetadataSchemaTwoFlagsRoundTrip pins the JSON tags of the two
// load-bearing schema-2 flags: the snapshot Degraded flag and the per-field
// Truncated flag must survive a marshal/unmarshal cycle exactly as a stored
// payload does — a dropped key would silently turn incomplete evidence into
// complete evidence. A decoded snapshot with both flags set must not
// prefill the ASIN.
func TestSourceMetadataSchemaTwoFlagsRoundTrip(t *testing.T) {
	snap := asinSnap([]string{"B123456789"}, audio.StatusPresent, audio.SourceEmbedded)
	snap.Degraded = true
	field := snap.Fields[audio.FieldASIN]
	field.Truncated = true
	snap.Fields[audio.FieldASIN] = field

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var decoded audio.SourceMetadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if !decoded.Degraded {
		t.Errorf("decoded Degraded = false; want true (the JSON tag must carry the flag)")
	}
	decodedField, ok := decoded.Fields[audio.FieldASIN]
	if !ok {
		t.Fatalf("decoded snapshot lost the %s field", audio.FieldASIN)
	}
	if !decodedField.Truncated {
		t.Errorf("decoded field Truncated = false; want true (the JSON tag must carry the flag)")
	}
	got := deriveSourceASIN(&decoded)
	expectASIN(t, got, models.SourceASINUnknown)
	if got.Value != "" {
		t.Errorf("decoded snapshot must not prefill; got value %q", got.Value)
	}
}

// TestListBooksPendingMetadataQueryFailureLeavesNil pins attachSourceASINs'
// best-effort contract: a query failure (here: the metadata table is
// absent) is logged and leaves every book's source_asin nil — it never
// fails the list.
func TestListBooksPendingMetadataQueryFailureLeavesNil(t *testing.T) {
	h := newTestHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book 1', 'pending')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec("DROP TABLE book_source_metadata"); err != nil {
		t.Fatalf("drop metadata table: %v", err)
	}

	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?status=pending", nil))
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
		t.Errorf("query failure must leave source_asin nil; got %+v", resp.Books[0].SourceASIN)
	}
}
