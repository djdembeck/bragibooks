package api

import (
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
	h := &Handler{svc: Services{DB: db, ProcessingSvc: svc}}
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
	// bookID 1 -> QueueJob flips the book to 'processing'.
	id, err := h.svc.ProcessingSvc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"})
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}

	status, err := jobStatusFromDB(t, h.svc.DB, id)
	if err != nil || status != "queued" {
		t.Fatalf("precondition: job status = %q, err = %v; want queued", status, err)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "processing" {
		t.Fatalf("precondition: book status = %q; want processing", got)
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
