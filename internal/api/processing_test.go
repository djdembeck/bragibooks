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
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// processingSchemaSQL adds the processing_jobs table to the books test schema.
const processingSchemaSQL = `
CREATE TABLE IF NOT EXISTS processing_jobs (
	id TEXT PRIMARY KEY,
	book_id INTEGER REFERENCES books(id) ON DELETE SET NULL,
	m4b_merge_args TEXT,
	action TEXT NOT NULL DEFAULT 'merge',
	detail TEXT,
	status TEXT DEFAULT 'queued',
	output TEXT DEFAULT '',
	error TEXT DEFAULT '',
	output_file TEXT,
	started_at TEXT,
	completed_at TEXT,
	created_at TEXT DEFAULT (datetime('now'))
);
`

// newTestJobsHandler returns a Handler with a ProcessingService backed by an
// isolated file-backed SQLite DB in a temp dir. File-backed is used (rather
// than a shared-cache in-memory DB) because the worker goroutine and the test
// both access the pool concurrently, and a shared-cache in-memory DB can be
// dropped when the last pooled connection closes.
func newTestJobsHandler(t *testing.T) (*Handler, *ProcessingService) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(processingSchemaSQL); err != nil {
		t.Fatalf("create jobs schema: %v", err)
	}

	svc := NewProcessingService(db, &audio.Processor{}, 1)
	h := &Handler{svc: &Services{DB: db, ProcessingSvc: svc}}
	return h, svc
}

// writeSleepingBinary writes a shell script to dir that sleeps, so a queued
// job transitions to running and can be cancelled mid-flight. It execs sleep
// with stdout/stderr closed so that, when the process group is killed on
// cancellation, no orphaned child holds the process's StdoutPipe open — that
// would otherwise block io.ReadAll and make cmd.Wait() never return.
func writeSleepingBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "sleep-bin")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 300 1>&- 2>&-\n"), 0o755); err != nil {
		t.Fatalf("write sleeping binary: %v", err)
	}
	return p
}

// setJobStatus directly overwrites a job's DB status, to place a job in a
// terminal state for the 409 path.
func setJobStatus(t *testing.T, db *sql.DB, id, status string) {
	t.Helper()
	if _, err := db.Exec("UPDATE processing_jobs SET status = ? WHERE id = ?", status, id); err != nil {
		t.Fatalf("set job status: %v", err)
	}
}

// jobStatusFromDB reads a job's status from the DB.
func jobStatusFromDB(t *testing.T, db *sql.DB, id string) (string, error) {
	t.Helper()
	var status string
	err := db.QueryRow("SELECT status FROM processing_jobs WHERE id = ?", id).Scan(&status)
	return status, err
}

// bookStatusFromDB reads a book's status from the DB.
func bookStatusFromDB(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var status string
	if err := db.QueryRow("SELECT status FROM books WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatalf("book status query: %v", err)
	}
	return status
}

// waitJobStatus polls a job's DB status until it reaches want or times out.
func waitJobStatus(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := jobStatusFromDB(t, db, id)
		if err == nil && status == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %s never reached status %q", id, want)
}

// doCancel posts to the cancel route for the given job id and returns the
// recorder. The handler is invoked directly (not through the chi router), so
// the {id} URL param is injected into a chi route context by hand.
func doCancel(t *testing.T, h *Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/jobs/"+id+"/cancel", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.CancelJob(w, req)
	return w
}

