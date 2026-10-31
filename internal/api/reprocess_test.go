package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
)

// reprocessPreviewWire mirrors the GET /api/books/{id}/reprocess JSON shape
// for assertions (nested pointer fields distinguish omitted from present).
type reprocessPreviewWire struct {
	Available    bool   `json:"available"`
	Reason       string `json:"reason"`
	CompletedDir string `json:"completed_dir"`
	LastRun      *struct {
		JobID        string `json:"job_id"`
		CreatedAt    string `json:"created_at"`
		OutputDir    string `json:"output_dir"`
		OutputScheme string `json:"output_scheme"`
	} `json:"last_run"`
	Inputs *struct {
		Total            int      `json:"total"`
		Found            int      `json:"found"`
		FoundInCompleted int      `json:"found_in_completed"`
		FoundInOriginal  int      `json:"found_in_original"`
		Missing          []string `json:"missing"`
	} `json:"inputs"`
}

// newReprocessHandler wires a jobs handler with an explicit config manager
// (newTestJobsHandler leaves svc.Config nil) carrying the given directories
// and path format.
func newReprocessHandler(t *testing.T, outputDir, completedDir, pathFormat, m4bBinary string) *Handler {
	t.Helper()
	h, _ := newTestJobsHandler(t)
	cm := config.NewConfigManager()
	live := cm.Config()
	live.Directories.OutputDir = outputDir
	live.Directories.CompletedDir = completedDir
	live.Processing.PathFormat = pathFormat
	live.M4bMerge.Binary = m4bBinary
	cm.Store(live)
	h.svc.Config = cm
	return h
}

// seedReprocessBook inserts a book at a fixed ID with the given status and
// recorded src path.
func seedReprocessBook(t *testing.T, h *Handler, bookID int64, status, srcPath string) {
	t.Helper()
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, src_path) VALUES (?, ?, ?, ?)",
		bookID, "Reprocess Book", status, srcPath,
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
}

// seedMergeRow inserts an action='merge' job row with the given stored args
// JSON and created_at, in caller-chosen order (rowid order = insert order).
func seedMergeRow(t *testing.T, h *Handler, jobID string, bookID int64, argsJSON, createdAt string) {
	t.Helper()
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at) VALUES (?, ?, ?, 'done', 'merge', ?)",
		jobID, bookID, argsJSON, createdAt,
	); err != nil {
		t.Fatalf("seed merge row: %v", err)
	}
}

// postReprocess drives POST /api/process/reprocess with a JSON body.
func postReprocess(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	w := httptest.NewRecorder()
	h.StartReprocess(w, httptest.NewRequest("POST", "/api/process/reprocess", bytes.NewReader(b)))
	return w
}

// getReprocess drives GET /api/books/{id}/reprocess with the chi route param
// injected by hand (handlers are invoked directly, not through the router).
func getReprocess(t *testing.T, h *Handler, bookID int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/books/%d/reprocess", bookID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = []string{"id"}
	rctx.URLParams.Values = []string{strconv.FormatInt(bookID, 10)}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.GetBookReprocess(w, req)
	return w
}

// newestJobArgs reads back the book's newest merge row's stored args as the
// parsed jobArgs (the reprocess row, which is always inserted last in these
// tests).
func newestJobArgs(t *testing.T, db *sql.DB, bookID int64) jobArgs {
	t.Helper()
	var argsJSON string
	if err := db.QueryRow(
		"SELECT m4b_merge_args FROM processing_jobs WHERE book_id = ? AND action = 'merge' ORDER BY rowid DESC LIMIT 1",
		bookID,
	).Scan(&argsJSON); err != nil {
		t.Fatalf("read new job args: %v", err)
	}
	var args jobArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		t.Fatalf("unmarshal new job args %q: %v", argsJSON, err)
	}
	if len(args.SrcPaths) == 0 {
		t.Fatalf("new job args have no src_paths: %q", argsJSON)
	}
	return args
}

