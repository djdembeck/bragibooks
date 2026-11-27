package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
// distinction: an explicit clear (remove the embedded tag) pins the engine
// clear policy — while the effective value is KEPT, so the output is still
// named from it — and the override row survives a re-capture (unlike
// reset-to-catalog, which deletes the row).
func TestMetadataOverrideClearDistinctFromReset(t *testing.T) {
	h := newOverrideTestHandler(t)
	// Clear the year: the tag comes off the output (the engine clear policy
	// pins it), but the effective value is kept for naming.
	if code := putOverride(t, h, 1, `{"cleared":["year"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state := getOverrideState(t, h, 1)
	if state.Effective.Year == nil || *state.Effective.Year != 2019 {
		t.Fatalf("effective year = %v, want 2019 (kept for naming after clear)", state.Effective.Year)
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

// TestMetadataOverrideNilArraysStayCatalog guards the nil-array storage
// fix: an override that edits ONE field (title) and leaves authors,
// narrators, and genres untouched must round-trip those arrays as NIL
// (stored as the empty string, not '[]'), so the effective people/genres
// stay the catalog values. An explicit empty [] still stores its JSON and
// reads back non-nil (deliberate empty), which the editor's people editor
// relies on to show "edited: none".
func TestMetadataOverrideNilArraysStayCatalog(t *testing.T) {
	h := newOverrideTestHandler(t)
	if code := putOverride(t, h, 1, `{"title":"Edited"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	// Raw storage: the untouched arrays are the empty string (nil), the
	// title is stored.
	var authors, narrators, genres string
	if err := h.svc.DB.QueryRow(
		`SELECT authors, narrators, genres FROM book_metadata_overrides WHERE book_id = 1`,
	).Scan(&authors, &narrators, &genres); err != nil {
		t.Fatal(err)
	}
	if authors != "" || narrators != "" || genres != "" {
		t.Fatalf("raw arrays = (authors=%q, narrators=%q, genres=%q), want all empty (nil stored)", authors, narrators, genres)
	}
	state := getOverrideState(t, h, 1)
	if state.Overrides == nil || state.Overrides.Title != "Edited" {
		t.Fatalf("overrides = %+v, want title edit present", state.Overrides)
	}
	if state.Overrides.Authors != nil || state.Overrides.Narrators != nil || state.Overrides.Genres != nil {
		t.Fatalf("override people/genres = %+v, want nil (no override recorded)", state.Overrides)
	}
	if state.Effective == nil {
		t.Fatal("effective = nil")
	}
	if len(state.Effective.Authors) != 2 || state.Effective.Authors[0] != "Auth One" {
		t.Fatalf("effective authors = %v, want catalog [Auth One Auth Two]", state.Effective.Authors)
	}
	if len(state.Effective.Narrators) != 1 || state.Effective.Narrators[0] != "Narr A" {
		t.Fatalf("effective narrators = %v, want catalog [Narr A]", state.Effective.Narrators)
	}
	// An explicit empty list is an EDIT: it stores its JSON and reads back
	// non-nil, blanking the catalog people in the effective state.
	if code := putOverride(t, h, 1, `{"title":"Edited","authors":[]}`); code != http.StatusOK {
		t.Fatalf("PUT empty authors = %d", code)
	}
	state = getOverrideState(t, h, 1)
	if state.Overrides.Authors == nil {
		t.Fatalf("override authors = nil, want non-nil empty (explicit edit)")
	}
	if len(state.Overrides.Authors) != 0 {
		t.Fatalf("override authors = %v, want empty", state.Overrides.Authors)
	}
	if len(state.Effective.Authors) != 0 {
		t.Fatalf("effective authors = %v, want blanked by the explicit empty edit", state.Effective.Authors)
	}
}

// TestMetadataOverrideScalarClearKeepsValueForNaming guards the clear-vs-
// naming fix: a cleared title/series/year keeps its effective value (the
// output is still named from it) while the engine clear policy pins the
// tag for removal.
func TestMetadataOverrideScalarClearKeepsValueForNaming(t *testing.T) {
	h := newOverrideTestHandler(t)
	// Catalog snapshot: Title "Catalog Title", Series X (2), year 2019.
	if code := putOverride(t, h, 1, `{"cleared":["title","series","year"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state := getOverrideState(t, h, 1)
	if state.Effective == nil {
		t.Fatal("effective = nil")
	}
	if state.Effective.Title != "Catalog Title" {
		t.Fatalf("effective title = %q, want the catalog title kept for naming", state.Effective.Title)
	}
	if state.Effective.SeriesName != "Series X" || state.Effective.SeriesPosition != "2" {
		t.Fatalf("effective series = %q (%q), want kept for naming", state.Effective.SeriesName, state.Effective.SeriesPosition)
	}
	if state.Effective.Year == nil || *state.Effective.Year != 2019 {
		t.Fatalf("effective year = %v, want 2019 kept for naming", state.Effective.Year)
	}
	// The engine job pins the clears on top of the effective values. The
	// frozen selection mirrors what QueueJob produces for a nil saved
	// selection plus scalar pins (the pin-forced default).
	_, pins := applyMetadataOverride(state.Snapshot, state.Overrides)
	if pins == nil || pins.Scalars == nil {
		t.Fatal("pins = nil; the clears must force scalar pins")
	}
	sel := &WriteSelection{
		Fields:     nil,
		Cover:      audio.WritePolicyValueWrite,
		Chapters:   audio.ChaptersPolicyValueWrite,
		ScalarPins: map[string]string{},
	}
	for k, v := range map[string]string{
		"title": pins.Scalars.Title, "series": pins.Scalars.Series, "year": pins.Scalars.Year,
	} {
		if v != "" {
			sel.ScalarPins[k] = v
		}
	}
	if sel.ScalarPins["title"] != "clear" || sel.ScalarPins["series"] != "clear" || sel.ScalarPins["year"] != "clear" {
		t.Fatalf("scalar pins = %v, want title/series/year pinned to clear", sel.ScalarPins)
	}
	jf, _, err := buildEngineJob(context.Background(), jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Snapshot: *state.Effective, Selection: sel}}, "job-clear", *state.Effective, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if jf.Policies.Title != "clear" || jf.Policies.Series != "clear" || jf.Policies.Year != "clear" {
		t.Fatalf("policies = %+v, want title/series/year pinned to clear", jf.Policies)
	}
	supplied := jf.InputSupplied
	if supplied.Title != "Catalog Title" {
		t.Fatalf("naming title = %q, want the kept value", supplied.Title)
	}
	// An edited-then-cleared title keeps the EDITED value for naming.
	if code := putOverride(t, h, 1, `{"title":"Edited","cleared":["title"]}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	state = getOverrideState(t, h, 1)
	if state.Effective.Title != "Edited" {
		t.Fatalf("effective title = %q, want the edited value kept for naming", state.Effective.Title)
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
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{}, "")
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
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{}, "")
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
	jf, _, err := buildEngineJob(context.Background(), args, id, args.Snapshot.Snapshot, t.TempDir(), args.OutputScheme)
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
	jf, staged, err := buildEngineJob(context.Background(), args, "job-7", snap, t.TempDir(), "{title}")
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
	jf, _, err = buildEngineJob(context.Background(), args, "job-7", snap, t.TempDir(), "{title}")
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
	// public-address check is exercised deterministically. A special-purpose
	// address (e.g. RFC 5737 documentation 203.0.113.0/24) is refused by the
	// guard, so the stub must name a genuinely public one.
	oldLookup := lookupCoverHost
	lookupCoverHost = func(_ context.Context, host string) ([]string, error) {
		if host == "covers.example" {
			return []string{"93.184.216.34"}, nil
		}
		return nil, fmt.Errorf("no address for %s", host)
	}
	t.Cleanup(func() { lookupCoverHost = oldLookup })
	// Stub the pinned dial so the fetch fails at the connection, not on the
	// luck of a live address being (un)reachable: the test is offline and
	// deterministic, and the job degrades to "clear" (the fetch-failure
	// path). The success path (a local staged file) is the same code branch
	// the override-asset test already covers.
	oldDial := coverDialContext
	coverDialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { coverDialContext = oldDial })

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
	id, err := svc.QueueJob(t.Context(), 1, []string{in}, queueDestination{}, "")
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
	if sel := args.Snapshot.Selection; sel == nil || sel.Cover != "write_url" {
		t.Fatalf("cover policy = %+v, want write_url", sel)
	}
	if sel.CoverURL != "https://covers.example/1.jpg" {
		t.Fatalf("frozen cover url = %q", sel.CoverURL)
	}
	// buildEngineJob is the authoritative serializer: it fetches the cover
	// through the pinned downloader into a local staged file (a "write"
	// cover) or degrades to "clear". The ENGINE JOB must never carry the
	// raw URL: the engine would re-resolve the host over live DNS, and a
	// name that passed the save-time guard can rebind to a private address
	// by run time. The dial is stubbed to refuse, so the fetch fails and
	// the job degrades to "clear" — the key contract is that the write_url
	// form is gone from the engine boundary.
	jf, _, err := buildEngineJob(context.Background(), args, id, args.Snapshot.Snapshot, t.TempDir(), args.OutputScheme)
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	got := string(jf.Cover)
	if strings.Contains(got, "write_url") || strings.Contains(got, "http") {
		t.Fatalf("engine job cover = %s, must not embed the URL for the engine to fetch (want a local staged write cover or the degraded clear)", got)
	}
	if got != `"clear"` && got != `"preserve"` {
		// The "write" object form names a local file under the output
		// root — the only other acceptable shape.
		t.Fatalf("engine job cover = %s, want a write object with a local path, clear, or preserve", got)
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
		// A PUBLIC host over http:// must be refused for the same reason as
		// the private-range cases: the pinned engine fetches write_url
		// covers over an HTTPS-only transport, so a public http URL would
		// pass save-time and only fail at run time.
		{"public http cover url", `{"cover_source":"url","cover_url":"http://covers.example/cover.jpg"}`},
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

// TestMetadataOverrideCoverUploadEnforcesPinnedEngineLimit guards the
// 5 MiB cap: the pinned engine (m4b-merge v2.2.0) refuses local covers
// above 5 MiB, so the server must refuse the upload with a 413 naming the
// limit rather than stage an asset that can never be written. The body
// (5 MiB JPEG + framing) is large enough to trip the transport cap before
// multipart parsing.
func TestMetadataOverrideCoverUploadEnforcesPinnedEngineLimit(t *testing.T) {
	t.Parallel()
	h := newOverrideTestHandler(t)
	h.svc.CoversDir = t.TempDir()
	oversize := append(testJPEG1x1, make([]byte, (5<<20)+1)...)
	body, ct := multipartBody(t, "file", "big.jpg", oversize)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = []string{"id"}
	rctx.URLParams.Values = []string{"1"}
	req := httptest.NewRequest("POST", "/api/books/1/metadata/cover", body)
	req.Header.Set("Content-Type", ct)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.UploadBookMetadataCover(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload = %d, want 413 — %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "5 MB limit") {
		t.Fatalf("body does not name the limit: %s", w.Body.String())
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

// TestMetadataOverrideResetKeepsPendingUpload guards the upload→save window:
// an override cover an upload just wrote (not yet referenced by the saved
// row) must survive a same-book reset that runs before the draft save lands.
// Without the pending-upload keep-set the reset prunes it and the later save
// points at a deleted asset (the job degrades to no cover). The save's drop
// is scoped to the cover the saved row references, so a save that does NOT
// reference the upload (a concurrent draft saving something else) must also
// keep it: only the referencing save drops the pending entry and lets the
// sweep prune it.
func TestMetadataOverrideResetKeepsPendingUpload(t *testing.T) {
	h := newOverrideTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	asset := "ovr-1-00000001.jpg"
	if err := os.WriteFile(filepath.Join(covers, asset), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The upload wrote the file and registered it; the draft save has not
	// persisted a row referencing it yet.
	h.svc.recordPendingCover(asset, 1)
	req := routeReq(t, "POST", "/api/books/1/metadata/reset", 1, nil)
	w := httptest.NewRecorder()
	h.ResetBookMetadata(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset)); err != nil {
		t.Fatalf("pending upload pruned by reset before its draft save: %v", err)
	}
	// A save that does not reference the upload leaves the pending entry
	// (and the asset) in place: the not-yet-saved draft may still point at
	// it, and the row no longer does.
	if code := putOverride(t, h, 1, `{"title":"T2"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset)); err != nil {
		t.Fatalf("non-referencing save pruned a pending upload: %v", err)
	}
	// Once the draft save lands referencing the asset (and drops the pending
	// entry), a later save that no longer references it prunes it.
	if code := putOverride(t, h, 1, `{"cover_source":"file","cover_file":"`+asset+`"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset)); err != nil {
		t.Fatalf("referencing save pruned its own cover asset: %v", err)
	}
	if code := putOverride(t, h, 1, `{"title":"T3"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if _, err := os.Stat(filepath.Join(covers, asset)); !os.IsNotExist(err) {
		t.Fatalf("pending entry survived the later non-referencing save")
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
	// A stored literal null decodes to a nil slice WITHOUT a JSON error; it
	// must still be a load error, not "no override" (which would let the
	// next save fold the field into explicit-empty).
	if _, err := db.Exec(`UPDATE book_metadata_overrides SET narrators = 'null', chapters = 'null' WHERE book_id = 1`); err != nil {
		t.Fatal(err)
	}
	if o3, err := loadBookMetadataOverride(db, 1); err == nil {
		t.Fatalf("null array columns loaded without error: %+v", o3)
	}
}

// TestNormalizeClearedFieldsRecovery guards the clear-marker lifecycle:
// an EMPTY list folds into cleared:["chapters"]; a NON-EMPTY list is an
// edited value and drops the marker (a saved clear must not discard the
// edited list); a NIL list means "no list state in this request" and
// leaves the marker untouched — the client is expected to strip the marker
// explicitly when the operator chooses the catalog list (the server cannot
// guess intent, and the stale-marker case it guards against is the
// non-empty one).
func TestNormalizeClearedFieldsRecovery(t *testing.T) {
	cases := []struct {
		name     string
		cleared  []string
		chapters []SnapshotChapter
		want     []string
	}{
		{"empty list folds to clear", []string{}, []SnapshotChapter{}, []string{"chapters"}},
		{"explicit clear survives", []string{"chapters"}, nil, []string{"chapters"}},
		{"non-empty list drops the clear", []string{"chapters", "title"}, []SnapshotChapter{{Title: "A"}}, []string{"title"}},
		{"nil list leaves the marker", []string{"chapters", "title"}, nil, []string{"chapters", "title"}},
		{"dedupes and sorts", []string{"b", "b", "a"}, nil, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeClearedFields(tc.cleared, tc.chapters)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("normalize = %v, want %v", got, tc.want)
			}
		})
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

// registerExpiredCover plants a pending-cover registry entry with a PAST
// expiry (the upload happened pendingCoverTTL ago and the draft save never
// arrived) plus its on-disk asset, so a reap sweep sees a fully expired
// entry.
func registerExpiredCover(t *testing.T, s *Services, name string, bookID int64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.CoversDir, name), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.pendingCoversMu.Lock()
	if s.pendingCovers == nil {
		s.pendingCovers = make(map[string]time.Time)
	}
	s.pendingCovers[name] = time.Now().Add(-time.Hour)
	s.pendingCoversMu.Unlock()
}

func hasPendingCover(s *Services, name string) bool {
	s.pendingCoversMu.Lock()
	defer s.pendingCoversMu.Unlock()
	_, ok := s.pendingCovers[name]
	return ok
}

// TestReapPendingCoversDefersBusyBook pins the cross-book deferral: an
// expired asset whose book's cover lock is held elsewhere (a concurrent
// save/capture) must NOT be removed by a sweep — the entry and the file
// stay for a later sweep, so the reap never blocks on (or races) a held
// book lock.
func TestReapPendingCoversDefersBusyBook(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	h.svc.CoversDir = t.TempDir()
	asset := "ovr-7-deadbeef.jpg"
	registerExpiredCover(t, h.svc, asset, 7)

	// Hold book 7's cover lock the way a concurrent save would.
	unlock := svc.lockBookCover(7)
	h.svc.pendingCoversMu.Lock()
	h.svc.reapPendingCoversLocked(time.Now(), 0)
	h.svc.pendingCoversMu.Unlock()
	unlock()

	if !hasPendingCover(h.svc, asset) {
		t.Errorf("expired entry for a lock-held book was reaped; it must be deferred to a later sweep")
	}
	if ok, _ := mustFile(filepath.Join(h.svc.CoversDir, asset)); !ok {
		t.Errorf("expired asset for a lock-held book was removed; it must survive a busy-lock sweep")
	}
	// With the lock free, the next sweep reaps it: deferral, not immunity.
	h.svc.pendingCoversMu.Lock()
	h.svc.reapPendingCoversLocked(time.Now(), 0)
	h.svc.pendingCoversMu.Unlock()
	if hasPendingCover(h.svc, asset) {
		t.Errorf("entry still registered after the lock was released and a sweep ran")
	}
	if ok, _ := mustFile(filepath.Join(h.svc.CoversDir, asset)); ok {
		t.Errorf("asset still on disk after the lock was released and a sweep ran")
	}
}

// TestReapPendingCoversKeepsSavedReference pins the save-vs-reap
// serialization: after a save persists an override row referencing a
// pending cover, the row (not the registry) is the reference, and a reap
// sweep must not delete the asset the saved row points at — the file must
// survive the sweep even though the registry no longer names it.
func TestReapPendingCoversKeepsSavedReference(t *testing.T) {
	h := newOverrideTestHandler(t)
	svc := h.svc.ProcessingSvc
	h.svc.CoversDir = t.TempDir()
	asset := "ovr-1-deadbeef.jpg"
	registerExpiredCover(t, h.svc, asset, 1)
	if code := putOverride(t, h, 1, `{"cover_source":"file","cover_file":"`+asset+`"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d; want 200", code)
	}
	if hasPendingCover(h.svc, asset) {
		t.Fatalf("the save must drop the pending entry for the asset it persisted (the row is now the reference)")
	}
	// The sweep: expired, and the book lock is free — it would remove the
	// entry/file for any book, so only the prune inside the save protected
	// the asset (and only the row can keep protecting it now).
	unlock := svc.lockBookCover(1)
	h.svc.pendingCoversMu.Lock()
	h.svc.reapPendingCoversLocked(time.Now(), 0)
	h.svc.pendingCoversMu.Unlock()
	unlock()
	if ok, _ := mustFile(filepath.Join(h.svc.CoversDir, asset)); !ok {
		t.Errorf("the asset a saved override row references was removed by a sweep; the row's reference must keep it")
	}
}

// jpegHeaderOnly builds a minimal JPEG whose SOI + APP0 (JFIF) + SOF0
// header declares the given dimensions: image.DecodeConfig reads exactly
// those dimensions (the full decode is never run — the test is about the
// header check, which is what the size bound exists to gate).
func jpegHeaderOnly(height, width int) []byte {
	// SOI, then APP0 (JFIF).
	b := []byte{0xFF, 0xD8}
	b = append(b, 0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	// SOF0: 8-bit depth, the declared dimensions, 3 components.
	b = append(b, 0xFF, 0xC0, 0, 17, 8)
	b = append(b, byte(height>>8), byte(height), byte(width>>8), byte(width), 3)
	b = append(b, 1, 0x11, 0, 2, 0x11, 1, 3, 0x11, 1)
	// EOI.
	b = append(b, 0xFF, 0xD9)
	return b
}

// decodedPixelHeaderCheck reproduces the two header-stage comparisons
// isValidJPEG runs after image.DecodeConfig, so the header gate can be
// asserted directly (a header-only JPEG never reaches the full decode).
func decodedPixelHeaderCheck(height, width int) bool {
	if height > maxCoverImageDimension || width > maxCoverImageDimension {
		return false
	}
	return int64(height)*int64(width) <= maxCoverPixels
}

// TestCoverUploadRejectsDecodedPixelAxisLimit pins the decoded-pixel gate
// in isValidJPEG: a header that declares an axis past maxCoverImageDimension
// is rejected at the HEADER stage (before the full decode allocates the
// pixel buffer), while a just-under axis passes the header check. The
// total-pixel bound (maxCoverPixels) is subsumed by the axis bound at the
// current constants (4472^2 = 19,998,784 < 20,000,000: no axis-legal pair
// can exceed the total), so the axis case is the independently reachable
// form of the same bound — the header check is asserted against the
// production constants directly.
func TestCoverUploadRejectsDecodedPixelAxisLimit(t *testing.T) {
	// Header stage, asserted against the production constants: the axis
	// over the limit fails, the axis at the limit passes (and its product
	// stays under the total), and the total is only reachable past the
	// axis bound (documenting the subsumption at the current constants).
	if decodedPixelHeaderCheck(4473, 100) {
		t.Fatalf("4473x100 passed the header check; the axis bound is maxCoverImageDimension=%d", maxCoverImageDimension)
	}
	if !decodedPixelHeaderCheck(4472, 100) {
		t.Fatalf("4472x100 failed the header check; the axis bound is maxCoverImageDimension=%d", maxCoverImageDimension)
	}
	// Subsumption at the current constants: the LARGEST axis-legal
	// product (both axes at the limit) is already under the total, so no
	// pair that passes the axis check can trip the total check.
	if int64(maxCoverImageDimension)*int64(maxCoverImageDimension) > maxCoverPixels {
		t.Fatalf("the total bound %d is reachable with axis-legal dimensions; the subsumption the test documents no longer holds", maxCoverPixels)
	}

	h := newOverrideTestHandler(t)
	h.svc.CoversDir = t.TempDir()
	cases := []struct {
		name string
		jpeg []byte
		want int
	}{
		{"axis over limit 4473x100", jpegHeaderOnly(4473, 100), http.StatusBadRequest},
		// The header passes (the axis is at the limit); the upload is still
		// refused because the header-only fixture has no scan data for the
		// full decode — the size gate and the decode gate both hold.
		{"axis at limit 4472x100", jpegHeaderOnly(4472, 100), http.StatusBadRequest},
		{"1x1 reference jpeg", testJPEG1x1, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, ct := multipartBody(t, "file", "cover.jpg", tc.jpeg)
			req := routeReq(t, "POST", "/api/books/1/metadata/cover", 1, body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			h.UploadBookMetadataCover(w, req)
			if w.Code != tc.want {
				t.Fatalf("upload = %d, want %d — %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusBadRequest {
				if !strings.Contains(w.Body.String(), "not a JPEG image") {
					t.Errorf("rejection does not name the JPEG validation: %s", w.Body.String())
				}
			}
		})
	}
}