func TestCancelQueuedJob(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	// Seed a book in 'matched' state, matching the real flow pre-processing.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Queued Book', 'matched', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	// Queue a job without starting workers, so it stays 'queued'.
	// Queueing intentionally leaves the book in its pre-queue state ('matched'):
	// the queued -> processing transition happens at worker start, so a queued
	// cancel needs no status rollback.
	id, err := h.svc.ProcessingSvc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}

	status, err := jobStatusFromDB(t, h.svc.DB, id)
	if err != nil || status != "queued" {
		t.Fatalf("precondition: job status = %q, err = %v; want queued", status, err)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "matched" {
		t.Fatalf("precondition: book status = %q; want matched (pre-queue state preserved)", got)
	}

	w := doCancel(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] != id {
		t.Errorf("resp id = %v; want %q", resp["id"], id)
	}
	if resp["status"] != "cancelled" {
		t.Errorf("resp status = %v; want cancelled", resp["status"])
	}
	if b, ok := resp["book_id"].(float64); !ok || b != 1 {
		t.Errorf("resp book_id = %v; want 1", resp["book_id"])
	}

	dbStatus, _ := jobStatusFromDB(t, h.svc.DB, id)
	if dbStatus != "cancelled" {
		t.Errorf("db job status = %q; want cancelled", dbStatus)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "matched" {
		t.Errorf("db book status = %q; want matched", got)
	}

	// Cancelling again must be a 409 (already terminal).
	w2 := doCancel(t, h, id)
	if w2.Code != http.StatusConflict {
		t.Errorf("second cancel status = %d; want 409 — body: %s", w2.Code, w2.Body.String())
	}
}

func TestCancelRunningJob(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	bin := writeSleepingBinary(t, t.TempDir())
	svc.proc = &audio.Processor{Binary: bin}

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Running Book', 'matched', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}

	// Wait until the worker has started the process (status running).
	waitJobStatus(t, h.svc.DB, id, "running")

	w := doCancel(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}

	// The worker's finalizeCancelled marks the job 'cancelled' after the
	// context kill.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := jobStatusFromDB(t, h.svc.DB, id)
		if status == "cancelled" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	finalStatus, _ := jobStatusFromDB(t, h.svc.DB, id)
	if finalStatus != "cancelled" {
		t.Fatalf("final job status = %q; want cancelled (process must be killed, not errored)", finalStatus)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "matched" {
		t.Errorf("final book status = %q; want matched", got)
	}
}

func TestCancelTerminalJobReturns409(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	// Seed jobs directly in terminal states.
	for _, status := range []string{"done", "error", "cancelled"} {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES (?, 1, '{}', ?)",
			"job-"+status, status,
		); err != nil {
			t.Fatalf("seed job %s: %v", status, err)
		}
	}

	for _, status := range []string{"done", "error", "cancelled"} {
		w := doCancel(t, h, "job-"+status)
		if w.Code != http.StatusConflict {
			t.Errorf("cancel %s job status = %d; want 409 — body: %s", status, w.Code, w.Body.String())
		}
	}
}

func TestCancelUnknownJobReturns404(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	w := doCancel(t, h, "does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cancel unknown status = %d; want 404 — body: %s", w.Code, w.Body.String())
	}
}

func TestQueueJobRecordsMergeAction(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Book', 'matched', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	id, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	var action string
	if err := h.svc.DB.QueryRow("SELECT action FROM processing_jobs WHERE id = ?", id).Scan(&action); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if action != "merge" {
		t.Fatalf("action = %q; want merge", action)
	}
}

func TestListJobsCarriesAction(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	h.svc.DB.Exec("INSERT INTO processing_jobs (id, book_id, status, action) VALUES ('j1', 1, 'done', 'import')")
	h.svc.DB.Exec("INSERT INTO processing_jobs (id, book_id, status, action) VALUES ('j2', 1, 'done', 'metadata_update')")

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListJobsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Fatalf("jobs = %d; want 2", len(resp.Jobs))
	}
	byID := map[string]any{}
	for _, j := range resp.Jobs {
		byID[j["id"].(string)] = j["action"]
	}
	if byID["j1"] != "import" {
		t.Errorf("j1 action = %v; want import", byID["j1"])
	}
	if byID["j2"] != "metadata_update" {
		t.Errorf("j2 action = %v; want metadata_update", byID["j2"])
	}
}