// clearQueuedJobs drops the book's queued rows (no worker runs them in these
// tests, so they would trip the active-job guard on a later request).
func clearQueuedJobs(t *testing.T, db *sql.DB, bookID int64) {
	t.Helper()
	if _, err := db.Exec("DELETE FROM processing_jobs WHERE book_id = ? AND status = 'queued'", bookID); err != nil {
		t.Fatalf("clear queued jobs: %v", err)
	}
}

func jobCountForBook(t *testing.T, db *sql.DB, bookID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM processing_jobs WHERE book_id = ?", bookID).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// TestReprocessResolvesCompletedFirst pins the candidate order: a basename
// present in BOTH the completed dir and the recorded original path resolves
// to the completed-dir file; every input found => 200 + one queued job whose
// stored args carry the completed-dir paths and the reprocess provenance
// detail, with the book status untouched (the worker does that transition).
func TestReprocessResolvesCompletedFirst(t *testing.T) {
	remain, completed := t.TempDir(), t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")

	originals := []string{filepath.Join(remain, "a.m4b"), filepath.Join(remain, "b.m4b")}
	for _, p := range originals {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write original: %v", err)
		}
	}
	for _, base := range []string{"a.m4b", "b.m4b"} {
		if err := os.WriteFile(filepath.Join(completed, base), []byte("x"), 0o644); err != nil {
			t.Fatalf("write completed: %v", err)
		}
	}
	seedReprocessBook(t, h, 1, "done", remain)
	seedMergeRow(t, h, "seeded-1", 1, `{"src_paths":["`+originals[0]+`","`+originals[1]+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	want := []string{filepath.Join(completed, "a.m4b"), filepath.Join(completed, "b.m4b")}
	if got := newestJobArgs(t, h.svc.DB, 1).SrcPaths; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("resolved src_paths = %v; want %v", got, want)
	}
	var detail string
	if err := h.svc.DB.QueryRow(
		"SELECT detail FROM processing_jobs WHERE book_id = 1 ORDER BY rowid DESC LIMIT 1",
	).Scan(&detail); err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if detail != "reprocess: 2 of 2 inputs from completed directory" {
		t.Errorf("detail = %q; want %q", detail, "reprocess: 2 of 2 inputs from completed directory")
	}
	if jobCountForBook(t, h.svc.DB, 1) != 2 {
		t.Errorf("job count = %d; want 2 (seeded + reprocess)", jobCountForBook(t, h.svc.DB, 1))
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status = %q at queue time; want done", got)
	}
}

