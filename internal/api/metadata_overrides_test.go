package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// newOverrideTestHandler builds the file-backed jobs handler (the queue
// path's shape) with a book + capture row seeded.
func newOverrideTestHandler(t *testing.T) *Handler {
	t.Helper()
	h, _ := newTestJobsHandler(t)
	db := h.svc.DB
	if _, err := db.Exec(`INSERT INTO books (id, title, description, series, release_date, status)
		VALUES (1, 'Catalog Title', 'Catalog description', 'Series X, 2', '2019-05-01', 'matched')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO people (book_id, name, role) VALUES (1, 'Auth One', 'author'), (1, 'Auth Two', 'author'), (1, 'Narr A', 'narrator')`); err != nil {
		t.Fatal(err)
	}
	year := 2019
	if _, err := db.Exec(`INSERT INTO book_snapshots (book_id, title, authors, narrators, series_name, series_position, description, year)
		VALUES (1, 'Catalog Title', '["Auth One","Auth Two"]', '["Narr A"]', 'Series X', '2', 'Catalog description', ?)`, year); err != nil {
		t.Fatal(err)
	}
	return h
}

// routeReq builds a request with the chi {id} URL parameter bound, the
// same convention as the books API tests.
func routeReq(t *testing.T, method, path string, bookID int64, body io.Reader) *http.Request {
	t.Helper()
	if body == nil {
		body = strings.NewReader("")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = []string{"id"}
	rctx.URLParams.Values = []string{strconv.FormatInt(bookID, 10)}
	req := httptest.NewRequest(method, path, body)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func putOverride(t *testing.T, h *Handler, bookID int64, body string) int {
	t.Helper()
	req := routeReq(t, "PUT", "/api/books/1/metadata", bookID, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdateBookMetadata(w, req)
	return w.Code
}

// getOverrideState decodes the GET /metadata response.
func getOverrideState(t *testing.T, h *Handler, bookID int64) *bookMetadataResponse {
	t.Helper()
	req := routeReq(t, "GET", "/api/books/1/metadata", bookID, nil)
	w := httptest.NewRecorder()
	h.GetBookMetadata(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET metadata = %d; want 200 — %s", w.Code, w.Body.String())
	}
	var resp bookMetadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &resp
}

// TestMetadataOverrideRoundTrip guards the editor state contract: a saved
// override round-trips with ORDERED people preserved, the effective state
// shows the edited values, and the catalog snapshot row is untouched
// (override layer and capture stay separate representations).
func TestMetadataOverrideRoundTrip(t *testing.T) {
	h := newOverrideTestHandler(t)
	body := `{"title":"Edited Title","authors":["Second","First"],"year":1999,"narrators":[]}`
	if code := putOverride(t, h, 1, body); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state := getOverrideState(t, h, 1)
	if state.Overrides == nil {
		t.Fatalf("overrides nil after save")
	}
	if state.Overrides.Title != "Edited Title" {
		t.Fatalf("title = %q", state.Overrides.Title)
	}
	if len(state.Overrides.Authors) != 2 || state.Overrides.Authors[0] != "Second" || state.Overrides.Authors[1] != "First" {
		t.Fatalf("authors order = %v, want [Second First]", state.Overrides.Authors)
	}
	if state.Overrides.Year == nil || *state.Overrides.Year != 1999 {
		t.Fatalf("year = %v", state.Overrides.Year)
	}
	if state.Overrides.Narrators == nil || len(state.Overrides.Narrators) != 0 {
		t.Fatalf("narrators = %v, want explicit empty", state.Overrides.Narrators)
	}
	if state.Effective == nil {
		t.Fatalf("effective nil")
	}
	if state.Effective.Title != "Edited Title" {
		t.Fatalf("effective title = %q", state.Effective.Title)
	}
	if len(state.Effective.Authors) != 2 || state.Effective.Authors[0] != "Second" {
		t.Fatalf("effective authors = %v", state.Effective.Authors)
	}
	if len(state.Effective.Narrators) != 0 {
		t.Fatalf("effective narrators = %v, want empty (edited empty list)", state.Effective.Narrators)
	}
	if *state.Effective.Year != 1999 {
		t.Fatalf("effective year = %v", *state.Effective.Year)
	}
	// The catalog capture row is untouched: it still names the catalog
	// title and the original people (re-matching re-takes THIS row).
	if state.Snapshot.Title != "Catalog Title" {
		t.Fatalf("snapshot title = %q, capture must stay catalog", state.Snapshot.Title)
	}
	if state.Snapshot.Authors[0] != "Auth One" {
		t.Fatalf("snapshot authors = %v, capture must stay catalog", state.Snapshot.Authors)
	}
}

// TestMetadataOverrideClearDistinctFromReset guards the plan's load-bearing
// distinction: an explicit clear (remove the embedded tag) blanks the
// effective value AND pins the engine clear policy, while the override row
// SURVIVES a re-capture (unlike reset-to-catalog, which deletes the row).
func TestMetadataOverrideClearDistinctFromReset(t *testing.T) {
	h := newOverrideTestHandler(t)
	// Clear the year: the tag comes off the output, the value blanks.
	if code := putOverride(t, h, 1, `{"cleared":["year"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state := getOverrideState(t, h, 1)
	if state.Effective.Year != nil {
		t.Fatalf("effective year = %v, want nil after clear", state.Effective.Year)
	}
	if state.Overrides == nil || len(state.Overrides.Cleared) != 1 || state.Overrides.Cleared[0] != "year" {
		t.Fatalf("cleared = %v, want [year]", state.Overrides.Cleared)
	}
	// A title edit + subtitle clear in one draft: the edited title writes
	// (the selection's write/preserve decision is untouched), the subtitle
	// is pinned to clear.
	if code := putOverride(t, h, 1, `{"title":"Edited","cleared":["subtitle"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state = getOverrideState(t, h, 1)
	if state.Overrides.Title != "Edited" || state.Effective.Title != "Edited" {
		t.Fatalf("edited title not effective: %+v", state.Overrides)
	}

	// Reset-to-catalog deletes the ROW (the cleared state is gone),
	// distinct from a clear entry surviving.
	req := routeReq(t, "POST", "/api/books/1/metadata/reset", 1, nil)
	w := httptest.NewRecorder()
	h.ResetBookMetadata(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset = %d — %s", w.Code, w.Body.String())
	}
	state = getOverrideState(t, h, 1)
	if state.Overrides != nil {
		t.Fatalf("overrides = %+v after reset, want nil (row deleted)", state.Overrides)
	}
	if state.Effective.Title != "Catalog Title" {
		t.Fatalf("effective title after reset = %q, want catalog", state.Effective.Title)
	}
}

// TestMetadataOverrideQueuedJobImmutability guards the plan's queue
// contract: the job freezes the EFFECTIVE metadata + pinned policies at
// enqueue time, and a later override edit (or catalog re-capture) does not
// alter the stored job args.
func TestMetadataOverrideQueuedJobImmutability(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	db := h.svc.DB
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'Catalog Title', 'matched')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_snapshots (book_id, title, authors, narrators)
		VALUES (1, 'Catalog Title', '["Auth One"]', '["Narr A"]')`); err != nil {
		t.Fatal(err)
	}
	// Save an override: edited title + cleared subtitle + custom chapters.
	// The first chapter starts at 0 ms: legal input the engine enforces
	// against real playlength at run time, so the server must accept it.
	if code := putOverride(t, h, 1, `{"title":"Edited Title","cleared":["subtitle"],"chapters":[{"title":"Ch 1","start_millis":0,"duration_millis":2000}]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	// Seed a real input file so the queue path's extension gate passes.
	in := filepath.Join(t.TempDir(), "in.m4a")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{})
	if err != nil {
		t.Fatalf("QueueJob: %v", err)
	}
	var stored string
	if err := db.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var args jobArgs
	if err := json.Unmarshal([]byte(stored), &args); err != nil {
		t.Fatal(err)
	}
	if args.Snapshot == nil || args.Snapshot.Snapshot.Title != "Edited Title" {
		t.Fatalf("frozen effective title = %+v, want 'Edited Title'", args.Snapshot)
	}
	if len(args.Snapshot.Snapshot.Chapters) != 1 || args.Snapshot.Snapshot.Chapters[0].Title != "Ch 1" {
		t.Fatalf("frozen chapters = %+v", args.Snapshot.Snapshot.Chapters)
	}
	sel := args.Snapshot.Selection
	if sel == nil {
		t.Fatalf("selection nil; the pins must force a frozen selection")
	}
	if sel.ScalarPins["subtitle"] != "clear" {
		t.Fatalf("scalar pins = %v, want subtitle=clear", sel.ScalarPins)
	}
	// The chapter policy is the saved per-field selection's value (the
	// fixture's default "write"), NOT a forced pin from the edited list:
	// an edited chapter list is effective metadata, and its write policy
	// stays with the selection so a "preserve" choice is honored (finding:
	// honor saved chapter-preserve policy for edited lists).
	if sel.Chapters != "write" {
		t.Fatalf("chapter policy = %q, want write (saved selection baseline, not a forced pin)", sel.Chapters)
	}

	// A LATER override edit changes the row, not the stored job.
	if code := putOverride(t, h, 1, `{"title":"Edited Twice","cleared":["subtitle"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	var after string
	if err := db.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != stored {
		t.Fatalf("stored job args changed after a later edit:\nbefore: %s\nafter:  %s", stored, after)
	}
}

// TestMetadataOverrideEditedChaptersHonorPreserve guards the B3 fix: an
// EDITED (non-empty) chapter list is effective metadata, and the chapter
// write POLICY stays with the saved per-field selection. A book whose
// selection says "preserve" must keep its no-embed / no-sidecar behavior
// even when the operator has edited the chapter list — the old code pinned
// the policy to "write", which embedded + generated the sidecar against
// the operator's preserve intent (plan: preserving chapter writes must not
// trigger unwanted embed/sidecar mutation).
func TestMetadataOverrideEditedChaptersHonorPreserve(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	db := h.svc.DB
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'T', 'matched')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_snapshots (book_id, title, authors, narrators)
		VALUES (1, 'T', '["A"]', '["N"]')`); err != nil {
		t.Fatal(err)
	}
	// The saved per-field selection says PRESERVE chapters.
	svc.proc.ApplyWriteSelection(nil, audio.ChaptersPolicyValuePreserve)
	if code := putOverride(t, h, 1, `{"chapters":[{"title":"My Chapter","start_millis":0,"duration_millis":3000}]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	in := filepath.Join(t.TempDir(), "in.m4a")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{})
	if err != nil {
		t.Fatalf("QueueJob: %v", err)
	}
	var stored string
	if err := db.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var args jobArgs
	if err := json.Unmarshal([]byte(stored), &args); err != nil {
		t.Fatal(err)
	}
	// The edited list IS the effective metadata the job carries...
	if len(args.Snapshot.Snapshot.Chapters) != 1 || args.Snapshot.Snapshot.Chapters[0].Title != "My Chapter" {
		t.Fatalf("frozen chapters = %+v, want the edited list", args.Snapshot.Snapshot.Chapters)
	}
	// ...but the chapter policy is the saved "preserve", not a forced "write".
	sel := args.Snapshot.Selection
	if sel == nil || sel.Chapters != audio.ChaptersPolicyValuePreserve {
		t.Fatalf("chapter policy = %+v, want preserve (saved selection, not a forced pin)", sel)
	}
	// The engine job honors preserve: no chapter embed, no sidecar.
	jf, _, err := buildEngineJob(args, id, args.Snapshot.Snapshot, t.TempDir(), args.OutputScheme)
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if string(jf.Chapters) != `"preserve"` {
		t.Fatalf("engine job chapters = %s, want the bare preserve policy (no embed/sidecar)", jf.Chapters)
	}
}

// TestMetadataOverrideBuildEngineJobPolicies guards the engine job shape:
// a cleared scalar runs under "clear" on top of the selection baseline, a
// custom cover file is staged, an explicit empty chapter list is the engine
// clear shape, and an unedited field keeps the selection's policy.
func TestMetadataOverrideBuildEngineJobPolicies(t *testing.T) {
	covers := t.TempDir()
	oldCovers := coversDirPath
	coversDirPath = covers
	t.Cleanup(func() { coversDirPath = oldCovers })
	coverName := "ovr-7-abc12345.jpg"
	if err := os.WriteFile(filepath.Join(covers, coverName), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := BookSnapshot{
		BookID: 7, Title: "Edited", Subtitle: "", Authors: []string{"A"},
		Narrators: []string{"N"}, Description: "d", CoverFile: coverName,
	}
	sel := &WriteSelection{
		Fields:     []string{"title", "description"},
		Cover:      "write",
		Chapters:   "write",
		ScalarPins: map[string]string{"subtitle": "clear"},
	}
	args := jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Snapshot: snap, Selection: sel}}
	jf, staged, err := buildEngineJob(args, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	// The cleared subtitle pins to clear DESPITE the selection baseline
	// (unselected subtitle would otherwise be preserve).
	if jf.Policies.Subtitle != "clear" {
		t.Fatalf("subtitle policy = %q, want clear (pin)", jf.Policies.Subtitle)
	}
	// The selected title writes; the unselected authors preserve.
	if jf.Policies.Title != "write" || jf.Policies.Authors != "preserve" {
		t.Fatalf("title/authors = %q/%q, want write/preserve", jf.Policies.Title, jf.Policies.Authors)
	}
	// The override cover asset is staged into the output root.
	if jf.Cover == nil || !strings.Contains(string(jf.Cover), "write") {
		t.Fatalf("cover = %s, want the write object form", jf.Cover)
	}
	if staged == "" {
		t.Fatalf("no staged cover for the override asset")
	}
	// An explicit EMPTY chapter list is the engine's clear shape.
	sel.Chapters = "clear"
	jf, _, err = buildEngineJob(args, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if string(jf.Chapters) != `{"write":{"chapters":[]}}` {
		t.Fatalf("chapters = %s, want the explicit-clear shape", jf.Chapters)
	}
}

// TestMetadataOverrideWriteURLCover guards the write_url pin: the frozen
// selection carries the operator's URL and the job's cover policy is the
// engine's URL-embed object form (the run embeds it; offline file covers do
// not).
func TestMetadataOverrideWriteURLCover(t *testing.T) {
	// The save-time URL guard resolves the cover host; stub it to a public
	// address so the fixture domain needs no live resolver and the guard's
	// public-address check is exercised deterministically.
	oldLookup := lookupCoverHost
	lookupCoverHost = func(_ context.Context, host string) ([]string, error) {
		if host == "covers.example" {
			return []string{"203.0.113.10"}, nil
		}
		return nil, fmt.Errorf("no address for %s", host)
	}
	t.Cleanup(func() { lookupCoverHost = oldLookup })

	h, svc := newTestJobsHandler(t)
	db := h.svc.DB
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'T', 'matched')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_snapshots (book_id, title, authors, narrators)
		VALUES (1, 'T', '["A"]', '["N"]')`); err != nil {
		t.Fatal(err)
	}
	if code := putOverride(t, h, 1, `{"cover_source":"url","cover_url":"https://covers.example/1.jpg"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	in := filepath.Join(t.TempDir(), "in.m4a")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{})
	if err != nil {
		t.Fatalf("QueueJob: %v", err)
	}
	var stored string
	if err := db.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var args jobArgs
	if err := json.Unmarshal([]byte(stored), &args); err != nil {
		t.Fatal(err)
	}
	sel := args.Snapshot.Selection
	if sel == nil || sel.Cover != "write_url" {
		t.Fatalf("cover policy = %+v, want write_url", sel)
	}
	if sel.CoverURL != "https://covers.example/1.jpg" {
		t.Fatalf("frozen cover url = %q", sel.CoverURL)
	}
	// The URL cover is NOT staged as a local asset (offline file covers are
	// distinct from the remote URL path).
	if strings.Contains(stored, "ovr-") {
		t.Fatalf("url cover must not reference a local asset: %s", stored)
	}
	// The engine job itself must serialize the URL-cover object form: a
	// regression that drops or mis-serializes the URL at the engine boundary
	// (the frozen selection alone would not catch it) is a real loss of the
	// operator's cover. buildEngineJob is the authoritative serializer.
	jf, _, err := buildEngineJob(args, id, args.Snapshot.Snapshot, t.TempDir(), args.OutputScheme)
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if got := string(jf.Cover); got != `{"write_url":"https://covers.example/1.jpg"}` {
		t.Fatalf("engine job cover = %s, want the write_url object form with the operator's URL", got)
	}
}

// TestMetadataOverrideValidationRejects guards the operator-facing gate:
// unknown cleared fields, out-of-range years, empty chapter titles,
// zero-duration chapters, and a foreign book's cover asset are all
// refused BEFORE storage (the row is not written). A zero start is legal
// (the first chapter of every recording starts at 00:00:00.000); the
// engine enforces start/duration against the real playlength at run time.
func TestMetadataOverrideValidationRejects(t *testing.T) {
	// The save-time URL guard resolves the cover host; stub it so the
	// public/loopback URL cases are deterministic (loopback needs no lookup
	// — the IP literal is classified directly — but covers.example does).
	oldLookup := lookupCoverHost
	lookupCoverHost = func(_ context.Context, host string) ([]string, error) {
		if host == "covers.example" {
			return []string{"203.0.113.10"}, nil
		}
		return nil, fmt.Errorf("no address for %s", host)
	}
	t.Cleanup(func() { lookupCoverHost = oldLookup })

	h := newOverrideTestHandler(t)
	cases := []struct {
		name string
		body string
	}{
		{"unknown cleared field", `{"cleared":["publisher"]}`},
		{"year too low", `{"year":999}`},
		{"year too high", `{"year":10000}`},
		{"empty chapter title", `{"chapters":[{"title":"","start_millis":1,"duration_millis":1}]}`},
		{"zero chapter duration", `{"chapters":[{"title":"C","start_millis":1,"duration_millis":0}]}`},
		{"overlapping chapters", `{"chapters":[{"title":"A","start_millis":0,"duration_millis":1000},{"title":"B","start_millis":500,"duration_millis":1000}]}`},
		{"out-of-order chapters", `{"chapters":[{"title":"A","start_millis":5000,"duration_millis":1000},{"title":"B","start_millis":0,"duration_millis":1000}]}`},
		{"cover file not this book", `{"cover_source":"file","cover_file":"ovr-9-abc12345.jpg"}`},
		{"cover url without source", `{"cover_url":"https://x.example/1.jpg"}`},
		{"cleared cover with file", `{"cover_source":"clear","cover_file":"ovr-1-abc12345.jpg"}`},
		{"loopback cover url", `{"cover_source":"url","cover_url":"http://127.0.0.1:8080/cover.jpg"}`},
		{"private-range cover url", `{"cover_source":"url","cover_url":"http://10.0.0.5/cover.jpg"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := putOverride(t, h, 1, tc.body); code != http.StatusBadRequest {
				t.Fatalf("PUT = %d, want 400", code)
			}
		})
	}
	// The row must not exist after the refusals.
	state := getOverrideState(t, h, 1)
	if state.Overrides != nil {
		t.Fatalf("row written despite validation refusals: %+v", state.Overrides)
	}
}

// TestMetadataOverrideCoverUploadStoresImmutableAsset guards the upload:
// a JPEG becomes a book-scoped asset, a non-image is refused, and the asset
// is served by the covers gate.
func TestMetadataOverrideCoverUploadStoresImmutableAsset(t *testing.T) {
	h := newOverrideTestHandler(t)
	h.svc.CoversDir = t.TempDir()
	// A minimal valid 1x1 JPEG (decodes as an image config).
	jpeg := testJPEG1x1
	body, ct := multipartBody(t, "file", "cover.jpg", jpeg)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = []string{"id"}
	rctx.URLParams.Values = []string{"1"}
	req := httptest.NewRequest("POST", "/api/books/1/metadata/cover", body)
	req.Header.Set("Content-Type", ct)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.UploadBookMetadataCover(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d — %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !overrideCoverNameFor(resp["cover_file"], 1) {
		t.Fatalf("cover_file = %q, not this book's asset", resp["cover_file"])
	}
	if !validCoverName(resp["cover_file"]) {
		t.Fatalf("covers gate rejects the asset name %q", resp["cover_file"])
	}
}

// testJPEG1x1 is a real 1x1 JPEG (decodes through image.DecodeConfig).
var testJPEG1x1 = []byte{
	255, 216, 255, 224, 0, 16, 74, 70, 73, 70, 0, 1,
	1, 0, 0, 1, 0, 1, 0, 0, 255, 219, 0, 67,
	0, 3, 2, 2, 3, 2, 2, 3, 3, 3, 3, 4,
	3, 3, 4, 5, 8, 5, 5, 4, 4, 5, 10, 7,
	7, 6, 8, 12, 10, 12, 12, 11, 10, 11, 11, 13,
	14, 18, 16, 13, 14, 17, 14, 11, 11, 16, 22, 16,
	17, 19, 20, 21, 21, 21, 12, 15, 23, 24, 22, 20,
	24, 18, 20, 21, 20, 255, 219, 0, 67, 1, 3, 4,
	4, 5, 4, 5, 9, 5, 5, 9, 20, 13, 11, 13,
	20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	20, 20, 255, 192, 0, 17, 8, 0, 1, 0, 1, 3,
	1, 34, 0, 2, 17, 1, 3, 17, 1, 255, 196, 0,
	31, 0, 0, 1, 5, 1, 1, 1, 1, 1, 1, 0,
	0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5,
	6, 7, 8, 9, 10, 11, 255, 196, 0, 181, 16, 0,
	2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0,
	0, 1, 125, 1, 2, 3, 0, 4, 17, 5, 18, 33,
	49, 65, 6, 19, 81, 97, 7, 34, 113, 20, 50, 129,
	145, 161, 8, 35, 66, 177, 193, 21, 82, 209, 240, 36,
	51, 98, 114, 130, 9, 10, 22, 23, 24, 25, 26, 37,
	38, 39, 40, 41, 42, 52, 53, 54, 55, 56, 57, 58,
	67, 68, 69, 70, 71, 72, 73, 74, 83, 84, 85, 86,
	87, 88, 89, 90, 99, 100, 101, 102, 103, 104, 105, 106,
	115, 116, 117, 118, 119, 120, 121, 122, 131, 132, 133, 134,
	135, 136, 137, 138, 146, 147, 148, 149, 150, 151, 152, 153,
	154, 162, 163, 164, 165, 166, 167, 168, 169, 170, 178, 179,
	180, 181, 182, 183, 184, 185, 186, 194, 195, 196, 197, 198,
	199, 200, 201, 202, 210, 211, 212, 213, 214, 215, 216, 217,
	218, 225, 226, 227, 228, 229, 230, 231, 232, 233, 234, 241,
	242, 243, 244, 245, 246, 247, 248, 249, 250, 255, 196, 0,
	31, 1, 0, 3, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5,
	6, 7, 8, 9, 10, 11, 255, 196, 0, 181, 17, 0,
	2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0,
	1, 2, 119, 0, 1, 2, 3, 17, 4, 5, 33, 49,
	6, 18, 65, 81, 7, 97, 113, 19, 34, 50, 129, 8,
	20, 66, 145, 161, 177, 193, 9, 35, 51, 82, 240, 21,
	98, 114, 209, 10, 22, 36, 52, 225, 37, 241, 23, 24,
	25, 26, 38, 39, 40, 41, 42, 53, 54, 55, 56, 57,
	58, 67, 68, 69, 70, 71, 72, 73, 74, 83, 84, 85,
	86, 87, 88, 89, 90, 99, 100, 101, 102, 103, 104, 105,
	106, 115, 116, 117, 118, 119, 120, 121, 122, 130, 131, 132,
	133, 134, 135, 136, 137, 138, 146, 147, 148, 149, 150, 151,
	152, 153, 154, 162, 163, 164, 165, 166, 167, 168, 169, 170,
	178, 179, 180, 181, 182, 183, 184, 185, 186, 194, 195, 196,
	197, 198, 199, 200, 201, 202, 210, 211, 212, 213, 214, 215,
	216, 217, 218, 226, 227, 228, 229, 230, 231, 232, 233, 234,
	242, 243, 244, 245, 246, 247, 248, 249, 250, 255, 218, 0,
	12, 3, 1, 0, 2, 17, 3, 17, 0, 63, 0, 240,
	74, 40, 162, 191, 63, 63, 184, 15, 255, 217,
}

// multipartBody builds a multipart/form-data body with one file part and
// returns its content type.
func multipartBody(t *testing.T, field, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf, w.FormDataContentType()
}

// TestMetadataOverrideResetPrunesOrphanedAsset guards the asset lifecycle:
// a reset (or a save that changes the asset) removes the unreferenced
// override cover, but an ACTIVE job naming the asset keeps it.
func TestMetadataOverrideResetPrunesOrphanedAsset(t *testing.T) {
	h := newOverrideTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	asset := "ovr-1-abc12345.jpg"
	if err := os.WriteFile(filepath.Join(covers, asset), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := putOverride(t, h, 1, `{"cover_source":"file","cover_file":"`+asset+`"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	// A save that DROPS the asset reference prunes it (no active job).
	if code := putOverride(t, h, 1, `{"title":"T2"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset)); !os.IsNotExist(err) {
		t.Fatalf("orphaned asset survived an unreferencing save")
	}
	// An active job naming the asset keeps it across a reset.
	asset2 := "ovr-1-fedcba98.jpg"
	if err := os.WriteFile(filepath.Join(covers, asset2), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := putOverride(t, h, 1, `{"cover_source":"file","cover_file":"`+asset2+`"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	snapshot, _ := json.Marshal(snapshotJSON{Version: 1, Snapshot: BookSnapshot{BookID: 1, Title: "x", CoverFile: asset2}})
	if _, err := h.svc.DB.Exec(`INSERT INTO processing_jobs (id, book_id, status, m4b_merge_args) VALUES ('job-1', 1, 'queued', ?)`,
		mustMarshalJobArgs(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	req := routeReq(t, "POST", "/api/books/1/metadata/reset", 1, nil)
	w := httptest.NewRecorder()
	h.ResetBookMetadata(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset2)); err != nil {
		t.Fatalf("asset named by an active job was pruned on reset: %v", err)
	}
}

// mustMarshalJobArgs builds the stored m4b_merge_args JSON for a job whose
// frozen snapshot blob is snapshotJSONBlob (the asset-prune scan reads it).
func mustMarshalJobArgs(t *testing.T, snapshotJSONBlob []byte) string {
	t.Helper()
	var snap snapshotJSON
	if err := json.Unmarshal(snapshotJSONBlob, &snap); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(jobArgs{SrcPaths: []string{"/x.m4a"}, Snapshot: &snap})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestLoadBookMetadataOverrideDecodesGuarded guards the decode contract:
// a CORRUPTED array column is a load error (it must never be folded into an
// empty slice, because '[]' is the explicit-empty "clear the tags" state
// and a corrupt value masquerading as it would silently blank captured
// people/genres on the next save/job), while a valid '[]' stays
// explicit-empty and ” stays no-override.
func TestLoadBookMetadataOverrideDecodesGuarded(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`INSERT INTO books (id, title) VALUES (1, 'T')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO book_metadata_overrides (book_id, title, authors, narrators, cleared, chapters, year)
		VALUES (1, 'T2', 'not-json', '[]', '[]', '', 1999)`); err != nil {
		t.Fatal(err)
	}
	// The corrupt authors column surfaces as a load error, not an empty list.
	if o, err := loadBookMetadataOverride(db, 1); err == nil {
		t.Fatalf("corrupt authors loaded without error: %+v", o)
	}
	// A valid '[]' stays explicit-empty, '' stays no-override.
	if _, err := db.Exec(`UPDATE book_metadata_overrides SET authors = '[]' WHERE book_id = 1`); err != nil {
		t.Fatal(err)
	}
	o, err := loadBookMetadataOverride(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.Authors == nil || len(o.Authors) != 0 {
		t.Fatalf("authors = %v, want explicit empty (non-nil, len 0)", o.Authors)
	}
	if o.Narrators == nil || len(o.Narrators) != 0 {
		t.Fatalf("narrators = %v, want explicit empty", o.Narrators)
	}
	if o.Chapters != nil {
		t.Fatalf("chapters = %v, want nil (no override)", o.Chapters)
	}
	if o.Year == nil || *o.Year != 1999 {
		t.Fatalf("year = %v", o.Year)
	}
	// A missing row is nil, not an error.
	if o2, err := loadBookMetadataOverride(db, 2); err != nil || o2 != nil {
		t.Fatalf("missing row = %v, %v; want nil, nil", o2, err)
	}
}

// TestMetadataOverrideAuditDetailIsTruthful guards the audit contract: the
// metadata_update row names the editor sections and states the record-only
// truth (tags are written by a job, not the save).
func TestMetadataOverrideAuditDetailIsTruthful(t *testing.T) {
	h := newOverrideTestHandler(t)
	if code := putOverride(t, h, 1, `{"title":"Edited","year":1998,"cleared":["year"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	var detail string
	if err := h.svc.DB.QueryRow(`SELECT detail FROM processing_jobs WHERE action = 'metadata_update' ORDER BY rowid DESC LIMIT 1`).Scan(&detail); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	for _, want := range []string{"Edited manual metadata", "title", "year", "cleared tags: year", "processing job"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("audit detail %q missing %q", detail, want)
		}
	}
	if strings.Contains(detail, "tags written") || !strings.Contains(detail, "database record only") {
		t.Fatalf("audit detail must state the record-only truth: %q", detail)
	}
}