func TestGetJobStatusCarriesAction(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	// Audit rows have no merge args (NULL); the pre-v8 fixture stuffed the
	// detail into m4b_merge_args, which the scan would reject.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, detail) VALUES ('j1', 1, 'done', 'import', '/in/a.m4b')",
	); err != nil {
		t.Fatalf("seed audit row: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/jobs/j1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, "j1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.GetJobStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["action"] != "import" {
		t.Fatalf("action = %v; want import", resp["action"])
	}
	if resp["detail"] != "/in/a.m4b" {
		t.Errorf("detail = %v; want /in/a.m4b", resp["detail"])
	}
}

func TestRecordBookActionWritesTerminalRow(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	h.svc.DB.Exec("INSERT INTO books (id, title, status) VALUES (7, 'Book', 'pending')")

	svc.RecordBookAction(7, "import", "/in/a.m4b")

	var (
		status, action, detail, done string
		bookID                       int64
		args                         sql.NullString
	)
	err := h.svc.DB.QueryRow(
		"SELECT status, action, book_id, m4b_merge_args, detail, completed_at FROM processing_jobs WHERE action = 'import'",
	).Scan(&status, &action, &bookID, &args, &detail, &done)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q; want done", status)
	}
	if bookID != 7 {
		t.Errorf("book_id = %d; want 7", bookID)
	}
	if done == "" {
		t.Errorf("completed_at empty; want set")
	}
	if detail != "/in/a.m4b" {
		t.Errorf("detail = %q; want /in/a.m4b", detail)
	}
	if args.Valid {
		t.Errorf("m4b_merge_args = %q; want NULL (the args column belongs to merge jobs)", args.String)
	}
}

func TestListBooksQFilter(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	books := []struct {
		title, series, status string
	}{
		{"Dune Messiah", "Dune", "matched"},
		{"Neuromancer", "Case", "pending"},
		{"Hyperion", "Hyperion Cantos", "done"},
		{"Dune", "Dune", "matched"},
		{"Redshirts", "", "error"},
	}
	for i, b := range books {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO books (id, title, series, status) VALUES (?, ?, ?, ?)",
			i+1, b.title, b.series, b.status,
		); err != nil {
			t.Fatalf("seed book %d: %v", i+1, err)
		}
	}

	// Case-insensitive substring on title.
	req := httptest.NewRequest("GET", "/api/books?q=dune", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// "dune" matches titles "Dune Messiah", "Dune" (2). Series "Dune" would
	// match "Dune" too but it is the same row, so total distinct = 2.
	if resp.Total != 2 {
		t.Errorf("q=dune total = %d; want 2", resp.Total)
	}
	if len(resp.Books) != 2 {
		t.Errorf("q=dune books len = %d; want 2", len(resp.Books))
	}

	// q matching on series: "Hyperion Cantos" series matches Hyperion.
	req = httptest.NewRequest("GET", "/api/books?q=cantos", nil)
	w = httptest.NewRecorder()
	h.ListBooks(w, req)
	var resp2 ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.Total != 1 {
		t.Errorf("q=cantos total = %d; want 1 (series match)", resp2.Total)
	}
	if resp2.Total == 1 && resp2.Books[0].Title != "Hyperion" {
		t.Errorf("q=cantos book = %q; want Hyperion", resp2.Books[0].Title)
	}

	// q composable with status filter.
	req = httptest.NewRequest("GET", "/api/books?q=dune&status=matched", nil)
	w = httptest.NewRecorder()
	h.ListBooks(w, req)
	var resp3 ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp3); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Both Dune books are 'matched'.
	if resp3.Total != 2 {
		t.Errorf("q=dune&status=matched total = %d; want 2", resp3.Total)
	}

	// q with no matches.
	req = httptest.NewRequest("GET", "/api/books?q=zzzz", nil)
	w = httptest.NewRecorder()
	h.ListBooks(w, req)
	var resp4 ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp4); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp4.Total != 0 || len(resp4.Books) != 0 {
		t.Errorf("q=zzzz total = %d, books = %d; want 0, 0", resp4.Total, len(resp4.Books))
	}
}