// TestReprocessMixedLocations covers one input in the completed dir and one
// only at its recorded original path: order is preserved and the detail
// counts the completed share.
func TestReprocessMixedLocations(t *testing.T) {
	remain, completed := t.TempDir(), t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")
	origA, origB := filepath.Join(remain, "a.m4b"), filepath.Join(remain, "b.m4b")
	for _, p := range []string{origA, origB, filepath.Join(completed, "a.m4b")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	seedReprocessBook(t, h, 1, "done", remain)
	seedMergeRow(t, h, "seeded-mix", 1, `{"src_paths":["`+origA+`","`+origB+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	got := newestJobArgs(t, h.svc.DB, 1).SrcPaths
	want := []string{filepath.Join(completed, "a.m4b"), origB}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("resolved src_paths = %v; want %v", got, want)
	}
	var detail string
	h.svc.DB.QueryRow("SELECT detail FROM processing_jobs WHERE book_id = 1 ORDER BY rowid DESC LIMIT 1").Scan(&detail)
	if detail != "reprocess: 1 of 2 inputs from completed directory" {
		t.Errorf("detail = %q; want %q", detail, "reprocess: 1 of 2 inputs from completed directory")
	}
}

// TestReprocessMissingInputsRefused: any input in neither location 400s
// before a job exists, names the basename and both looked-in locations, and
// leaves the book untouched.
func TestReprocessMissingInputsRefused(t *testing.T) {
	remain, completed := t.TempDir(), t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")
	if err := os.WriteFile(filepath.Join(completed, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	// b.m4b exists in NEITHER location; its recorded path points into remain.
	seedReprocessBook(t, h, 1, "done", remain)
	seedMergeRow(t, h, "seeded-miss", 1, `{"src_paths":["`+filepath.Join(remain, "a.m4b")+`","`+filepath.Join(remain, "b.m4b")+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"b.m4b", completed, "recorded source paths"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q does not name %q", body, want)
		}
	}
	if n := jobCountForBook(t, h.svc.DB, 1); n != 1 {
		t.Errorf("job count = %d; want 1 (seeded only)", n)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status = %q; want done", got)
	}

	// Empty completed dir shortens the sentence to the single location.
	cm := h.svc.Config
	live := cm.Config()
	live.Directories.CompletedDir = ""
	cm.Store(live)
	w = postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "in the recorded source paths") {
		t.Fatalf("no-completed-dir body = %q; want the recorded-source-paths-only sentence", w.Body.String())
	}
}

// TestReprocessNoRun covers the two history-less refusals: a book with no
// merge rows at all (no-run sentence) and one whose newest merge row
// recorded no inputs (no-inputs sentence); the preview reports available
// false with the same operator sentence in either case.
func TestReprocessNoRun(t *testing.T) {
	h := newReprocessHandler(t, t.TempDir(), t.TempDir(), "{author}/{title}", "")
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedReprocessBook(t, h, 2, "done", t.TempDir())
	seedReprocessBook(t, h, 3, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-empty", 2, `{"src_paths":[]}`, "2026-07-03 10:00:00")
	// Legacy rows predate the m4b_merge_args column and carry SQL NULL.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at) VALUES (?, ?, NULL, 'done', 'merge', '2026-07-03 10:00:00')",
		"seeded-null", 3,
	); err != nil {
		t.Fatalf("seed null-args row: %v", err)
	}

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "No recorded processing run for this book") {
		t.Fatalf("no-run: status %d body %q; want 400 + no-run sentence", w.Code, w.Body.String())
	}
	w = postReprocess(t, h, StartReprocessRequest{BookID: 2})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "No original input files were recorded") {
		t.Fatalf("no-inputs: status %d body %q; want 400 + no-inputs sentence", w.Code, w.Body.String())
	}
	w = postReprocess(t, h, StartReprocessRequest{BookID: 3})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "No original input files were recorded") {
		t.Fatalf("null-args: status %d body %q; want the no-inputs sentence, not a 500", w.Code, w.Body.String())
	}

	rec := getReprocess(t, h, 1)
	var prev reprocessPreviewWire
	if err := json.NewDecoder(rec.Body).Decode(&prev); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if prev.Available || prev.Reason == "" || prev.LastRun != nil || prev.Inputs != nil {
		t.Fatalf("preview = %+v; want available=false with reason and no last_run/inputs", prev)
	}

	// The SQL-NULL args row (book 3) is refusal state, not a server error:
	// the same no-inputs sentence as book 2, last_run/inputs omitted.
	rec = getReprocess(t, h, 3)
	if rec.Code != http.StatusOK {
		t.Fatalf("null-args preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&prev); err != nil {
		t.Fatalf("decode null-args preview: %v", err)
	}
	if prev.Available || prev.Reason == "" {
		t.Fatalf("null-args preview = %+v; want available=false with a reason", prev)
	}
	if !strings.Contains(prev.Reason, "No original input files were recorded") {
		t.Errorf("null-args preview reason = %q; want the no-inputs sentence", prev.Reason)
	}
	if prev.LastRun != nil || prev.Inputs != nil {
		t.Errorf("null-args preview = %+v; want last_run/inputs omitted", prev)
	}

	// Unknown book is a 404 on the preview.
	if rec := getReprocess(t, h, 99); rec.Code != http.StatusNotFound {
		t.Errorf("preview unknown book = %d; want 404", rec.Code)
	}
}

// TestReprocessActiveJob409: a queued job for the book blocks the reprocess
// POST with the same sentence as StartProcessing.
func TestReprocessActiveJob409(t *testing.T) {
	h := newReprocessHandler(t, t.TempDir(), t.TempDir(), "{author}/{title}", "")
	completed := t.TempDir()
	if err := os.WriteFile(filepath.Join(completed, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-active", 1, `{"src_paths":["`+filepath.Join(completed, "a.m4b")+`"]}`, "2026-07-03 10:00:00")
	// Seeded AFTER NewProcessingService's startup sweep, so it is read as
	// queued by the active-job guard.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action) VALUES ('active-1', 1, 'queued', 'merge')",
	); err != nil {
		t.Fatalf("seed active job: %v", err)
	}

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "book 1 already has an active processing job") {
		t.Fatalf("status %d body %q; want 409 + active-job sentence", w.Code, w.Body.String())
	}
	if n := jobCountForBook(t, h.svc.DB, 1); n != 2 {
		t.Errorf("job count = %d; want 2 (seeded merge + active)", n)
	}
}

// TestReprocessDirectoryShadowsInput: a DIRECTORY in the completed dir named
// like a recorded input (or at its recorded path) is not an input — the real
// file at the other location must win, or the input counts as missing.
func TestReprocessDirectoryShadowsInput(t *testing.T) {
	remain, completed := t.TempDir(), t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")
	// a.m4b: a directory in completed must NOT shadow the real file in remain.
	if err := os.Mkdir(filepath.Join(completed, "a.m4b"), 0o755); err != nil {
		t.Fatalf("mkdir completed a.m4b: %v", err)
	}
	origA := filepath.Join(remain, "a.m4b")
	if err := os.WriteFile(origA, []byte("x"), 0o644); err != nil {
		t.Fatalf("write a.m4b: %v", err)
	}
	// b.m4b: a directory at the recorded path, nowhere else — not an input.
	if err := os.Mkdir(filepath.Join(remain, "b.m4b"), 0o755); err != nil {
		t.Fatalf("mkdir recorded b.m4b: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", remain)
	seedMergeRow(t, h, "seeded-dir", 1, `{"src_paths":["`+origA+`","`+filepath.Join(remain, "b.m4b")+`"]}`, "2026-07-03 10:00:00")

	rec := getReprocess(t, h, 1)
	var prev reprocessPreviewWire
	if err := json.NewDecoder(rec.Body).Decode(&prev); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !prev.Available || prev.LastRun == nil || prev.Inputs == nil {
		t.Fatalf("preview = %+v; want available with a last run", prev)
	}
	if got := prev.Inputs.FoundInCompleted; got != 0 {
		t.Errorf("found_in_completed = %d; want 0 (a directory is not an input)", got)
	}
	if got := prev.Inputs.FoundInOriginal; got != 1 {
		t.Errorf("found_in_original = %d; want 1 (the real a.m4b file)", got)
	}
	if len(prev.Inputs.Missing) != 1 || prev.Inputs.Missing[0] != "b.m4b" {
		t.Errorf("missing = %v; want [b.m4b]", prev.Inputs.Missing)
	}

	// The same rule blocks the run: b.m4b is a directory, so it is missing.
	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "b.m4b") {
		t.Fatalf("status %d body %q; want 400 naming b.m4b", w.Code, w.Body.String())
	}
	if n := jobCountForBook(t, h.svc.DB, 1); n != 1 {
		t.Errorf("job count = %d; want 1 (seeded only)", n)
	}
}