func TestListBooksAllIDs(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	// Seed 1000+ books to exercise the cap.
	for i := 1; i <= 1005; i++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO books (id, title, status) VALUES (?, ?, 'matched')",
			i, fmt.Sprintf("Title %04d", i),
		); err != nil {
			t.Fatalf("seed book %d: %v", i, err)
		}
	}

	req := httptest.NewRequest("GET", "/api/books?all_ids=1", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("all_ids status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp map[string][]int64
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids, ok := resp["ids"]
	if !ok {
		t.Fatalf("response missing 'ids' key: %s", w.Body.String())
	}
	if len(ids) != maxBookIDs {
		t.Errorf("all_ids returned %d ids; want cap %d", len(ids), maxBookIDs)
	}
	// ids must be a valid non-empty subset of seeded IDs.
	for _, id := range ids {
		if id < 1 || id > 1005 {
			t.Errorf("all_ids returned out-of-range id %d", id)
			break
		}
	}
}

func TestGetBooksCountsShape(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	seeds := []struct {
		title, status string
		n             int
	}{
		{"A", "pending", 2},
		{"B", "matched", 3},
		{"C", "processing", 1},
		{"D", "done", 4},
		{"E", "error", 1},
	}
	for _, s := range seeds {
		for i := 0; i < s.n; i++ {
			if _, err := h.svc.DB.Exec(
				"INSERT INTO books (title, status) VALUES (?, ?)",
				s.title, s.status,
			); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	req := httptest.NewRequest("GET", "/api/books/counts", nil)
	w := httptest.NewRecorder()
	h.GetBooksCounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("counts status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}

	var resp BooksCountsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]int{"pending": 2, "matched": 3, "processing": 1, "done": 4, "error": 1}
	for k, v := range want {
		if resp.Counts[k] != v {
			t.Errorf("counts[%s] = %d; want %d", k, resp.Counts[k], v)
		}
	}
	// The response must always carry exactly the five known keys.
	if len(resp.Counts) != 5 {
		t.Errorf("counts has %d keys; want 5: %v", len(resp.Counts), resp.Counts)
	}
}

func TestSweepOrphansOnStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sweep.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(processingSchemaSQL); err != nil {
		t.Fatalf("create jobs schema: %v", err)
	}

	// A prior run's state: one queued job, one running job (both with stuck
	// books), and one finished job that must be left alone.
	seed := `
		INSERT INTO books (id, title, status) VALUES (1, 'Queued Book', 'processing'), (2, 'Running Book', 'processing'), (3, 'Done Book', 'done');
		INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES
			('q1', 1, '{}', 'queued'),
			('r1', 2, '{}', 'running'),
			('d1', 3, '{}', 'done');
	`
	if _, err := db.Exec(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Constructing the service triggers the startup sweep.
	NewProcessingService(db, &audio.Processor{}, 1)

	for id, want := range map[string]string{"q1": "error", "r1": "error", "d1": "done"} {
		status, err := jobStatusFromDB(t, db, id)
		if err != nil {
			t.Fatalf("job %s: %v", id, err)
		}
		if status != want {
			t.Errorf("job %s status = %q; want %q", id, status, want)
		}
	}
	var orphanErr string
	db.QueryRow("SELECT error FROM processing_jobs WHERE id = 'q1'").Scan(&orphanErr)
	if !strings.Contains(orphanErr, "orphaned") {
		t.Errorf("q1 error = %q; want it to contain %q", orphanErr, "orphaned")
	}
	for id, want := range map[int64]string{1: "pending", 2: "pending", 3: "done"} {
		if got := bookStatusFromDB(t, db, int64(id)); got != want {
			t.Errorf("book %d status = %q; want %q", id, got, want)
		}
	}
}

// TestStreamJobActiveJobPayload covers the NULL-scan fix: active jobs have
// NULL output_file/started_at/completed_at, which must not kill the stream.
func TestStreamJobActiveJobPayload(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	// A queued job exactly as QueueJob would insert it: output_file,
	// started_at and completed_at are NULL.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES ('active-1', 1, '{}', 'queued')",
	); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, "active-1")
	req := httptest.NewRequest("GET", "/api/jobs/active-1/stream", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.StreamJob(w, req)

	// The pre-fix scan error made the handler return silently; the active
	// job must produce a payload its current state.
	body := w.Body.String()
	if !strings.Contains(body, `"status":"queued"`) {
		t.Errorf("stream body = %q; want the queued status payload", body)
	}
}

// seedAuditRows inserts n terminal import audit rows at 10-minute intervals
// from "2026-01-02 09:00:00", to simulate a batch of imports landing while a
// merge is in flight. For n >= 5 the whole recent window (last 5 rows) sits
// on 01-02, and every job seeded on 01-01 sorts out of it.
func seedAuditRows(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		mins := 10 * (i - 1)
		if _, err := db.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, detail, created_at) VALUES (?, 1, 'done', 'import', ?, ?)",
			fmt.Sprintf("audit-%02d", i), fmt.Sprintf("/in/%02d.m4b", i), fmt.Sprintf("2026-01-02 %02d:%02d:00", 9+mins/60, mins%60),
		); err != nil {
			t.Fatalf("seed audit %d: %v", i, err)
		}
	}
}

// jobIDsFromList decodes a ListJobsResponse body into an id->status map and
// the ordered job id sequence.
func jobIDsFromList(t *testing.T, body *httptest.ResponseRecorder) (map[string]string, []string) {
	t.Helper()
	var resp ListJobsResponse
	if err := json.NewDecoder(body.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := make(map[string]string, len(resp.Jobs))
	ids := make([]string, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		id, _ := j["id"].(string)
		byID[id] = j["status"].(string)
		ids = append(ids, id)
	}
	return byID, ids
}

// Regression: the Queue page derives its active set from GET /jobs. When a
// batch of audit rows (imports/metadata saves) lands while a merge is in
// flight, the recent-history LIMIT used to push the active job out of the
// response; it must now be appended after the pointed-at window.
func TestListJobsAppendsActiveJobs(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'processing')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// The active merge, created before the audit batch (created_at 01-01,
	// then audits at 01-02 ...) so it sorts OUT of a 5-row recent window.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at, started_at) VALUES ('m1', 1, '{\"src_paths\":[\"/in/a.m4b\"]}', 'running', 'merge', '2026-01-01 00:00:00', '2026-01-01 00:00:00')",
	); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	// A second active merge created INSIDE the top-5 window (between
	// audit-24 and audit-25 on 01-02): the append must skip it without
	// duplicating it.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at, started_at) VALUES ('m2', 1, '{\"src_paths\":[\"/in/b.m4b\"]}', 'running', 'merge', '2026-01-02 12:55:00', '2026-01-02 12:55:00')",
	); err != nil {
		t.Fatalf("seed in-window active job: %v", err)
	}
	seedAuditRows(t, h.svc.DB, 25)

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=5", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	byID, ids := jobIDsFromList(t, w)

	if status, ok := byID["m1"]; !ok || status != "running" {
		t.Fatalf("active job m1 missing from list (got %d jobs); the limit must not push running work out", len(byID))
	}
	if status, ok := byID["m2"]; !ok || status != "running" {
		t.Fatalf("in-window active job m2 missing from list (got %d jobs)", len(byID))
	}
	// No duplicates from the append: 5 recent (incl. in-window m2) + 1
	// appended (m1) = 6 list entries. The raw length — not the id map —
	// grows if the append re-emits a job the window already carried.
	if n := len(ids); n != 6 {
		t.Errorf("list len = %d; want 5 recent + 1 appended active", n)
	}
	// The recent window stays newest-first, with the active job appended last.
	if len(ids) == 0 || ids[0] != "audit-25" {
		t.Errorf("first job = %v; want the newest audit row", ids)
	}
	if len(ids) == 0 || ids[len(ids)-1] != "m1" {
		t.Errorf("last job = %v; want the appended active job", ids)
	}
}