// TestReprocessProfileSnapshot: a profile resolves exactly like the queue
// page (directory only inherits the global format) into the stored job
// snapshot, and a profile combined with an explicit override is refused.
func TestReprocessProfileSnapshot(t *testing.T) {
	outputRoot := t.TempDir()
	completed := t.TempDir()
	h := newReprocessHandler(t, outputRoot, completed, "{author}/{title}", "")
	profileRoot := filepath.Join(t.TempDir(), "profile-out")
	os.MkdirAll(profileRoot, 0o755)
	completedIn := filepath.Join(completed, "a.m4b")
	if err := os.WriteFile(completedIn, []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}

	var profileID int64
	if _, err := h.svc.DB.Exec(
		"INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES ('reprocess-dir', ?, '')", profileRoot,
	); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := h.svc.DB.QueryRow("SELECT id FROM output_profiles WHERE name = 'reprocess-dir'").Scan(&profileID); err != nil {
		t.Fatalf("read profile id: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-prof", 1, `{"src_paths":["`+completedIn+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1, OutputProfileID: &profileID})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	args := newestJobArgs(t, h.svc.DB, 1)
	absProfile, _ := filepath.Abs(profileRoot)
	if args.OutputDir != absProfile {
		t.Errorf("stored output_dir = %q; want %q", args.OutputDir, absProfile)
	}
	if args.OutputScheme != "{author}/{title}" {
		t.Errorf("stored output_scheme = %q; want the inherited global {author}/{title}", args.OutputScheme)
	}
	var resp StartProcessingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].BookID != 1 {
		t.Fatalf("response jobs = %+v; want exactly one entry for book 1", resp.Jobs)
	}
	if resp.OutputDir != absProfile || resp.OutputScheme != "{author}/{title}" {
		t.Errorf("response destination = %q / %q; want the resolved pair", resp.OutputDir, resp.OutputScheme)
	}

	// The first post queued a job (no worker runs it): the active-job guard
	// would 409 the next request, so clear queued rows between posts.
	clearQueuedJobs(t, h.svc.DB, 1)

	// Profile id PLUS an explicit override is one source of truth violation.
	w = postReprocess(t, h, StartReprocessRequest{BookID: 1, OutputProfileID: &profileID, OutputDir: outputRoot})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not both") {
		t.Fatalf("both-services: status %d body %q; want 400 + both-services sentence", w.Code, w.Body.String())
	}
}