// Regression: the book_id scope must apply to BOTH the recent-history LIMIT
// and the active append. A scoped list carries only that book's history and
// that book's live work — another book's running merge and audit rows must
// never leak in.
func TestListJobsBookScopeAppliesToAppend(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book One', 'processing'), (2, 'Book Two', 'processing')",
	); err != nil {
		t.Fatalf("seed books: %v", err)
	}
	// Book 1's active merge, created before the audit batch (created_at
	// 01-01) so it sorts OUT of the 5-row recent window and surfaces only
	// via the book-scoped active append.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at, started_at) VALUES ('m1', 1, '{\"src_paths\":[\"/in/a.m4b\"]}', 'running', 'merge', '2026-01-01 00:00:00', '2026-01-01 00:00:00')",
	); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	// Book 2's active merge, created INSIDE its own recent window (like m2
	// in the unscoped test), so it appears via the window — not the append.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, created_at, started_at) VALUES ('m2b', 2, '{\"src_paths\":[\"/in/b.m4b\"]}', 'running', 'merge', '2026-01-02 12:55:00', '2026-01-02 12:55:00')",
	); err != nil {
		t.Fatalf("seed in-window active job: %v", err)
	}
	// Book 1's audit rows fill its recent window (5 rows on 01-02).
	seedAuditRows(t, h.svc.DB, 5)
	// Book 2 gets the same shape of audit rows under its own books and ids.
	for i := 1; i <= 5; i++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, detail, created_at) VALUES (?, 2, 'done', 'import', ?, ?)",
			fmt.Sprintf("audit2-%02d", i), fmt.Sprintf("/in2/%02d.m4b", i), fmt.Sprintf("2026-01-02 09:%02d:00", 10*(i-1)),
		); err != nil {
			t.Fatalf("seed book-2 audit %d: %v", i, err)
		}
	}

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=5&book_id=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	byID, ids := jobIDsFromList(t, w)

	// Book 1's running job must be appended despite sorting out of its own window.
	if status, ok := byID["m1"]; !ok || status != "running" {
		t.Fatalf("book-1 active job m1 missing from list (got %d jobs)", len(byID))
	}
	// Book 2's active job and history must not leak into book 1's scope,
	// which means both the recent query and the active append honor book_id.
	if _, ok := byID["m2b"]; ok {
		t.Errorf("book-2 active job m2b present in book-1 list")
	}
	for id := range byID {
		if strings.HasPrefix(id, "audit2-") {
			t.Errorf("book-2 audit row %s present in book-1 list", id)
		}
	}
	// 5 book-1 audits (recent window, newest-first) + 1 appended active.
	if n := len(ids); n != 6 {
		t.Errorf("book-1 list len = %d; want 5 recent + 1 appended active", n)
	}
	if len(ids) == 0 || ids[0] != "audit-05" {
		t.Errorf("first job = %v; want the newest book-1 audit row", ids)
	}
	if len(ids) == 0 || ids[len(ids)-1] != "m1" {
		t.Errorf("last job = %v; want the appended active job", ids)
	}

	// Mirror image: book 2's scope carries its in-window active job and
	// none of book 1's rows.
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=5&book_id=2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	byID, ids = jobIDsFromList(t, w)

	if status, ok := byID["m2b"]; !ok || status != "running" {
		t.Fatalf("book-2 active job m2b missing from list (got %d jobs)", len(byID))
	}
	if _, ok := byID["m1"]; ok {
		t.Errorf("book-1 active job m1 present in book-2 list")
	}
	for id := range byID {
		if strings.HasPrefix(id, "audit-") {
			t.Errorf("book-1 audit row %s present in book-2 list", id)
		}
	}
	// 5 in-window rows (m2b + 4 audits) — m2b is already in the window, so
	// the append adds nothing.
	if n := len(ids); n != 5 {
		t.Errorf("book-2 list len = %d; want 5 in-window, nothing appended", n)
	}
	if len(ids) == 0 || ids[0] != "m2b" {
		t.Errorf("first job = %v; want the in-window active merge", ids)
	}
}

// The jobs routes resolve after the route-registration cleanup: the list
// endpoint answers, and an unknown id still reaches GetJobStatus with 404.
func TestJobsRoutesResolve(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	router := chi.NewRouter()
	h.RegisterRoutes(router, func(next http.Handler) http.Handler { return next })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/jobs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/jobs status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListJobsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(resp.Jobs) != 0 {
		t.Errorf("empty db list = %d jobs; want 0", len(resp.Jobs))
	}

	// An unknown id still reaches the single-job handler: 404.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/jobs/does-not-exist", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("/jobs/{bad id} status = %d; want 404 — body: %s", w.Code, w.Body.String())
	}
}

// fakeM4bBinary writes a shell script that exits 0 immediately and reports a
// version, standing in for m4b-merge in pipeline tests.
func fakeM4bBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fake-m4b")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 1.0.0; exit 0; fi\necho ran\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return p
}

// TestStreamJobProgressCloseEndsStream covers the row-16 fix: finalize closes
// the progress channel, so the SSE loop terminates on its own (and the
// trailing "done" line is written) without waiting for a client disconnect.
func TestStreamJobProgressCloseEndsStream(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	st := svc.GetJob(id)
	if st == nil {
		t.Fatal("no in-memory state for queued job")
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, id)
	req := httptest.NewRequest("GET", "/api/jobs/"+id+"/stream", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		h.StreamJob(w, req)
	}()

	// Give the stream a moment to connect, then finalize as a finished job
	// would.
	time.Sleep(100 * time.Millisecond)
	st.finalize(`{"status":"done","message":"processing complete"}`, nil)

	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after progress channel close")
	}

	body := w.Body.String()
	if !strings.Contains(body, `"status":"done","message":"processing complete"`) {
		t.Errorf("stream body = %q; want the terminal progress event", body)
	}
	if !strings.Contains(body, `{"status":"done"}`) {
		t.Errorf("stream body = %q; want the trailing done line from the done channel", body)
	}
}

// TestQueueJobDuplicateRejected covers the server-side 409 guard.
func TestQueueJobDuplicateRejected(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	first, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("first queue: %v", err)
	}
	if _, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}); err == nil {
		t.Fatal("second queue for a book with a queued job: want error, got nil")
	}

	// A finished job frees the book for a new run.
	setJobStatus(t, h.svc.DB, first, "done")
	if _, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}); err != nil {
		t.Fatalf("queue after terminal job: %v", err)
	}
}

// TestStartProcessingDuplicateBook409 exercises the same guard through the
// handler: a repeated book_id in one request body is a 409, not a double run.
func TestStartProcessingDuplicateBook409(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed audio: %v", err)
	}

	body, _ := json.Marshal(StartProcessingRequest{BookIDs: []int64{1, 1}, SrcDirs: []string{srcDir, srcDir}})
	req := httptest.NewRequest("POST", "/api/process", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.StartProcessing(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate book status = %d; want 409 — body: %s", w.Code, w.Body.String())
	}
}

// writeMergingBinary writes an executable shell script that mimics a
// successful m4b-merge run: it honors -o <dir> and creates
// <dir>/Author/Title.m4b.
func writeMergingBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "merging-bin")
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift; fi
  shift
done
mkdir -p "$out/Author"
printf 'book' > "$out/Author/Title.m4b"
echo merged
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write merging binary: %v", err)
	}
	return p
}

// TestProcessJobRecordsOutputFormat is the regression test for #438: the run
// that discovers the produced .m4b knows its format, so the success write
// must persist it alongside dest_path. Without it the book page's FILE PATHS
// card falls back to "Unknown" forever.
func TestProcessJobRecordsOutputFormat(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	bin := writeMergingBinary(t, t.TempDir())
	svc.proc = &audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}", LogLevel: "info"}

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{filepath.Join(t.TempDir(), "a.m4b")})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	waitJobStatus(t, h.svc.DB, id, "done")

	var format, dest string
	if err := h.svc.DB.QueryRow(
		"SELECT format_type, dest_path FROM books WHERE id = 1",
	).Scan(&format, &dest); err != nil {
		t.Fatalf("book format query: %v", err)
	}
	if format != "m4b" {
		t.Errorf("format_type = %q; want m4b for the discovered .m4b output", format)
	}
	if dest == "" {
		t.Error("dest_path = empty; want the discovered output path")
	}
}

// TestFinishedJobLeavesMap covers the terminal cleanup: after a job reaches a
// terminal state its in-memory state is dropped, while the DB retains it.
func TestFinishedJobLeavesMap(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	bin := fakeM4bBinary(t, t.TempDir())
	svc.proc = &audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}", LogLevel: "info"}

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{filepath.Join(t.TempDir(), "a.m4b")})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}

	waitJobStatus(t, h.svc.DB, id, "done")

	if st := svc.GetJob(id); st != nil {
		t.Errorf("GetJob after terminal state = non-nil; want map entry removed")
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status = %q; want done", got)
	}
}