// TestReprocessOverrideValidation: explicit overrides are validated
// identically to profile overrides and stored verbatim (dir absolutized),
// and a fully blank request snapshots nothing (global-settings run).
func TestReprocessOverrideValidation(t *testing.T) {
	h := newReprocessHandler(t, t.TempDir(), t.TempDir(), "{author}/{title}", "")
	completed := h.svc.Config.Config().Directories.CompletedDir
	altDir := filepath.Join(t.TempDir(), "alt-out")
	os.MkdirAll(altDir, 0o755)
	completedIn := filepath.Join(completed, "a.m4b")
	if err := os.WriteFile(completedIn, []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-ov", 1, `{"src_paths":["`+completedIn+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1, PathFormat: "/abs/bad"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "output path format") {
		t.Fatalf("bad format: status %d body %q; want 400 + format sentence", w.Code, w.Body.String())
	}

	// Each non-blank override alone is legal (the other side inherits).
	w = postReprocess(t, h, StartReprocessRequest{BookID: 1, OutputDir: altDir, PathFormat: "{title}"})
	if w.Code != http.StatusOK {
		t.Fatalf("valid overrides: status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	args := newestJobArgs(t, h.svc.DB, 1)
	absAlt, _ := filepath.Abs(altDir)
	if args.OutputDir != absAlt || args.OutputScheme != "{title}" {
		t.Errorf("stored snapshot = %q / %q; want %q / %q", args.OutputDir, args.OutputScheme, absAlt, "{title}")
	}

	// Blank overrides + no profile => destination fields omitted entirely.
	clearQueuedJobs(t, h.svc.DB, 1)
	w = postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("globals: status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var raw string
	h.svc.DB.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE book_id = 1 ORDER BY rowid DESC LIMIT 1").Scan(&raw)
	if strings.Contains(raw, "output_dir") || strings.Contains(raw, "output_scheme") {
		t.Errorf("global-settings args = %q; want no destination fields", raw)
	}
}

// TestReprocessOverrideDirFormatRejects pins the override-branch refusals:
// a RELATIVE output_dir 400s (destination roots are absolute; blank inherits
// the global) and a WHITESPACE-ONLY path_format 400s (a sent field is an
// override, not blank), and an absolute dir alone is stored as accepted.
func TestReprocessOverrideDirFormatRejects(t *testing.T) {
	completed := t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")
	if err := os.WriteFile(filepath.Join(completed, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-rejects", 1, `{"src_paths":["`+filepath.Join(completed, "a.m4b")+`"]}`, "2026-07-03 10:00:00")
	absAlt := filepath.Join(t.TempDir(), "alt-out")
	if err := os.MkdirAll(absAlt, 0o755); err != nil {
		t.Fatalf("mkdir alt: %v", err)
	}

	t.Run("relative dir", func(t *testing.T) {
		w := postReprocess(t, h, StartReprocessRequest{BookID: 1, OutputDir: "relative/out"})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "output directory must be an absolute path") {
			t.Fatalf("status %d body %q; want 400 + absolute-path sentence", w.Code, w.Body.String())
		}
	})

	t.Run("whitespace-only format", func(t *testing.T) {
		w := postReprocess(t, h, StartReprocessRequest{BookID: 1, PathFormat: "   \t "})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "output path format is empty") {
			t.Fatalf("status %d body %q; want 400 + empty-format sentence", w.Code, w.Body.String())
		}
	})

	t.Run("absolute dir accepted", func(t *testing.T) {
		clearQueuedJobs(t, h.svc.DB, 1) // t.Run is non-parallel: order is stable
		w := postReprocess(t, h, StartReprocessRequest{BookID: 1, OutputDir: absAlt})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		args := newestJobArgs(t, h.svc.DB, 1)
		if args.OutputDir != absAlt {
			t.Errorf("stored output_dir = %q; want %q", args.OutputDir, absAlt)
		}
	})
}

// TestReprocessDestinationProbeRejectsBeforeQueueing: an effective output
// destination (profile, explicit-override-free global) that is an EXISTING
// REGULAR FILE — plus a completed dir that is — must 400 the request,
// naming the failing destination, with no reprocess job row created
// (mirrors the queue-endpoint probe test on the reprocess path).
func TestReprocessDestinationProbeRejectsBeforeQueueing(t *testing.T) {
	remain := t.TempDir()
	origA := filepath.Join(remain, "a.m4b")
	if err := os.WriteFile(origA, []byte("x"), 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}
	aRegularFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(aRegularFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write regular-file destination: %v", err)
	}

	seedAndPost := func(h *Handler, req StartReprocessRequest) *httptest.ResponseRecorder {
		seedReprocessBook(t, h, 1, "done", remain)
		seedMergeRow(t, h, "seeded-probe", 1, `{"src_paths":["`+origA+`"]}`, "2026-07-03 10:00:00")
		return postReprocess(t, h, req)
	}

	t.Run("profile output dir", func(t *testing.T) {
		h := newReprocessHandler(t, t.TempDir(), t.TempDir(), "{author}/{title}", "")
		var profileID int64
		if _, err := h.svc.DB.Exec(
			"INSERT INTO output_profiles (name, output_dir) VALUES ('probe-blockaded', ?)", aRegularFile,
		); err != nil {
			t.Fatalf("seed profile: %v", err)
		}
		if err := h.svc.DB.QueryRow("SELECT id FROM output_profiles WHERE name = 'probe-blockaded'").Scan(&profileID); err != nil {
			t.Fatalf("read profile id: %v", err)
		}

		w := seedAndPost(h, StartReprocessRequest{BookID: 1, OutputProfileID: &profileID})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "output directory") || !strings.Contains(w.Body.String(), aRegularFile) {
			t.Errorf("body = %q; want it naming the failing profile destination", w.Body.String())
		}
		if n := jobCountForBook(t, h.svc.DB, 1); n != 1 {
			t.Errorf("job count = %d; want 1 (seeded only — probe runs before queueing)", n)
		}
	})

	t.Run("global output dir", func(t *testing.T) {
		h := newReprocessHandler(t, aRegularFile, t.TempDir(), "{author}/{title}", "")
		w := seedAndPost(h, StartReprocessRequest{BookID: 1})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "output directory") || !strings.Contains(w.Body.String(), aRegularFile) {
			t.Errorf("body = %q; want it naming the failing global destination", w.Body.String())
		}
		if n := jobCountForBook(t, h.svc.DB, 1); n != 1 {
			t.Errorf("job count = %d; want 1 (seeded only — probe runs before queueing)", n)
		}
	})

	t.Run("completed dir", func(t *testing.T) {
		// The output dir is a healthy temp dir so the probe reaches the
		// completed-dir check; the input resolves at its recorded path
		// (stat under a regular-file "dir" fails, original wins).
		h := newReprocessHandler(t, t.TempDir(), aRegularFile, "{author}/{title}", "")
		w := seedAndPost(h, StartReprocessRequest{BookID: 1})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "completed directory") || !strings.Contains(w.Body.String(), aRegularFile) {
			t.Errorf("body = %q; want it naming the failing completed directory", w.Body.String())
		}
		if n := jobCountForBook(t, h.svc.DB, 1); n != 1 {
			t.Errorf("job count = %d; want 1 (seeded only — probe runs before queueing)", n)
		}
	})
}

// TestReprocessPreview pins the preview wire shape on mixed locations:
// completed_dir from the live config, the seeded row's created_at +
// snapshot pair (absent fields read as ""), and the 2/1/1 input split with
// no missing names.
func TestReprocessPreview(t *testing.T) {
	remain, completed := t.TempDir(), t.TempDir()
	h := newReprocessHandler(t, t.TempDir(), completed, "{author}/{title}", "")
	origB := filepath.Join(remain, "b.m4b")
	for _, p := range []string{origB, filepath.Join(completed, "a.m4b")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	seedReprocessBook(t, h, 1, "done", remain)
	seedMergeRow(t, h, "seeded-prev", 1,
		`{"src_paths":["`+origB+`","`+filepath.Join(completed, "a.m4b")+`"],`+
			`"output_dir":"`+t.TempDir()+`","output_scheme":"Genre/{title}"}`, "2026-07-03 09:15:00")

	rec := getReprocess(t, h, 1)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var prev reprocessPreviewWire
	if err := json.NewDecoder(rec.Body).Decode(&prev); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !prev.Available || prev.Reason != "" {
		t.Fatalf("preview = %+v; want available", prev)
	}
	if prev.CompletedDir != completed {
		t.Errorf("completed_dir = %q; want %q", prev.CompletedDir, completed)
	}
	if prev.LastRun == nil || prev.LastRun.JobID != "seeded-prev" || prev.LastRun.CreatedAt != "2026-07-03 09:15:00" {
		t.Fatalf("last_run = %+v; want the seeded row's identity + created_at", prev.LastRun)
	}
	if prev.LastRun.OutputScheme != "Genre/{title}" || prev.LastRun.OutputDir == "" {
		t.Errorf("last_run destination = %q / %q; want the seeded snapshot pair", prev.LastRun.OutputDir, prev.LastRun.OutputScheme)
	}
	if prev.Inputs == nil || prev.Inputs.Total != 2 || prev.Inputs.Found != 2 ||
		prev.Inputs.FoundInCompleted != 1 || prev.Inputs.FoundInOriginal != 1 || len(prev.Inputs.Missing) != 0 {
		t.Fatalf("inputs = %+v; want 2/2 with a 1/1 split and nothing missing", prev.Inputs)
	}

	// A global-settings run (no snapshot) reads back as the empty pair.
	seedMergeRow(t, h, "seeded-nosnap", 1, `{"src_paths":["`+filepath.Join(completed, "a.m4b")+`"]}`, "2026-07-03 09:16:00")
	rec = getReprocess(t, h, 1)
	if err := json.NewDecoder(rec.Body).Decode(&prev); err != nil {
		t.Fatalf("decode second preview: %v", err)
	}
	if prev.LastRun == nil || prev.LastRun.JobID != "seeded-nosnap" || prev.LastRun.OutputDir != "" || prev.LastRun.OutputScheme != "" {
		t.Fatalf("unsnapshotted last_run = %+v; want the newer row with an empty pair", prev.LastRun)
	}
}

// TestReprocessLatestJobTieBreak pins the rowid tie-break: two merge rows
// with the SAME created_at string resolve to the later insert, not arbitrary
// order. The first row's only input is missing (a 400 if it were picked), so
// a 200 with the second row's resolved path is the proof.
func TestReprocessLatestJobTieBreak(t *testing.T) {
	h := newReprocessHandler(t, t.TempDir(), t.TempDir(), "{author}/{title}", "")
	completed := t.TempDir()
	completedIn := filepath.Join(completed, "x.m4b")
	if err := os.WriteFile(completedIn, []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-old", 1, `{"src_paths":["/does/not/exist.m4b"]}`, "2026-07-03 10:00:00")
	seedMergeRow(t, h, "seeded-new", 1, `{"src_paths":["`+completedIn+`"]}`, "2026-07-03 10:00:00")

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s) — the newer rowid must win the tied created_at", w.Code, w.Body.String())
	}
	if got := newestJobArgs(t, h.svc.DB, 1).SrcPaths; len(got) != 1 || got[0] != completedIn {
		t.Errorf("resolved src_paths = %v; want [%s]", got, completedIn)
	}
}

// TestReprocessEndToEndOnce runs the full worker path once: reprocess queues,
// m4b-merge (fake) merges, the job reaches done and the book gets its
// dest_path with status done.
func TestReprocessEndToEndOnce(t *testing.T) {
	out, completed := t.TempDir(), t.TempDir()
	bin := writeMergingBinary(t, t.TempDir())
	h := newReprocessHandler(t, out, completed, "{author}/{title}", bin)
	h.svc.ProcessingSvc.proc = &audio.Processor{}
	h.svc.ProcessingSvc.proc.ApplyConfig(h.svc.Config.Config())

	completedIn := filepath.Join(completed, "a.m4b")
	if err := os.WriteFile(completedIn, []byte("x"), 0o644); err != nil {
		t.Fatalf("write completed: %v", err)
	}
	seedReprocessBook(t, h, 1, "done", t.TempDir())
	seedMergeRow(t, h, "seeded-e2e", 1, `{"src_paths":["`+completedIn+`"]}`, "2026-07-03 10:00:00")

	ctx := t.Context()
	h.svc.ProcessingSvc.RunWorkers(ctx, 1)

	w := postReprocess(t, h, StartReprocessRequest{BookID: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp StartProcessingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("jobs = %+v; want exactly one", resp.Jobs)
	}
	waitJobStatus(t, h.svc.DB, resp.Jobs[0].ID, jobStatusDone)
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status after run = %q; want done", got)
	}
	var dest string
	if err := h.svc.DB.QueryRow("SELECT dest_path FROM books WHERE id = 1").Scan(&dest); err != nil {
		t.Fatalf("dest_path query: %v", err)
	}
	if dest == "" {
		t.Error("dest_path empty after a successful reprocess run")
	}
}
