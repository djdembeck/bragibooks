package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/models"
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
	created_at TEXT DEFAULT (datetime('now')),
	queue_seq INTEGER,
	book_pre_status TEXT,
	batch_id TEXT,
	batch_closed INTEGER NOT NULL DEFAULT 0,
	snapshot_version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS book_snapshots (
    book_id INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    audiobookdb_book_id TEXT NOT NULL DEFAULT '',
    audiobookdb_release_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    subtitle TEXT NOT NULL DEFAULT '',
    authors TEXT NOT NULL DEFAULT '[]',
    narrators TEXT NOT NULL DEFAULT '[]',
    series_name TEXT NOT NULL DEFAULT '',
    series_position TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    genres TEXT NOT NULL DEFAULT '[]',
    year INTEGER,
    cover_file TEXT NOT NULL DEFAULT '',
    chapters TEXT NOT NULL DEFAULT '[]',
    captured_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS book_metadata_overrides (
    book_id INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    subtitle TEXT NOT NULL DEFAULT '',
    authors TEXT NOT NULL DEFAULT '',
    narrators TEXT NOT NULL DEFAULT '',
    series_name TEXT NOT NULL DEFAULT '',
    series_position TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    genres TEXT NOT NULL DEFAULT '',
    year INTEGER,
    cleared TEXT NOT NULL DEFAULT '[]',
    cover_source TEXT NOT NULL DEFAULT '',
    cover_file TEXT NOT NULL DEFAULT '',
    cover_url TEXT NOT NULL DEFAULT '',
    chapters TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
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
	// WAL + busy_timeout (internal/db/sqlite.go also enables foreign keys,
	// but the pre-existing tests seed books/jobs without matching rows and
	// rely on that being off, so it is intentionally omitted here). The
	// worker goroutine and the test poll the DB concurrently, and without a
	// busy timeout their writes race into SQLITE_BUSY.
	db, err := sql.Open("sqlite", fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path,
	))
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

	// The processor carries a queueable global destination: the queue path
	// freezes the effective output root + format into every job, so an
	// empty global is a refused (not inheritable) configuration. Tests
	// that exercise specific destinations swap svc.proc below (and re-store
	// the engine identity via testProcessor, which seeds it).
	svc := NewProcessingService(db, testProcessor(&audio.Processor{
		OutputDir:  t.TempDir(),
		PathFormat: "{title}",
		LogLevel:   "info",
	}), 1)
	h := &Handler{svc: &Services{DB: db, ProcessingSvc: svc}}
	return h, svc
}

// seedSnapshot inserts a minimal authoritative capture row for a book so the
// queue path's hard prerequisite (an operator-captured selection) is met.
// Pipeline tests are about the job mechanics, not the capture, so the row
// carries just the book's title; a real capture comes from Match/save.
func seedSnapshot(t *testing.T, db *sql.DB, bookID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO book_snapshots (book_id, title, authors, narrators)
		 VALUES (?, (SELECT title FROM books WHERE id = ?), '["Author"]', '["Narrator"]')`,
		bookID, bookID,
	); err != nil {
		t.Fatalf("seed snapshot for book %d: %v", bookID, err)
	}
}

// testProcessor builds a fake-engine Processor with the verified engine
// identity stored, the same pair the real startup path (CheckCapabilities +
// StoreCapabilities) produces. A job reports back the identity captured at
// queue time, so a test that swaps the processor must re-store it.
func testProcessor(p *audio.Processor) *audio.Processor {
	p.StoreCapabilities(&audio.Capabilities{SchemaVersion: 1, ResolverVersion: "1"})
	return p
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

// writeStreamingSleepBinary prints one progress line to stderr (the engine's
// log stream in machine mode) before sleeping, so a running job has live
// output in memory when it is cancelled.
func writeStreamingSleepBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "stream-sleep-bin")
	// The --version and --capabilities branches serve the pre-launch engine
	// gate: the mock must report a version at or above the floor and the
	// schema/resolver identity the test seeded into the processor, or
	// RunJob refuses to start it.
	script := "#!/bin/sh\ncase \"$*\" in *--version*) echo 'm4b-merge 2.2.0'; exit 0;; *--capabilities*) echo '{\"schema_version\":1,\"resolver_version\":\"1\",\"default_path_format\":\"{author}/{title}\",\"path_tokens\":[{\"name\":\"book_id\",\"value\":\"b\",\"example\":\"B\"},{\"name\":\"authors\",\"value\":\"a\",\"example\":\"A\"},{\"name\":\"narrators\",\"value\":\"n\",\"example\":\"N\"},{\"name\":\"title\",\"value\":\"t\",\"example\":\"T\"}],\"field_policies\":{\"values\":[\"write\",\"preserve\",\"clear\"],\"scalar_fields\":[\"title\",\"subtitle\",\"authors\",\"narrators\",\"series\",\"description\",\"genres\",\"year\"]},\"cover_policies\":{\"constraint\":\"str\",\"variants\":[\"write\",\"preserve\",\"clear\"]},\"chapter_policies\":{\"constraint\":\"str\",\"variants\":[\"write\",\"preserve\",\"clear\"]}}'; exit 0;; esac\nprintf 'partial live line\\n' 1>&2\nexec sleep 300 1>&- 2>&-\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write streaming sleeping binary: %v", err)
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
	seedSnapshot(t, h.svc.DB, 1)

	// Queue a job without starting workers, so it stays 'queued'.
	// Queueing intentionally leaves the book in its pre-queue state ('matched'):
	// the queued -> processing transition happens at worker start, so a queued
	// cancel needs no status rollback.
	id, err := h.svc.ProcessingSvc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
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

func TestCancelQueuedJobRestoresPreQueueBookStatus(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Reprocessed Book', 'done', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)
	id, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	if _, err := h.svc.DB.Exec("UPDATE books SET status = 'processing' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelJob(id); err != nil {
		t.Fatalf("cancel queued job: %v", err)
	}
	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status after queued cancellation = %q; want pre-queue done", got)
	}
}

func TestWorkerLosingQueuedClaimRestoresPreQueueBookStatus(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Reprocessed Book', 'done', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)
	id, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	state := svc.GetJob(id)
	if state == nil {
		t.Fatal("queued job has no state")
	}

	// Pin the cancel-vs-start loser state: cancellation has made the job
	// terminal after the book was temporarily marked processing.
	if _, err := h.svc.DB.Exec("UPDATE books SET status = 'processing' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DB.Exec("UPDATE processing_jobs SET status = 'cancelled' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	job := state.job
	svc.processJob(t.Context(), &job)

	if got := bookStatusFromDB(t, h.svc.DB, 1); got != "done" {
		t.Errorf("book status after worker loses queued claim = %q; want pre-queue done", got)
	}
	if got, err := jobStatusFromDB(t, h.svc.DB, id); err != nil || got != "cancelled" {
		t.Errorf("job status after worker loses queued claim = %q, err = %v; want cancelled", got, err)
	}
}

func TestCancelRunningJob(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	bin := writeSleepingBinary(t, t.TempDir())
	svc.proc = testProcessor(&audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}"})

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Running Book', 'matched', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{"/x/a.m4b"}, queueDestination{}, "")
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
	// The book restore follows the job's terminal write on the worker and
	// can trail it briefly while a write lock drains; poll with the same
	// window as the job status instead of reading once.
	finalBook := "processing"
	for time.Now().Before(deadline) {
		finalBook = bookStatusFromDB(t, h.svc.DB, 1)
		if finalBook == "matched" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalBook != "matched" {
		t.Errorf("final book status = %q; want matched", finalBook)
	}
}

func TestCancelRunningJobPersistsLiveOutput(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	bin := writeStreamingSleepBinary(t, t.TempDir())
	svc.proc = testProcessor(&audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}"})
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	waitJobStatus(t, h.svc.DB, id, "running")

	// Wait until the child's line reached the live buffer; cancelling before
	// stdout exists would test nothing.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := svc.GetJob(id); st != nil {
			if live, _ := st.outputSnapshot(); strings.Contains(live, "partial live line") {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	finalState := svc.GetJob(id)
	if finalState == nil {
		t.Fatal("live stdout never reached the job state before cancellation")
	}
	live, _ := finalState.outputSnapshot()
	if !strings.Contains(live, "partial live line") {
		t.Fatal("live stdout never reached the job state before cancellation")
	}

	w := doCancel(t, h, id)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	waitJobStatus(t, h.svc.DB, id, "cancelled")

	var output string
	if err := h.svc.DB.QueryRow("SELECT output FROM processing_jobs WHERE id = ?", id).Scan(&output); err != nil {
		t.Fatalf("cancelled output query: %v", err)
	}
	if !strings.Contains(output, "partial live line") {
		t.Errorf("cancelled output = %q; want the stdout produced before cancellation", output)
	}
}

// TestFinalizeCancelledExecErrorSkipsBookRestoreAndCleansUp pins the
// Exec-error branch of finalizeCancelled (regression): when the
// cancellation UPDATE fails, the returned sql.Result is nil, and the
// res.RowsAffected() guard MUST be skipped — calling it on a nil result
// panics. The UPDATE is made to fail deterministically by dropping the
// processing_jobs table (it then returns "no such table" while the books
// table stays readable and the DB stays open), and the job passed in has a
// valid BookID with a recorded pre-queue status, so a SUCCESSFUL
// finalizeCancelled WOULD have restored the book. The expected behavior is:
// log, skip book restoration (book stays 'processing'), continue cleanup
// (drop the in-memory job state), never panic.
func TestFinalizeCancelledExecErrorSkipsBookRestoreAndCleansUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path,
	))
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
	svc := NewProcessingService(db, testProcessor(&audio.Processor{
		OutputDir:  t.TempDir(),
		PathFormat: "{title}",
		LogLevel:   "info",
	}), 1)

	// Book in its pre-queue status: QueueJob records it as
	// book_pre_status, so a SUCCESSFUL finalizeCancelled WOULD have
	// restored the book from 'processing' back to 'matched' (book_id
	// valid, book_pre_status recorded).
	if _, err := db.Exec(
		"INSERT INTO books (id, title, status, status_message) VALUES (1, 'Running Book', 'matched', '')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, db, 1)

	id, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}

	// Simulate a worker that has started the job: the job is flipped to
	// 'running' so the cancellation UPDATE matches, and the book to
	// 'processing' so the success path's restore branch would flip it back
	// to 'matched' — making the skip on the Exec error observable.
	setJobStatus(t, db, id, "running")
	if _, err := db.Exec("UPDATE books SET status = 'processing' WHERE id = 1"); err != nil {
		t.Fatalf("set book to processing: %v", err)
	}

	// Dropping the table makes ONLY the cancellation UPDATE fail
	// deterministically ("no such table: processing_jobs") while the books
	// table stays readable for the post-call assertion. GetJob/forgetJob
	// are pure in-memory, so no worker goroutine is needed.
	if _, err := db.Exec("DROP TABLE processing_jobs"); err != nil {
		t.Fatalf("drop processing_jobs: %v", err)
	}

	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		svc.finalizeCancelled(&models.ProcessingJob{
			ID:            id,
			BookID:        sql.NullInt64{Int64: 1, Valid: true},
			BookPreStatus: sql.NullString{String: "matched", Valid: true},
		}, "")
	}()
	if panicked {
		t.Fatal("finalizeCancelled panicked on cancellation-UPDATE Exec failure; the res.RowsAffected() guard must be skipped when the Exec errored")
	}

	// The book row must be UNCHANGED: on the success path finalizeCancelled
	// would have restored it from 'processing' to 'matched', but the UPDATE
	// failed, so book restoration was skipped and it stays 'processing'.
	if got := bookStatusFromDB(t, db, 1); got != "processing" {
		t.Errorf("book status after Exec-failing cancel = %q; want processing (book must NOT be restored when the cancellation UPDATE fails)", got)
	}
	if st := svc.GetJob(id); st != nil {
		t.Error("job still present in service.jobs after finalizeCancelled; the in-memory state must be cleaned up even on Exec failure")
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
	seedSnapshot(t, h.svc.DB, 1)
	id, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
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
	var resp struct {
		IDs       []int64 `json:"ids"`
		Total     int     `json:"total"`
		Truncated bool    `json:"truncated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The F21 contract: the payload is capped at maxBookIDs, while total
	// reports the FULL match count and truncated makes the cut explicit.
	if len(resp.IDs) != maxBookIDs {
		t.Errorf("all_ids returned %d ids; want cap %d", len(resp.IDs), maxBookIDs)
	}
	if resp.Total != 1005 {
		t.Errorf("total = %d; want 1005 (the full match count)", resp.Total)
	}
	if !resp.Truncated {
		t.Error("truncated = false; want true (1005 > cap)")
	}
	// ids must be a valid non-empty subset of seeded IDs.
	for _, id := range resp.IDs {
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

// TestSweepOrphansRestoresPreQueueStatus pins the F28 cross-path contract:
// the orphan sweep restores a stuck book to the status it had when its
// (newest) job was queued — the same answer the cancel path gives — not a
// hard-coded 'pending'. A reprocessed 'done' book stays 'done'; a book that
// was 'matched' when queued goes back to 'matched'.
func TestSweepOrphansRestoresPreQueueStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sweep-pre.db")
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

	seed := `
		INSERT INTO books (id, title, status) VALUES
			(1, 'Reprocessed Book', 'processing'),
			(2, 'Matched Book', 'processing'),
			(3, 'Stale Book', 'processing');
		INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, book_pre_status, queue_seq) VALUES
			('d1', 1, '{}', 'done', 'matched', 1),
			('r1', 1, '{}', 'running', 'done', 2),
			('q1', 2, '{}', 'queued', 'matched', 3),
			('r2', 3, '{}', 'running', NULL, 4);
	`
	if _, err := db.Exec(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	NewProcessingService(db, &audio.Processor{}, 1)

	for id, want := range map[int64]string{
		1: "done",    // newest swept job (r1) recorded pre-queue 'done'
		2: "matched", // newest swept job (q1) recorded pre-queue 'matched'
		3: "pending", // NULL pre-status falls back to the historic 'pending'
	} {
		if got := bookStatusFromDB(t, db, id); got != want {
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

// decodeList decodes a ListJobsResponse from raw response bytes: the
// recorder's reader is consumed by the first decode, so the aggregate
// assertions decode the captured copy, not the recorder.
func decodeList(t *testing.T, raw []byte) ListJobsResponse {
	t.Helper()
	var resp ListJobsResponse
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp
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

// TestStreamJobProgressCloseEndsStream covers the row-16 fix: finalize closes
// the progress channel, so the SSE loop terminates on its own (and the
// trailing "done" line is written) without waiting for a client disconnect.
func TestStreamJobProgressCloseEndsStream(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
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

// streamRecorder is a concurrency-safe SSE sink: the stream handler writes
// from its goroutine while the test waits for a payload.
type streamRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	writes chan struct{}
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: make(http.Header), writes: make(chan struct{}, 1)}
}

func (w *streamRecorder) Header() http.Header { return w.header }
func (w *streamRecorder) WriteHeader(int)     {}
func (w *streamRecorder) Flush()              {}

func (w *streamRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.writes <- struct{}{}:
	default:
	}
	return n, err
}

func (w *streamRecorder) body() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *streamRecorder) waitFor(substr string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if strings.Contains(w.body(), substr) {
			return true
		}
		select {
		case <-w.writes:
		case <-deadline:
			return false
		}
	}
}

// newStreamRequest builds a GET /api/jobs/{id}/stream request with the chi
// URL param injected by hand (the tests drive handlers directly, not the
// router).
func newStreamRequest(t *testing.T, id string) *http.Request {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, id)
	req := httptest.NewRequest("GET", "/api/jobs/"+id+"/stream", nil)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestStreamJobStreamsLiveOutput covers the live log: stdout appended to the
// in-memory job state reaches an open SSE stream before the job ends, so the
// page shows progress while m4b-merge is still running. The stream carries
// the CONNECT offset in its initial payload and only the appended suffix in
// each output event (no full-log re-send per line).
func TestStreamJobStreamsLiveOutput(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	st := svc.GetJob(id)
	if st == nil {
		t.Fatal("no in-memory state for queued job")
	}

	// Log content that existed BEFORE this stream connected: it belongs to
	// this connection's initial snapshot, not to any suffix event.
	st.appendOutput("pre-existing line")

	w := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamJob(w, newStreamRequest(t, id))
	}()

	// Initial payload: full snapshot + the offset new lines start at
	// (18 bytes: "pre-existing line\n").
	if !w.waitFor(`"output_offset":18`, time.Second) {
		t.Fatalf("initial SSE payload missing its offset; body = %q", w.body())
	}
	if !strings.Contains(w.body(), `"output":"pre-existing line\n"`) {
		t.Fatalf("initial SSE payload missing the full snapshot; body = %q", w.body())
	}

	st.appendOutput("first line")
	st.appendOutput("second line")
	// Suffix only — the pre-existing line must NOT be re-sent. Back-to-back
	// appends are NOT guaranteed to coalesce into one suffix event (a
	// receiver can run between them under -race), so assert the terminal
	// cursor — 18 + 11 ("first line\n") + 12 ("second line\n") = 41 — and
	// each line's bytes independently, never a single-event shape.
	if !w.waitFor(`"output_offset":41`, 2*time.Second) {
		t.Fatalf("live output suffix missing its terminal offset; body = %q", w.body())
	}
	if !strings.Contains(w.body(), "first line") || !strings.Contains(w.body(), "second line") {
		t.Fatalf("live output lines missing from SSE; body = %q", w.body())
	}
	if strings.Contains(w.body(), `"output":"pre-existing line\nfirst line`) {
		t.Errorf("SSE re-sent the pre-existing snapshot bytes as a suffix; body = %q", w.body())
	}

	st.finalize(`{"status":"done","message":"processing complete"}`, nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after finalize")
	}
	if body := w.body(); !strings.Contains(body, `{"status":"done"}`) {
		t.Errorf("stream body = %q; want the trailing done line", body)
	}
}

// TestStreamJobFanOutToConcurrentTimeStreams covers the two-tabs bug: the
// single shared wake channel used to be consumed by one of N concurrent SSE
// connections, so the other tab saw no live output at all. With per-connection
// subscriptions every stream registered BEFORE the append must be woken.
func TestStreamJobFanOutToConcurrentTimeStreams(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	st := svc.GetJob(id)
	if st == nil {
		t.Fatal("no in-memory state for queued job")
	}

	wA := newStreamRecorder()
	wB := newStreamRecorder()
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() {
		defer close(doneA)
		h.StreamJob(wA, newStreamRequest(t, id))
	}()
	go func() {
		defer close(doneB)
		h.StreamJob(wB, newStreamRequest(t, id))
	}()

	// Both streams must be registered (initial payload flushed) before the
	// fan-out append races with registration.
	if !wA.waitFor(`"status":"queued"`, 2*time.Second) || !wB.waitFor(`"status":"queued"`, 2*time.Second) {
		t.Fatalf("both streams must connect; A = %q; B = %q", wA.body(), wB.body())
	}

	st.appendOutput("fan-out line")
	if !wA.waitFor(`"output":"fan-out line\n"`, 2*time.Second) {
		t.Fatalf("stream A missed the live output; body = %q", wA.body())
	}
	if !wB.waitFor(`"output":"fan-out line\n"`, 2*time.Second) {
		t.Fatalf("stream B missed the live output (the consumed-notify bug); body = %q", wB.body())
	}

	terminal := `data: {"status":"done"}`
	st.finalize(`{"status":"done"}`, nil)
	select {
	case <-doneA:
	case <-time.After(5 * time.Second):
		t.Fatal("stream A did not terminate after finalize")
	}
	select {
	case <-doneB:
	case <-time.After(5 * time.Second):
		t.Fatal("stream B did not terminate after finalize")
	}

	// The terminal PROGRESS line (not just the trailing status line from the
	// done await) must reach EVERY stream: a concurrent single reader
	// consumes the progress channel, so the loser of that race can only get
	// the line from the done-sync clone after the loop.
	for name, w := range map[string]*streamRecorder{"A": wA, "B": wB} {
		if n := strings.Count(w.body(), terminal); n < 2 {
			t.Errorf("stream %s saw the terminal event %d times; want the progress line AND the status line (body = %q)", name, n, w.body())
		}
	}
}

// TestStreamJobSlowReaderCannotStallAppend covers the drain invariant: a
// subscriber whose wake channel is already full (a reader that never drains
// it) must be DROPPED — not blocked — so m4b-merge's stdout drain (appendOutput)
// proceeds. One of the pair may then lack the coalesced event, but it is
// NEVER delayed by a sick peer, and appendOutput itself must not hang.
func TestStreamJobSlowReaderCannotStallAppend(t *testing.T) {
	st := &jobState{outputSubs: make(map[chan struct{}]struct{})}

	choked := make(chan struct{}, 1)
	choked <- struct{}{} // Reader never drains: the next wake must not block.
	healthy := make(chan struct{}, 1)
	defer st.registerOutputSubscriber(choked)()
	defer st.registerOutputSubscriber(healthy)()

	done := make(chan struct{})
	go func() {
		st.appendOutput("kept moving")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("appendOutput stalled on a slow subscriber; the stdout drain would hang")
	}

	// The healthy subscriber still woke. (The choked one keeps its pending
	// wake; the coalescing argument — each wake means "resend current state"
	// — makes that safe.)
	select {
	case <-healthy:
	case <-time.After(2 * time.Second):
		t.Fatal("healthy subscriber was not woken while a sick peer choked")
	}

	// The stroke of the drain invariant: the append itself landed in the
	// buffer regardless of the sick reader.
	live, offset := st.outputSnapshot()
	if live != "kept moving\n" || offset != 12 {
		t.Errorf("buffer = %q offset = %d; want the appended line", live, offset)
	}
}

// TestStreamJobReconnectCarriesFullSnapshot covers cancel/disconnect recovery:
// after bytes were appended and one stream already consumed them as a suffix,
// a newly opening stream must receive the FULL accumulated buffer (with the
// cursor offset), not just the delta.
func TestStreamJobReconnectCarriesFullSnapshot(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	st := svc.GetJob(id)
	if st == nil {
		t.Fatal("no in-memory state for queued job")
	}

	wFirst := newStreamRecorder()
	doneFirst := make(chan struct{})
	go func() {
		defer close(doneFirst)
		h.StreamJob(wFirst, newStreamRequest(t, id))
	}()
	if !wFirst.waitFor(`"status":"queued"`, time.Second) {
		t.Fatalf("initial SSE payload missing; body = %q", wFirst.body())
	}
	st.appendOutput("line one")
	st.appendOutput("line two")
	// Same coalescing caveat: the first stream received these via suffix
	// events, so assert the terminal cursor (9 + 9 = 18) and each line's
	// bytes — never that both lines share one event.
	if !wFirst.waitFor(`"output_offset":18`, 2*time.Second) {
		t.Fatalf("first stream missed its terminal offset; body = %q", wFirst.body())
	}
	if !strings.Contains(wFirst.body(), "line one") || !strings.Contains(wFirst.body(), "line two") {
		t.Fatalf("first stream missed the suffix lines; body = %q", wFirst.body())
	}

	// The second stream opens AFTER the bytes exist: its initial payload
	// must carry the whole log, exactly once, plus the buffer-end offset.
	wSecond := newStreamRecorder()
	doneSecond := make(chan struct{})
	go func() {
		defer close(doneSecond)
		h.StreamJob(wSecond, newStreamRequest(t, id))
	}()
	if !wSecond.waitFor(`"output":"line one\nline two\n"`, 2*time.Second) {
		t.Fatalf("reconnect stream missing the full snapshot; body = %q", wSecond.body())
	}
	if !wSecond.waitFor(`"output_offset":18`, 2*time.Second) {
		t.Fatalf("reconnect stream missing the cursor offset; body = %q", wSecond.body())
	}
	// The delta must NOT appear again as a suffix event on this connection.
	if strings.Count(wSecond.body(), "line one") != 1 {
		t.Errorf("reconnect body re-sent bytes; body = %q", wSecond.body())
	}

	st.finalize(`{"status":"done"}`, nil)
	select {
	case <-doneFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not terminate after finalize")
	}
	select {
	case <-doneSecond:
	case <-time.After(5 * time.Second):
		t.Fatal("second stream did not terminate after finalize")
	}
}

// TestStreamJobFinalDrainDeliversTrailingLine covers the post-loop drain: a
// wake for the LAST appended line may already be pending when the loop's
// select picks the closed progress channel and breaks — the buffer bytes
// appended after this stream's cursor must then be emitted by the drain
// before the terminal sequence, or the log is truncated mid-run. The
// append+finalize are fired back-to-back (no intervening wait) to maximize
// the window in which the wake is pending at loop exit, and the iteration
// repeats on fresh jobs; the assertion is invariant-based (exactly one copy,
// before the terminal line), so it holds whether or not the drain was the
// path that delivered the bytes.
func TestStreamJobFinalDrainDeliversTrailingLine(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	for i := range 20 {
		id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
		if err != nil {
			t.Fatalf("iteration %d: queue job: %v", i, err)
		}
		st := svc.GetJob(id)
		if st == nil {
			t.Fatalf("iteration %d: no in-memory state for queued job", i)
		}

		w := newStreamRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.StreamJob(w, newStreamRequest(t, id))
		}()

		// The stream is in its select loop only once the initial payload
		// (an empty fresh job carries the zero cursor) is out.
		if !w.waitFor(`"output_offset":0`, time.Second) {
			t.Fatalf("iteration %d: initial SSE payload missing; body = %q", i, w.body())
		}

		// No sleep between the two: a wake for the trailing line can be
		// pending at the instant the closed progress channel wins the select.
		st.appendOutput("trailing line")
		st.finalize(`{"status":"done"}`, nil)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: stream did not terminate after finalize", i)
		}

		body := w.body()
		if n := strings.Count(body, "trailing line"); n != 1 {
			t.Fatalf("iteration %d: trailing line present %d times; want exactly 1 (a missing drain truncates the log); body = %q", i, n, body)
		}
		terminal := `data: {"status":"done"}`
		if !strings.Contains(body, terminal) {
			t.Fatalf("iteration %d: terminal status line missing; body = %q", i, body)
		}
		if last := strings.LastIndex(body, "trailing line"); last >= strings.LastIndex(body, terminal) {
			t.Fatalf("iteration %d: trailing line landed at/after the terminal status line; body = %q", i, body)
		}
	}
}

// TestStreamJobLateSubscriberGetsTerminalResult covers the subscriber that
// opens AFTER finalize: the progress channel is already closed and the done
// sync already reported, so the stream must return immediately (not block on
// the closed channel's empty buffer) with the buffer snapshot, the terminal
// progress line, and the terminal status line.
func TestStreamJobLateSubscriberGetsTerminalResult(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	id, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue job: %v", err)
	}
	st := svc.GetJob(id)
	if st == nil {
		t.Fatal("no in-memory state for queued job")
	}
	st.appendOutput("last live line")
	st.finalize(`{"status":"error","message":"m4b-merge stopped"}`, errors.New("m4b-merge stopped"))

	w := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamJob(w, newStreamRequest(t, id))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("late stream did not return after finalize; body = %q", w.body())
	}

	body := w.body()
	if !strings.Contains(body, `"output":"last live line\n"`) {
		t.Errorf("late stream missing the buffer snapshot; body = %q", body)
	}
	if !strings.Contains(body, `data: {"status":"error","message":"m4b-merge stopped"}`) {
		t.Errorf("late stream missing the terminal PROGRESS line; body = %q", body)
	}
	if !strings.Contains(body, `data: {"message":"m4b-merge stopped","status":"error"}`) {
		t.Errorf("late stream missing the terminal STATUS line; body = %q", body)
	}
}

// TestDoneSyncAwaitAfterReport pins the broadcast contract: await() on an
// already-reported done sync returns the SAME (msg, err) to every reader —
// a terminal result must never be consumed by the first stream that awaits
// it, leaving a second concurrent stream without the terminal line.
func TestDoneSyncAwaitAfterReport(t *testing.T) {
	ds := newDoneSync()
	ds.report(`{"status":"done","message":"processing complete"}`, errors.New("worker stopped"))

	for i := range 3 {
		msg, err := ds.await()
		if msg != `{"status":"done","message":"processing complete"}` {
			t.Errorf("await %d: msg = %q; want the reported terminal message (broadcast, not consumed-once)", i, msg)
		}
		if err == nil || err.Error() != "worker stopped" {
			t.Errorf("await %d: err = %v; want the reported terminal error", i, err)
		}
	}
}

func TestJobStateSendProgressConcurrentWithFinalize(t *testing.T) {
	for range 100 {
		st := &jobState{
			progressSubs: make(map[chan string]struct{}),
			doneSync:     newDoneSync(),
		}
		ch := make(chan string, 1)
		st.registerProgressSubscriber(ch)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for range 10 {
				st.sendProgress(`{"status":"running"}`)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			st.finalize(cancelledProgressMsg, nil)
		}()
		close(start)
		wg.Wait()
	}
}

// TestJobStateDeregisterStopsWakes covers the deregistration: a subscriber
// whose slot was removed must not be woken by later appends (its goroutine
// is gone; waking it would be benign but leaks the wake pattern), while the
// remaining subscriber still is, and the map holds exactly the live slot.
func TestJobStateDeregisterStopsWakes(t *testing.T) {
	st := &jobState{outputSubs: make(map[chan struct{}]struct{})}

	dropped := make(chan struct{}, 1)
	kept := make(chan struct{}, 1)
	deregister := st.registerOutputSubscriber(dropped)
	defer st.registerOutputSubscriber(kept)()
	deregister()
	if len(st.outputSubs) != 1 {
		t.Fatalf("len(outputSubs) = %d after deregister; want 1", len(st.outputSubs))
	}

	st.appendOutput("wake line")

	select {
	case <-dropped:
		t.Fatal("deregistered subscriber was woken; its map slot must be gone")
	default:
	}
	select {
	case <-kept:
	case <-time.After(2 * time.Second):
		t.Fatal("live subscriber was not woken")
	}
}

// TestJobStateSubscriberChurnIsRaceFree hammer-tests the register/deregister
// pair against a concurrent appendOutput: the map must never be read after
// delete and must be empty once every churned subscriber has deregistered.
// Run under -race.
func TestJobStateSubscriberChurnIsRaceFree(t *testing.T) {
	st := &jobState{outputSubs: make(map[chan struct{}]struct{})}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				ch := make(chan struct{}, 1)
				st.registerOutputSubscriber(ch)()
			}
		}()
	}
	appendDone := make(chan struct{})
	go func() {
		defer close(appendDone)
		for i := 0; i < 200; i++ {
			st.appendOutput(fmt.Sprintf("churn line %d", i))
		}
	}()

	wg.Wait()
	<-appendDone
	if len(st.outputSubs) != 0 {
		t.Fatalf("len(outputSubs) = %d after all churn settled; want 0", len(st.outputSubs))
	}
}

// TestJobStateOutputFrom pins outputFrom's cursor arithmetic: a start at the
// beginning yields the whole buffer; a start AT the end and PAST the end
// both yield the empty suffix (the coalesced-wake safe case) with the
// buffer-end offset, and clamping must never grow the buffer.
func TestJobStateOutputFrom(t *testing.T) {
	st := &jobState{outputSubs: make(map[chan struct{}]struct{})}
	st.appendOutput("a")
	st.appendOutput("b") // "a\nb\n" — 4 bytes

	tests := []struct {
		name       string
		start      int
		wantSuffix string
	}{
		{"start at buffer beginning", 0, "a\nb\n"},
		{"start at buffer end", 4, ""},
		{"start past buffer end", 99, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			suffix, next := st.outputFrom(tc.start)
			if suffix != tc.wantSuffix || next != 4 {
				t.Errorf("outputFrom(%d) = (%q, %d); want (%q, 4)", tc.start, suffix, next, tc.wantSuffix)
			}
		})
	}

	// The clamped reads must have left the buffer untouched.
	live, offset := st.outputSnapshot()
	if live != "a\nb\n" || offset != 4 {
		t.Errorf("buffer after clamped reads = (%q, %d); want (\"a\\nb\\n\", 4) — outputFrom must not grow the buffer", live, offset)
	}
}

// TestJobsExposeDurableQueueOrder covers the tie-breaker for jobs queued in
// the same second: the DB sequence is monotonic and reaches the jobs API so
// a reload can rebuild FIFO order.
func TestJobsExposeDurableQueueOrder(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	first, err := svc.QueueJob(t.Context(), 0, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue first job: %v", err)
	}
	second, err := svc.QueueJob(t.Context(), 0, []string{"/x/b.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("queue second job: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/jobs?limit=10", nil)
	w := httptest.NewRecorder()
	h.ListJobs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list jobs status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []struct {
			ID         string `json:"id"`
			QueueOrder int64  `json:"queue_order"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	orders := make(map[string]int64, len(resp.Jobs))
	for _, job := range resp.Jobs {
		orders[job.ID] = job.QueueOrder
	}
	if orders[first] <= 0 || orders[second] <= 0 {
		t.Fatalf("queue_order missing: first=%d second=%d", orders[first], orders[second])
	}
	if orders[second] <= orders[first] {
		t.Errorf("queue_order first=%d second=%d; want second after first", orders[first], orders[second])
	}
}

// TestProcessRequestsCarryDurableBatchIdentity covers the completion
// banner's grouping contract: one POST /api/process request shares one
// batch id across every job it enqueues, and back-to-back requests stay
// contiguous in queue_order (MAX+1 per insert, rows never pruned) — so the
// batch id, not the adjacency, is what separates two user batches.
func TestProcessRequestsCarryDurableBatchIdentity(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	for id := int64(1); id <= 4; id++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO books (id, title, status) VALUES (?, 'Book', 'matched')", id); err != nil {
			t.Fatalf("seed book %d: %v", id, err)
		}
		seedSnapshot(t, h.svc.DB, id)
	}
	srcA := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcA, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed audio: %v", err)
	}
	srcB := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcB, "b.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed audio: %v", err)
	}

	post := func(bookIDs []int64, srcDirs []string) string {
		body, _ := json.Marshal(StartProcessingRequest{BookIDs: bookIDs, SrcDirs: srcDirs})
		req := httptest.NewRequest("POST", "/api/process", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.StartProcessing(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
		}
		var resp StartProcessingResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.BatchID == "" || len(resp.Jobs) != len(bookIDs) {
			t.Fatalf("response = %+v; want %d jobs and a batch id", resp, len(bookIDs))
		}
		return resp.BatchID
	}

	first := post([]int64{1, 2}, []string{srcA, srcB})
	second := post([]int64{3, 4}, []string{srcA, srcB})
	if first == second {
		t.Fatalf("two requests share batch id %q; want distinct ids", first)
	}

	var list struct {
		Jobs []struct {
			BookID     int64  `json:"book_id"`
			QueueOrder int64  `json:"queue_order"`
			BatchID    string `json:"batch_id"`
		} `json:"jobs"`
	}
	req := httptest.NewRequest("GET", "/api/jobs?limit=10", nil)
	w := httptest.NewRecorder()
	h.ListJobs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list jobs status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(list.Jobs) != 4 {
		t.Fatalf("jobs = %d; want 4", len(list.Jobs))
	}

	byOrder := make(map[int64]struct {
		bookID  int64
		batchID string
	}, len(list.Jobs))
	minOrder, maxOrder := int64(1<<62), int64(-1)
	for _, job := range list.Jobs {
		byOrder[job.QueueOrder] = struct {
			bookID  int64
			batchID string
		}{job.BookID, job.BatchID}
		if job.QueueOrder < minOrder {
			minOrder = job.QueueOrder
		}
		if job.QueueOrder > maxOrder {
			maxOrder = job.QueueOrder
		}
	}
	// The scenario the banner must survive: four contiguous queue orders
	// across two separate requests (min..max with no gap).
	if maxOrder-minOrder+1 != 4 {
		t.Fatalf("queue orders span %d..%d; want 4 contiguous values", minOrder, maxOrder)
	}
	for i := int64(0); i < 4; i++ {
		entry, ok := byOrder[minOrder+i]
		if !ok {
			t.Fatalf("missing queue order %d in %v", minOrder+i, byOrder)
		}
		wantBatch := first
		wantBooks := map[int64]bool{1: true, 2: true}
		if i >= 2 {
			wantBatch, wantBooks = second, map[int64]bool{3: true, 4: true}
		}
		if !wantBooks[entry.bookID] {
			t.Errorf("order %d: book %d; want one of %v", minOrder+i, entry.bookID, wantBooks)
		}
		if entry.batchID != wantBatch {
			t.Errorf("order %d: batch id = %q; want %q", minOrder+i, entry.batchID, wantBatch)
		}
	}

	// The single-job endpoint carries the id too (pollJob refreshes from it).
	var one struct {
		BatchID string `json:"batch_id"`
	}
	jobReq := httptest.NewRequest("GET", "/api/jobs?limit=1", nil)
	jobW := httptest.NewRecorder()
	h.ListJobs(jobW, jobReq)
	newest := list.Jobs[0]
	for _, job := range list.Jobs {
		if job.QueueOrder > newest.QueueOrder {
			newest = job
		}
	}
	singleID := newestBookID(h, newest.BookID, t)
	singleW := httptest.NewRecorder()
	h.GetJobStatus(singleW, chiJobRequest(t, singleID))
	if singleW.Code != http.StatusOK {
		t.Fatalf("get job status = %d — body: %s", singleW.Code, singleW.Body.String())
	}
	if err := json.Unmarshal(singleW.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	wantID := first
	if newest.BatchID == second {
		wantID = second
	}
	if one.BatchID != wantID {
		t.Errorf("single job batch id = %q; want %q", one.BatchID, wantID)
	}

	// Pre-batch rows (no batch id) omit the field: the web falls back to
	// the queue-order adjacency rule for those.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, detail, started_at, completed_at, queue_seq) VALUES ('legacy-1', 1, 'done', 'import', '/in/a.m4b', '2026-01-01 00:00:00', '2026-01-01 00:00:01', 0)",
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	var legacy struct {
		BatchID any `json:"batch_id"`
	}
	legacyW := httptest.NewRecorder()
	h.GetJobStatus(legacyW, chiJobRequest(t, "legacy-1"))
	if legacyW.Code != http.StatusOK {
		t.Fatalf("legacy job status = %d — body: %s", legacyW.Code, legacyW.Body.String())
	}
	if err := json.Unmarshal(legacyW.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("decode legacy job: %v", err)
	}
	if legacy.BatchID != nil {
		t.Errorf("legacy row batch id = %v; want the field absent", legacy.BatchID)
	}
}

// chiJobRequest builds a GET /api/jobs/{id} request with the chi route
// context the direct GetJobStatus test calls need (chiURLParam reads it).
func chiJobRequest(t *testing.T, jobID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/jobs/"+jobID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// newestBookID looks a book id up for the test; the jobs API exposes only
// ids, so the single-job probe needs the book's job id resolved from the DB.
func newestBookID(h *Handler, bookID int64, t *testing.T) string {
	t.Helper()
	var id string
	err := h.svc.DB.QueryRow(
		"SELECT id FROM processing_jobs WHERE book_id = ? AND batch_id IS NOT NULL ORDER BY queue_seq DESC LIMIT 1",
		bookID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("find newest job for book %d: %v", bookID, err)
	}
	return id
}

// TestQueueJobDuplicateRejected covers the server-side 409 guard.
func TestQueueJobDuplicateRejected(t *testing.T) {
	h, svc := newTestJobsHandler(t)

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)

	first, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("first queue: %v", err)
	}
	if _, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, ""); err == nil {
		t.Fatal("second queue for a book with a queued job: want error, got nil")
	}

	// A finished job frees the book for a new run.
	setJobStatus(t, h.svc.DB, first, "done")
	if _, err := svc.QueueJob(t.Context(), 1, []string{"/x/a.m4b"}, queueDestination{}, ""); err != nil {
		t.Fatalf("queue after terminal job: %v", err)
	}
}

// TestStartProcessingDuplicateBookRejected exercises the duplicate guard
// through the handler: a repeated book_id in one request body is refused in
// the preflight loop (400) BEFORE any insert, so a valid first occurrence is
// never left with a queued job and an unclosed batch.
func TestStartProcessingDuplicateBookRejected(t *testing.T) {
	h, _ := newTestJobsHandler(t)

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed audio: %v", err)
	}

	body, _ := json.Marshal(StartProcessingRequest{BookIDs: []int64{1, 1}, SrcDirs: []string{srcDir, srcDir}})
	req := httptest.NewRequest("POST", "/api/process", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.StartProcessing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate book status = %d; want 400 (preflight rejects before any insert) — body: %s", w.Code, w.Body.String())
	}
	var n int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 (the duplicate must be refused before any insert)", n)
	}
}

// writeMergingBinary writes an executable that mimics a successful
// machine-protocol m4b-merge run: it reads the --job file, publishes
// <root>/Author/Title.m4b directly under the job's output_root (the engine
// publishes in place, no relocation), and emits the single machine result
// document on stdout.
func writeMergingBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "merging-bin")
	script := `#!/usr/bin/env python3
import json, os, sys

if "--version" in sys.argv:
    print("m4b-merge 2.2.0")
    sys.exit(0)
if "--capabilities" in sys.argv:
    print(json.dumps({"schema_version": 1, "resolver_version": "1",
        "default_path_format": "{author}/{title}",
        "path_tokens": [{"name": k, "value": k, "example": k} for k in
            ("author", "authors", "narrator", "narrators", "title", "subtitle",
             "series_name", "series_position", "year", "genre",
             "metadata_id", "book_id")],
        "field_policies": {"values": ["write", "preserve", "clear"],
            "scalar_fields": ["title", "subtitle", "authors", "narrators",
                "series", "description", "genres", "year"]},
        "cover_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]},
        "chapter_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]}}))
    sys.exit(0)
job = None
for i, a in enumerate(sys.argv):
    if a == "--job" and i + 1 < len(sys.argv):
        job = sys.argv[i + 1]
with open(job) as f:
    jf = json.load(f)
root = jf["output_root"]
rel = "Author/Title.m4b"
os.makedirs(os.path.join(root, "Author"), exist_ok=True)
with open(os.path.join(root, rel), "w") as f:
    f.write("book")
sys.stderr.write("progress\n")
print(json.dumps({
    "schema_version": 1,
    "resolver_version": "1",
    "status": "succeeded",
    "groups": [{
        "group": "main",
        "outcome": "succeeded",
        "artifacts": [{"relative_path": rel, "kind": "audio", "exists": True}],
        "files_moved": True
    }]
}))
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write merging binary: %v", err)
	}
	return p
}

// writeDerivedTitleBinary writes a fake m4b-merge that honors -o and -p and
// derives the output file name from the FIRST -i source's basename, so
// same-batch jobs writing into one shared destination tree do not collide
// (each book lands at <root>/<scheme-with-{title}-filled>.m4b, distinct per
// source).
func writeDerivedTitleBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "derived-bin")
	script := `#!/usr/bin/env python3
import json, os, sys

if "--version" in sys.argv:
    print("m4b-merge 2.2.0")
    sys.exit(0)
if "--capabilities" in sys.argv:
    print(json.dumps({"schema_version": 1, "resolver_version": "1",
        "default_path_format": "{author}/{title}",
        "path_tokens": [{"name": k, "value": k, "example": k} for k in
            ("author", "authors", "narrator", "narrators", "title", "subtitle",
             "series_name", "series_position", "year", "genre",
             "metadata_id", "book_id")],
        "field_policies": {"values": ["write", "preserve", "clear"],
            "scalar_fields": ["title", "subtitle", "authors", "narrators",
                "series", "description", "genres", "year"]},
        "cover_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]},
        "chapter_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]}}))
    sys.exit(0)
job = None
for i, a in enumerate(sys.argv):
    if a == "--job" and i + 1 < len(sys.argv):
        job = sys.argv[i + 1]
with open(job) as f:
    jf = json.load(f)
root = jf["output_root"]
scheme = jf.get("path_format") or "{title}"
input_ = jf["inputs"][0]
# {title} derives from the source basename so same-batch jobs in one shared
# destination tree never collide; the remaining tokens come from the job's
# input_supplied metadata (the engine substitutes them).
meta = jf.get("input_supplied", {})
def tok(key):
    v = meta.get(key, "")
    if isinstance(v, list):
        return ", ".join(v)
    return "" if v is None else str(v)
tokens = {k: (tok(k) if k != "title" else os.path.basename(input_).rsplit(".", 1)[0])
          for k in ("title", "author", "authors", "narrator", "narrators",
                    "series_name", "series_position", "subtitle", "year",
                    "genre", "metadata_id", "book_id")}
import re
rel = re.sub(r"\{(\w+)\}", lambda m: tokens.get(m.group(1), ""), scheme)
# Empty token fields leave leading/duplicate slashes; normalize so the
# relative path stays under the root.
rel = re.sub(r"/+", "/", rel).strip("/")
os.makedirs(os.path.join(root, os.path.dirname(rel) or "."), exist_ok=True)
with open(os.path.join(root, rel + ".m4b"), "w") as f:
    f.write("book")
sys.stderr.write("progress\n")
print(json.dumps({
    "schema_version": 1,
    "resolver_version": "1",
    "status": "succeeded",
    "groups": [{
        "group": "main",
        "outcome": "succeeded",
        "artifacts": [{"relative_path": rel + ".m4b", "kind": "audio", "exists": True}],
        "files_moved": True
    }]
}))
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write derived-title binary: %v", err)
	}
	return p
}

// countM4Bs counts regular .m4b files anywhere under dir.
func countM4Bs(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".m4b") {
			n++
		}
		return nil
	})
	return n
}

// TestProcessJobRecordsOutputFormat is the regression test for #438: the run
// that discovers the produced .m4b knows its format, so the success write
// must persist it alongside dest_path. Without it the book page's FILE PATHS
// card falls back to "Unknown" forever.
func TestProcessJobRecordsOutputFormat(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	bin := writeMergingBinary(t, t.TempDir())
	svc.proc = testProcessor(&audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}", LogLevel: "info"})

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{filepath.Join(t.TempDir(), "a.m4b")}, queueDestination{}, "")
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
	bin := writeMergingBinary(t, t.TempDir())
	svc.proc = testProcessor(&audio.Processor{Binary: bin, OutputDir: t.TempDir(), PathFormat: "{title}", LogLevel: "info"})

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)

	ctx := t.Context()
	svc.RunWorkers(ctx, 1)

	id, err := svc.QueueJob(ctx, 1, []string{filepath.Join(t.TempDir(), "a.m4b")}, queueDestination{}, "")
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

// profileSuiteFixtures wires one test's shared destination roots, the temp
// YAML config (loaded through ConfigManager.LoadFrom and applied to the
// Processor from the SAME config), and its shaped profiles.
type profileSuiteFixtures struct {
	h        *Handler
	svc      *ProcessingService
	cm       *config.ConfigManager
	bin      string
	rootA    string // global output dir in the temp YAML config
	profileB int64  // directory-only profile pointing at root B
	profileG int64  // format-only profile "Genre/{title}"
}

// setupProfileSuite creates a jobs handler whose live config (manager AND
// processor) comes from one temp YAML: directories.output_dir = rootA,
// processing.path_format = {author}/{title}.
func setupProfileSuite(t *testing.T) profileSuiteFixtures {
	t.Helper()
	h, svc := newTestJobsHandler(t)
	bin := writeDerivedTitleBinary(t, t.TempDir())
	rootA := t.TempDir()
	yamlPath := filepath.Join(t.TempDir(), "config.yaml")
	yamlText := fmt.Sprintf(
		"m4b_merge:\n  binary: %q\ndirectories:\n  output_dir: %q\nprocessing:\n  path_format: \"{author}/{title}\"\n  num_cpus: 1\n  log_level: info\n", bin, rootA)
	if err := os.WriteFile(yamlPath, []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cm := config.NewConfigManager()
	if _, err := cm.LoadFrom(yamlPath); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	h.svc.Config = cm
	svc.proc = testProcessor(&audio.Processor{})
	svc.proc.ApplyConfig(cm.Config())

	// Book 1 -> source A (becomes "A"), book 2 -> source B (becomes "B").
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Alpha', 'matched'), (2, 'Beta', 'matched')",
	); err != nil {
		t.Fatalf("seed books: %v", err)
	}
	seedSnapshot(t, h.svc.DB, 1)
	seedSnapshot(t, h.svc.DB, 2)

	fx := profileSuiteFixtures{h: h, svc: svc, cm: cm, bin: bin, rootA: rootA}
	fx.profileB, fx.profileG = 0, 0
	if _, err := h.svc.DB.Exec(
		"INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES ('dir-b', ?, '')",
		t.TempDir(),
	); err != nil {
		t.Fatalf("seed profile B: %v", err)
	}
	var id1 int64
	if err := h.svc.DB.QueryRow("SELECT id FROM output_profiles WHERE name = 'dir-b'").Scan(&id1); err != nil {
		t.Fatalf("read profile B id: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES ('genre', '', 'Genre/{title}')",
	); err != nil {
		t.Fatalf("seed profile G: %v", err)
	}
	var id2 int64
	if err := h.svc.DB.QueryRow("SELECT id FROM output_profiles WHERE name = 'genre'").Scan(&id2); err != nil {
		t.Fatalf("read profile G id: %v", err)
	}
	fx.profileB, fx.profileG = id1, id2
	return fx
}

// reconfigureManager swaps the live YAML config (root + scheme) and applies
// it to the Processor — the same path UpdateSettings takes after a save.
func (fx profileSuiteFixtures) reconfigure(t *testing.T, newRoot, newScheme string) {
	fx.reconfigureBinary(t, fx.bin, newRoot, newScheme)
}

func (fx profileSuiteFixtures) reconfigureBinary(t *testing.T, newBin, newRoot, newScheme string) {
	t.Helper()
	yamlPath := filepath.Join(t.TempDir(), "config2.yaml")
	yamlText := fmt.Sprintf(
		"m4b_merge:\n  binary: %q\ndirectories:\n  output_dir: %q\nprocessing:\n  path_format: %q\n  num_cpus: 1\n  log_level: info\n", newBin, newRoot, newScheme)
	if err := os.WriteFile(yamlPath, []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if _, err := fx.cm.LoadFrom(yamlPath); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	fx.svc.proc.ApplyConfig(fx.cm.Config())
}

// postProcess drives POST /api/process; optional profileID nil = omitted.
func (fx profileSuiteFixtures) postProcess(t *testing.T, srcDirs []string, profileID *int64, bookIDs ...int64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(StartProcessingRequest{BookIDs: bookIDs, SrcDirs: srcDirs, OutputProfileID: profileID})
	req := httptest.NewRequest("POST", "/api/process", bytes.NewReader(body))
	w := httptest.NewRecorder()
	fx.h.StartProcessing(w, req)
	return w
}

func m4bsForBook(t *testing.T, h *Handler, bookID int64) string {
	t.Helper()
	var dest string
	if err := h.svc.DB.QueryRow("SELECT dest_path FROM books WHERE id = ?", bookID).Scan(&dest); err != nil {
		t.Fatalf("dest path query: %v", err)
	}
	return dest
}

// int64Ptr returns a pointer to v for optional request fields.
func int64Ptr(v int64) *int64 { return &v }

// TestQueueProfileDirOnlySnapshotsGlobalSchema: a directory-only profile for
// a two-book batch resolves the global scheme at queue time; both jobs store
// the same complete pair and land under the profile root with the inherited
// global format. A worker-time reconfigure (ApplyConfig after enqueue, via
// the workerStartHook) must NOT affect the already-snapshotted jobs; the
// same profile re-queued later picks up the new scheme, proving the
// reconfigure took effect and the earlier jobs were snapshot-bound.
func TestQueueProfileDirOnlySnapshotsGlobalSchema(t *testing.T) {
	fx := setupProfileSuite(t)
	rootB := t.TempDir()
	if _, err := fx.h.svc.DB.Exec(
		"UPDATE output_profiles SET output_dir = ? WHERE id = ?", rootB, fx.profileB,
	); err != nil {
		t.Fatalf("set profile B dir: %v", err)
	}
	// Each book gets its OWN source dir: jobs walk their paired dir for
	// inputs, so one shared dir would feed both jobs the same files and
	// derive the same title.
	srcA := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcA, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	srcB := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcB, "b.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// Reconfigure exactly when the worker starts its FIRST job.
	applied := false
	fx.svc.workerStartHook = func(string) {
		if !applied {
			applied = true
			fx.reconfigure(t, t.TempDir(), "{title}")
		}
	}
	fx.svc.RunWorkers(t.Context(), 1)

	w := fx.postProcess(t, []string{srcA, srcB}, int64Ptr(fx.profileB), 1, 2)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || len(resp.Jobs) != 2 {
		t.Fatalf("jobs = %d (err %v); want 2", len(resp.Jobs), err)
	}

	ids := []string{resp.Jobs[0]["id"].(string), resp.Jobs[1]["id"].(string)}
	for _, id := range ids {
		waitJobStatus(t, fx.h.svc.DB, id, "done")
	}

	// Both jobs must store the SAME complete queue-time pair.
	for i, id := range ids {
		var stored string
		if err := fx.h.svc.DB.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", id).
			Scan(&stored); err != nil {
			t.Fatalf("read job %d args: %v", i+1, err)
		}
		var a struct {
			OutputDir    string `json:"output_dir"`
			OutputScheme string `json:"output_scheme"`
		}
		if err := json.Unmarshal([]byte(stored), &a); err != nil {
			t.Fatalf("unmarshal job %d args: %v", i+1, err)
		}
		if a.OutputDir != rootB || a.OutputScheme != "{author}/{title}" {
			t.Errorf("job %d stored snapshot = (%q, %q); want (%q, %q)", i+1, a.OutputDir, a.OutputScheme, rootB, "{author}/{title}")
		}
	}

	// The fake merger derives {title} from each source basename and leaves
	// the other tokens empty, so the inherited {author}/{title} format
	// resolves to <profile root B>/a.m4b / b.m4b.
	for i, dest := range []string{
		filepath.Join(rootB, "a.m4b"),
		filepath.Join(rootB, "b.m4b"),
	} {
		got := m4bsForBook(t, fx.h, int64(i+1))
		if got != dest {
			t.Errorf("book %d dest = %q; want %q (snapshot root B, inherited {author}/{title})", i+1, got, dest)
		}
		if _, err := os.Stat(dest); err != nil {
			t.Errorf("output missing at %s: %v", dest, err)
		}
	}
	if n := countM4Bs(t, fx.rootA); n != 0 {
		t.Errorf("global root A got %d .m4b; want 0 (profile root wins)", n)
	}

	// The reconfigure (root C = tmp, {title}) now governs NEW queues: a
	// fresh profile-B batch lands under B with the NEW scheme.
	fx.svc.workerStartHook = nil
	src2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(src2, "c.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w = fx.postProcess(t, []string{src2}, int64Ptr(fx.profileB), 1)
	if w.Code != http.StatusOK {
		t.Fatalf("re-queue status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp2 struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp2); err != nil || len(resp2.Jobs) != 1 {
		t.Fatalf("re-queued jobs = %d (err %v); want 1", len(resp2.Jobs), err)
	}
	waitJobStatus(t, fx.h.svc.DB, resp2.Jobs[0]["id"].(string), "done")
	got := m4bsForBook(t, fx.h, 1)
	if want := filepath.Join(rootB, "c.m4b"); got != want {
		t.Errorf("requeued book dest = %q; want %q (profile root B, worker-time global scheme {title})", got, want)
	}
}

// TestQueueProfileFormatOnlySnapshotsGlobalDir: a format-only profile
// snapshots the global directory at queue time; outputs land under the
// global root A with the profile subpath even after the reconfigure changed
// the global directory.
func TestQueueProfileFormatOnlySnapshotsGlobalDir(t *testing.T) {
	fx := setupProfileSuite(t)

	fx.svc.workerStartHook = func(string) {
		fx.reconfigure(t, t.TempDir(), "{title}")
	}
	fx.svc.RunWorkers(t.Context(), 1)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w := fx.postProcess(t, []string{src}, int64Ptr(fx.profileG), 1)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || len(resp.Jobs) != 1 {
		t.Fatalf("jobs = %d (err %v); want 1", len(resp.Jobs), err)
	}
	waitJobStatus(t, fx.h.svc.DB, resp.Jobs[0]["id"].(string), "done")

	want := filepath.Join(fx.rootA, "Genre", "a.m4b")
	if got := m4bsForBook(t, fx.h, 1); got != want {
		t.Errorf("dest = %q; want %q (queue-time global dir A + profile Genre/{title})", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("output missing at %s: %v", want, err)
	}
}

// TestQueueUnprofiledUsesWorkerTimeGlobals: without a profile ID the
// destination follows the Processor's live values AT START TIME (no
// queue-time snapshot, no ConfigManager dependency). Queuing with globals
// (A, {author}/{title}) then reconfiguring to (C, {title}) before the worker
// runs must move the output to C.
// TestQueueUnprofiledFreezesGlobalsAtQueue: a plain (unprofiled, no-
// override) job must run on the globals FROZEN AT ENQUEUE TIME, not on a
// later worker-time reconfigure: the queue freezes the effective triple
// into the job args, and processJob consumes only the queued values.
func TestQueueUnprofiledFreezesGlobalsAtQueue(t *testing.T) {
	fx := setupProfileSuite(t)

	fx.svc.workerStartHook = func(string) {
		// A settings change AFTER the job was queued: it must not redirect
		// the frozen job.
		fx.reconfigure(t, t.TempDir(), "{author}/{title}")
	}
	fx.svc.RunWorkers(t.Context(), 1)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w := fx.postProcess(t, []string{src}, nil, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || len(resp.Jobs) != 1 {
		t.Fatalf("jobs = %d (err %v); want 1", len(resp.Jobs), err)
	}
	waitJobStatus(t, fx.h.svc.DB, resp.Jobs[0]["id"].(string), "done")

	var dest string
	if err := fx.h.svc.DB.QueryRow("SELECT dest_path FROM books WHERE id = ?", 1).Scan(&dest); err != nil {
		t.Fatalf("dest query: %v", err)
	}
	if !strings.HasSuffix(dest, "a.m4b") {
		t.Errorf("dest = %q; want it to end with a.m4b (queue-time {title})", dest)
	}
	if !strings.HasPrefix(dest, fx.rootA) {
		t.Errorf("dest = %q; MUST be under the queue-time global root %q (a post-queue reconfigure must not redirect it)", dest, fx.rootA)
	}
}

// TestQueueProfileEmptyEffectiveDir400: a directory-only profile against an
// empty global output dir (and no profile dir) must 400 before creating a
// single job — the resolver must not Abs("") into the working directory.
func TestQueueProfileEmptyEffectiveDir400(t *testing.T) {
	fx := setupProfileSuite(t)
	if _, err := fx.h.svc.DB.Exec("DELETE FROM output_profiles WHERE id = ?", fx.profileG); err != nil {
		t.Fatalf("delete profile G: %v", err)
	}
	// Reconfigure the global output dir to empty; the dir-b profile has no
	// dir of its own after we blank it too.
	reconfigureEmpty := func() {
		yamlPath := filepath.Join(t.TempDir(), "c3.yaml")
		if err := os.WriteFile(yamlPath, []byte("directories:\n  output_dir: ''\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := fx.cm.LoadFrom(yamlPath); err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		fx.svc.proc.ApplyConfig(fx.cm.Config())
	}
	reconfigureEmpty()
	if _, err := fx.h.svc.DB.Exec("UPDATE output_profiles SET output_dir = '' WHERE id = ?", fx.profileB); err != nil {
		t.Fatalf("blank profile B dir: %v", err)
	}

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w := fx.postProcess(t, []string{src}, int64Ptr(fx.profileB), 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
	var n int
	if err := fx.h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 (no Abs('') into cwd)", n)
	}
}

// TestQueueWriteAccessProbeRejectsBeforeQueueing: a stored destination the
// server can no longer create subdirectories in 400s the whole request with
// zero jobs — settings-save probing cannot cover a remount or uid change.
func TestQueueWriteAccessProbeRejectsBeforeQueueing(t *testing.T) {
	fx := setupProfileSuite(t)
	locked := readOnlyTestDir(t)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// Profile whose stored root became unwritable after it was saved.
	if _, err := fx.h.svc.DB.Exec("UPDATE output_profiles SET output_dir = ? WHERE id = ?",
		filepath.Join(locked, "books"), fx.profileB); err != nil {
		t.Fatalf("point profile at locked dir: %v", err)
	}
	w := fx.postProcess(t, []string{src}, int64Ptr(fx.profileB), 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("profile destination status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "output directory ") {
		t.Errorf("body = %q; want it to name the output directory setting", w.Body.String())
	}

	// Global completed directory became unwritable: the profile-less path
	// must fail the same way, before QueueJob.
	yamlPath := filepath.Join(t.TempDir(), "completed.yaml")
	yamlText := "directories:\n  output_dir: " + strconv.Quote(fx.rootA) +
		"\n  completed_dir: " + strconv.Quote(filepath.Join(locked, "completed")) + "\n"
	if err := os.WriteFile(yamlPath, []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := fx.cm.LoadFrom(yamlPath); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	fx.svc.proc.ApplyConfig(fx.cm.Config())
	w = fx.postProcess(t, []string{src}, nil, 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("completed dir status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "completed directory ") {
		t.Errorf("body = %q; want it to name the completed directory setting", w.Body.String())
	}

	var n int
	if err := fx.h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 for unwritable destinations", n)
	}
}

// TestQueueStaleProfileID404AndNoJobs: a request selecting a deleted (or
// never-extant) profile is a 404 with no jobs created.
func TestQueueStaleProfileID404AndNoJobs(t *testing.T) {
	fx := setupProfileSuite(t)
	if _, err := fx.h.svc.DB.Exec("DELETE FROM output_profiles WHERE id = ?", fx.profileB); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w := fx.postProcess(t, []string{src}, int64Ptr(fx.profileB), 1)
	if w.Code != http.StatusNotFound {
		t.Fatalf("stale profile status = %d; want 404 — body: %s", w.Code, w.Body.String())
	}
	var n int
	if err := fx.h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 for a stale profile", n)
	}
}

// TestQueueInvalidProfileID400: a malformed (non-positive) profile ID is a
// 400 with no jobs created; a config-unaware processor still serves
// unprofiled requests (no ConfigManager requirement on that path).
func TestQueueInvalidProfileID400(t *testing.T) {
	fx := setupProfileSuite(t)
	fx.h.svc.Config = nil // proves the no-profile path never touches the manager
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	w := fx.postProcess(t, []string{src}, int64Ptr(0), 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
	var n int
	if err := fx.h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 for an invalid id", n)
	}

	// The no-profile path still works without a ConfigManager.
	w = fx.postProcess(t, []string{src}, nil, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("unprofiled status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
}

// TestQueueSingleBookReprocessUsesGlobals (behavioral anchor for the book
// detail page's single-book reprocess): a /process request with one book and
// NO profile ID freezes the global destination at QUEUE time (the unprofiled
// job's globals inherit into its stored args) — the exact unprofiled
// contract the detail page relies on.
func TestQueueSingleBookReprocessUsesGlobals(t *testing.T) {
	fx := setupProfileSuite(t)
	fx.svc.RunWorkers(t.Context(), 1)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w := fx.postProcess(t, []string{src}, nil, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || len(resp.Jobs) != 1 {
		t.Fatalf("jobs = %d (err %v); want 1", len(resp.Jobs), err)
	}
	waitJobStatus(t, fx.h.svc.DB, resp.Jobs[0]["id"].(string), "done")
	// {author}/{title} with empty author -> <global root A>/a.m4b
	want := filepath.Join(fx.rootA, "a.m4b")
	if got := m4bsForBook(t, fx.h, 1); got != want {
		t.Errorf("single-book dest = %q; want %q (global root + global format)", got, want)
	}
	// Unprofiled jobs freeze the globals into their stored args at queue
	// time: the effective root + format must be present and match the live
	// globals, not omitted.
	var stored string
	if err := fx.h.svc.DB.QueryRow("SELECT m4b_merge_args FROM processing_jobs WHERE id = ?", resp.Jobs[0]["id"]).Scan(&stored); err != nil {
		t.Fatalf("read stored args: %v", err)
	}
	if !strings.Contains(stored, `"output_dir":"`+fx.rootA+`"`) {
		t.Errorf("stored args missing the frozen global output dir %q: %s", fx.rootA, stored)
	}
	if !strings.Contains(stored, `"output_scheme":"{author}/{title}"`) {
		t.Errorf("stored args missing the frozen global path format: %s", stored)
	}
}

// TestQueueResponseCarriesEffectiveDestination: the POST /process response
// reports the effective destination pair (absolute root + format) the batch
// was snapshotted under, so the web's freshly queued job shows the queue-time
// destination independent of any client-cached profile. A global-settings
// batch carries neither field.
func TestQueueResponseCarriesEffectiveDestination(t *testing.T) {
	fx := setupProfileSuite(t)
	rootB := t.TempDir()
	if _, err := fx.h.svc.DB.Exec(
		"UPDATE output_profiles SET output_dir = ? WHERE id = ?", rootB, fx.profileB,
	); err != nil {
		t.Fatalf("set profile B dir: %v", err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	w := fx.postProcess(t, []string{src}, int64Ptr(fx.profileB), 1)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp StartProcessingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || len(resp.Jobs) != 1 {
		t.Fatalf("decode response: %v (jobs %d)", err, len(resp.Jobs))
	}
	// Profile supplies the dir, the global config supplies the format.
	if resp.OutputDir != rootB || resp.OutputScheme != "{author}/{title}" {
		t.Errorf("response destination = (%q, %q); want (%q, %q)", resp.OutputDir, resp.OutputScheme, rootB, "{author}/{title}")
	}

	// Global-settings batch (no profile): both fields absent from the JSON.
	src2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(src2, "b.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	w = fx.postProcess(t, []string{src2}, nil, 2)
	if w.Code != http.StatusOK {
		t.Fatalf("process status = %d — body: %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["output_dir"]; ok {
		t.Errorf("global-settings batch response carries output_dir: %s", w.Body.String())
	}
	if _, ok := raw["output_scheme"]; ok {
		t.Errorf("global-settings batch response carries output_scheme: %s", w.Body.String())
	}
}

// Regression: the completion banner summarizes the LATEST batch, so its
// rollup must stay whole even when the batch is longer than the limit
// window (the cap truncates the recent list) or newer audit rows have
// pushed it out of the recent window entirely. The response carries the
// batch as a bounded aggregate (counts, wall clock, row ids) — not its
// full rows — and ?batch_id=ID pages the rows in queue order.
func TestListJobsServesLatestBatchAggregate(t *testing.T) {
	t.Run("batch longer than the limit window", func(t *testing.T) {
		h, _ := newTestJobsHandler(t)
		// 120 done jobs of one batch, queue_seq 1..120, created 00:00:00..
		// 01:59:00 so they are the NEWEST rows and also the newest batch.
		for i := 1; i <= 120; i++ {
			if _, err := h.svc.DB.Exec(
				"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id, batch_closed) VALUES (?, 1, 'done', 'merge', ?, ?, 'b1', 1)",
				fmt.Sprintf("b1-%03d", i), fmt.Sprintf("2026-01-01 %02d:%02d:00", (i-1)/60, (i-1)%60), i,
			); err != nil {
				t.Fatalf("seed batch job %d: %v", i, err)
			}
		}
		w := httptest.NewRecorder()
		h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=100", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
		}
		var resp ListJobsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// The recent window is capped at 100 rows: the batch's full rows
		// no longer ride along, so the list stays bounded.
		if n := len(resp.Jobs); n != 100 {
			t.Fatalf("list len = %d; want the 100-row cap, not the 120-job batch", n)
		}
		// The aggregate covers the WHOLE 120-job batch: counts, wall
		// clock, and every row id, in queue order.
		if resp.Batch == nil {
			t.Fatal("batch aggregate missing from response")
		}
		if resp.Batch.BatchID != "b1" || resp.Batch.Total != 120 || resp.Batch.Done != 120 {
			t.Fatalf("aggregate = %+v; want b1, total 120, done 120", resp.Batch)
		}
		if !resp.Batch.AllTerminal || resp.Batch.InFlight != 0 {
			t.Errorf("aggregate terminal = (%v, %d); want all terminal", resp.Batch.AllTerminal, resp.Batch.InFlight)
		}
		if resp.Batch.LastSeq != 120 {
			t.Errorf("aggregate last_seq = %d; want 120 (the batch's newest queue order)", resp.Batch.LastSeq)
		}
		if n := len(resp.Batch.JobIDs); n != 120 {
			t.Fatalf("aggregate job ids = %d; want all 120 row ids", n)
		}
		for i := 1; i <= 120; i++ {
			if want := fmt.Sprintf("b1-%03d", i); resp.Batch.JobIDs[i-1] != want {
				t.Fatalf("aggregate job id %d = %q; want %q (queue order)", i, resp.Batch.JobIDs[i-1], want)
			}
		}

		// The paged fetch serves the same batch in queue order, bounded:
		// page 1 is the first 16 rows, and the last page the tail.
		w = httptest.NewRecorder()
		h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?batch_id=b1&limit=16&offset=0", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("batch page status = %d; want 200 — body: %s", w.Code, w.Body.String())
		}
		var page ListJobsResponse
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		if page.Batch != nil {
			t.Errorf("batch page carries an aggregate; want rows only")
		}
		if n := len(page.Jobs); n != 16 {
			t.Fatalf("batch page len = %d; want 16", n)
		}
		if page.Jobs[0]["id"] != "b1-001" || page.Jobs[15]["id"] != "b1-016" {
			t.Errorf("batch page = (%v..%v); want b1-001..b1-016 in queue order", page.Jobs[0]["id"], page.Jobs[15]["id"])
		}
		w = httptest.NewRecorder()
		h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?batch_id=b1&limit=16&offset=112", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("batch tail page status = %d; want 200 — body: %s", w.Code, w.Body.String())
		}
		var tail ListJobsResponse
		if err := json.NewDecoder(w.Body).Decode(&tail); err != nil {
			t.Fatalf("decode tail: %v", err)
		}
		if n := len(tail.Jobs); n != 8 {
			t.Fatalf("batch tail len = %d; want the final 8 rows", n)
		}
		if tail.Jobs[0]["id"] != "b1-113" || tail.Jobs[7]["id"] != "b1-120" {
			t.Errorf("batch tail = (%v..%v); want b1-113..b1-120", tail.Jobs[0]["id"], tail.Jobs[7]["id"])
		}
	})

	t.Run("finished batch pushed out of the recent window by newer audit rows", func(t *testing.T) {
		h, _ := newTestJobsHandler(t)
		// A finished 3-job batch created on 01-01.
		for i := 1; i <= 3; i++ {
			if _, err := h.svc.DB.Exec(
				"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id, batch_closed) VALUES (?, 1, 'done', 'merge', '2026-01-01 00:00:00', ?, 'b1', 1)",
				fmt.Sprintf("b1-%d", i), i,
			); err != nil {
				t.Fatalf("seed batch job %d: %v", i, err)
			}
		}
		// 110 newer audit rows: the 100-row recent window no longer
		// reaches the batch, and nothing in the batch is active.
		seedAuditRows(t, h.svc.DB, 110)
		w := httptest.NewRecorder()
		h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=100", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
		}
		var resp ListJobsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// The recent window is 100 audit rows; the batch's rows are NOT
		// appended (the aggregate stands in for them).
		if n := len(resp.Jobs); n != 100 {
			t.Fatalf("list len = %d; want the 100-row recent window only", n)
		}
		for _, j := range resp.Jobs {
			if id, _ := j["id"].(string); strings.HasPrefix(id, "b1-") {
				t.Fatalf("batch row %s in the list; the aggregate, not the rows, rides along", id)
			}
		}
		// The aggregate still covers the whole batch: the banner keeps the
		// run even when no batch row is in the response.
		if resp.Batch == nil || resp.Batch.BatchID != "b1" || resp.Batch.Total != 3 || resp.Batch.Done != 3 {
			t.Fatalf("aggregate = %+v; want b1, total 3, done 3", resp.Batch)
		}
		if n := len(resp.Batch.JobIDs); n != 3 {
			t.Fatalf("aggregate job ids = %d; want 3", n)
		}
	})
}

// Regression: a LATER book's validation failure (here: a missing snapshot)
// must reject the WHOLE request before any insert, so a valid first book is
// not left with a queued job and an unclosed batch. The preflight loop
// validates every book before the enqueue loop runs (StartProcessing), so a
// mid-batch failure cannot hide an already-accepted job.
func TestStartProcessingPreflightRejectsWholeBatch(t *testing.T) {
	fx := setupProfileSuite(t)
	// Book 3 exists but has no captured snapshot: loadSnapshotForQueue
	// returns a nil capture, the cheapest deterministic per-book rejection.
	if _, err := fx.h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (3, 'Gamma', 'matched')",
	); err != nil {
		t.Fatalf("seed book 3: %v", err)
	}
	srcA := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcA, "a.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source A: %v", err)
	}
	srcB := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcB, "b.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed source B: %v", err)
	}
	// Book 1 is valid (snapshot seeded by the suite); book 3 is not.
	w := fx.postProcess(t, []string{srcA, srcB}, nil, 1, 3)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("process status = %d; want 500 (book 3 has no snapshot) — body: %s", w.Code, w.Body.String())
	}
	// The valid first book must NOT have been enqueued: the whole batch is
	// rejected, so the queue is empty and no batch was created.
	var n int
	if err := fx.h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Errorf("jobs created = %d; want 0 (the preflight rejection must leave no accepted job)", n)
	}
}

// Regression: the aggregate's count/rollup and its id list must come from
// one snapshot. StartProcessing enqueues a batch's jobs individually, so a
// read that straddles an in-flight enqueue (two separate queries) can count
// N rows and return N+1 (or N-1) ids, and the client pages the wrong number
// of rows. One read transaction makes total and job_ids agree.
func TestLatestBatchAggregateReadsOneSnapshot(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	// 24 rows on disk; a writer adds 16 more, like an in-flight enqueue.
	for i := 1; i <= 24; i++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES (?, 1, 'done', 'merge', ?, ?, 'b1')",
			fmt.Sprintf("b1-%03d", i), fmt.Sprintf("2026-01-01 %02d:%02d:00", (i-1)/60, (i-1)%60), i,
		); err != nil {
			t.Fatalf("seed batch job %d: %v", i, err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 25; i <= 40; i++ {
			if _, err := h.svc.DB.Exec(
				"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES (?, 1, 'done', 'merge', ?, ?, 'b1')",
				fmt.Sprintf("b1-%03d", i), fmt.Sprintf("2026-01-01 %02d:%02d:00", (i-1)/60, (i-1)%60), i,
			); err != nil {
				t.Errorf("writer insert %d: %v", i, err)
				return
			}
		}
	}()
	for i := range 25 {
		agg, err := h.latestBatchAggregate("b1", 0)
		if err != nil {
			t.Fatalf("aggregate read %d: %v", i, err)
		}
		if len(agg.JobIDs) != agg.Total {
			t.Fatalf("aggregate read %d: total = %d but job_ids has %d entries; the count and the id list must come from one snapshot", i, agg.Total, len(agg.JobIDs))
		}
	}
	<-done
	// After the writer stops, the aggregate covers the whole batch.
	agg, err := h.latestBatchAggregate("b1", 0)
	if err != nil {
		t.Fatalf("final aggregate: %v", err)
	}
	if agg.Total != 40 || len(agg.JobIDs) != 40 {
		t.Errorf("final aggregate = (total %d, ids %d); want 40 of each", agg.Total, len(agg.JobIDs))
	}
}

// Regression: with the pool closed, ListJobs 500s — the recent query
// fails before the handler ever reaches the aggregate. The aggregate's
// own contract (a read failure comes back as an error, not a silently
// overstated total) is asserted directly below.
func TestListJobsFailsWhenDBClosed(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES ('b1-1', 1, 'done', 'merge', '2026-01-01 00:00:00', 1, 'b1')",
	); err != nil {
		t.Fatalf("seed batch job: %v", err)
	}
	h.svc.DB.Close()

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=10", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("list over a closed db = %d; want 500 — body: %s", w.Code, w.Body.String())
	}

	// The aggregate's read failure must surface as an error (the id
	// scan must not be swallowed), so ListJobs' degrade branch can act
	// on it when the recent/active queries survive.
	agg, err := h.latestBatchAggregate("b1", 0)
	if err == nil {
		t.Fatalf("aggregate over a closed db = %+v, nil; want a non-nil error (the id read must not be swallowed)", agg)
	}
}

// Regression: latestBatchID's own contract — sql.ErrNoRows (no batched row)
// is the only non-error outcome; any other probe failure must surface as a
// real error so ListJobs can take its degrade branch (200, recent+active
// only) instead of 500ing.
func TestLatestBatchIDProbeFailureContract(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES ('b1-1', 1, 'done', 'merge', '2026-01-01 00:00:00', 1, 'b1')",
	); err != nil {
		t.Fatalf("seed batch job: %v", err)
	}

	// Healthy probe: newest batched row wins, book scope filters it.
	got, err := h.latestBatchID(0)
	if err != nil {
		t.Fatalf("healthy probe: %v", err)
	}
	if got != "b1" {
		t.Errorf("unscoped probe = %q; want b1", got)
	}
	if got, err = h.latestBatchID(1); err != nil || got != "b1" {
		t.Errorf("book-1 probe = (%q, %v); want (b1, nil)", got, err)
	}
	if got, err = h.latestBatchID(2); err != nil || got != "" {
		t.Errorf("book-2 probe = (%q, %v); want (\"\", nil) — no batched row for book 2", got, err)
	}

	// Closed DB: a non-ErrNoRows probe failure must come back as an error
	// (the degrade branch's precondition).
	h.svc.DB.Close()
	t.Cleanup(func() { t.Log("db closed by test") })
	if got, err := h.latestBatchID(0); err == nil {
		t.Errorf("closed-db probe = (%q, nil); want a non-nil error the handler can log and degrade on", got)
	} else if errors.Is(err, sql.ErrNoRows) {
		t.Errorf("closed-db probe error = %v; want a real fault, not ErrNoRows (which means 'no batch', not 'probe failed')", err)
	}
}

// Regression: when the probe finds no batch (or fails), ListJobs degrades
// to the pre-batch behavior — 200 with recent + active only and NO batch
// aggregate — and the client falls back to queue-order adjacency. A
// batched row the probe cannot see (queue_seq NULL) is the faithful
// fixture: the probe's filter excludes it, so the handler takes the
// no-aggregate branch while the row still rides along in the list.
func TestListJobsDegradesWithoutBatchOnProbeMiss(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	// A batched row the probe cannot see: batch_id set, queue_seq NULL.
	// The probe (batch_id IS NOT NULL AND queue_seq IS NOT NULL) misses it,
	// so the response must carry no aggregate — the old behavior.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, created_at, batch_id) VALUES ('b1-1', 1, 'done', 'merge', '2026-01-01 00:00:00', 'b1')",
	); err != nil {
		t.Fatalf("seed batch job: %v", err)
	}
	// Pre-batch history: no batch id at all.
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq) VALUES ('l1', 1, 'done', 'merge', '2025-12-31 00:00:00', 1)",
	); err != nil {
		t.Fatalf("seed legacy job: %v", err)
	}

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("degraded list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	raw := w.Body.Bytes()
	resp := decodeList(t, raw)
	// No aggregate: the probe saw no batch, so the banner falls back to
	// the adjacency walk over the rows the list carries.
	if resp.Batch != nil {
		t.Fatalf("degraded response carries aggregate %+v; want none", resp.Batch)
	}
	// The rows are still there (old behavior: the list is the source of
	// truth for the banner).
	if n := len(resp.Jobs); n != 2 {
		t.Fatalf("degraded list len = %d; want both rows", n)
	}

	// Control: the same row WITH a queue_seq is visible to the probe and
	// the aggregate appears.
	h2, _ := newTestJobsHandler(t)
	if _, err := h2.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES ('b1-1', 1, 'done', 'merge', '2026-01-01 00:00:00', 1, 'b1')",
	); err != nil {
		t.Fatalf("seed visible batch job: %v", err)
	}
	w = httptest.NewRecorder()
	h2.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("control list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	control := decodeList(t, w.Body.Bytes())
	if control.Batch == nil || control.Batch.BatchID != "b1" || control.Batch.Total != 1 {
		t.Fatalf("control aggregate = %+v; want b1 total 1", control.Batch)
	}
}

// Regression: the book_id scope must apply to the batch aggregate and the
// paged batch fetch as well as the recent and active queries — a scoped
// list must never surface another book's batch (its rollup or its rows).
func TestListJobsBookScopeAppliesToBatchAggregate(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book One', 'processing'), (2, 'Book Two', 'processing')",
	); err != nil {
		t.Fatalf("seed books: %v", err)
	}
	// Two finished 3-job batches created at the SAME second: created_at
	// cannot separate them, so the newest-batch probe resolves by
	// queue_seq. The book-2 batch has the HIGHER queue orders, so the
	// probe picks it even for the unscoped list — the book-1 scope must
	// then fall back to book 1's OWN batch id, not inherit the global one.
	for i := 1; i <= 3; i++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES (?, 1, 'done', 'merge', '2026-01-01 00:00:00', ?, 'b1')",
			fmt.Sprintf("b1-%d", i), i,
		); err != nil {
			t.Fatalf("seed book-1 batch %d: %v", i, err)
		}
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES (?, 2, 'done', 'merge', '2026-01-01 00:00:00', ?, 'b2')",
			fmt.Sprintf("b2-%d", i), 3+i,
		); err != nil {
			t.Fatalf("seed book-2 batch %d: %v", i, err)
		}
	}
	// 110 newer book-1 audit rows push book 1's batch out of the 100-row
	// recent window: the aggregate (and the paged fetch) are the ONLY
	// paths that re-include it.
	seedAuditRows(t, h.svc.DB, 110)

	// Unscoped: the probe's newest batch is b2 (queue_seq 4..6); the
	// aggregate carries b2, not b1. The list is the 100-row window only.
	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=100", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("unscoped list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	byID, ids := jobIDsFromList(t, w)
	if n := len(ids); n != 100 {
		t.Fatalf("unscoped list len = %d; want the 100-row recent window", n)
	}
	for i := 1; i <= 3; i++ {
		if _, ok := byID[fmt.Sprintf("b1-%d", i)]; ok {
			t.Errorf("unscoped list carries b1-%d; the batch's rows are not appended", i)
		}
	}

	// Book 1's scope: the probe must resolve book 1's OWN batch (b1) even
	// though b2 is the global newest, and the aggregate's AND book_id = ?
	// must keep book 2's rollup out.
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=100&book_id=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("book-1 list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	raw1 := w.Body.Bytes()
	byID, ids = jobIDsFromList(t, w)
	// 100-row recent window PLUS book 1's own latest-batch rows re-included
	// (b1's 3 rows sort out of the window behind the 110 audit rows; the
	// book page needs its book's full history in the list).
	if n := len(ids); n != 103 {
		t.Fatalf("book-1 list len = %d; want the 100-row recent window + 3 re-included b1 rows", n)
	}
	for id := range byID {
		if strings.HasPrefix(id, "b2-") {
			t.Errorf("book-2 batch row %s present in book-1 list", id)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, ok := byID[fmt.Sprintf("b1-%d", i)]; !ok {
			t.Errorf("book-1 list missing re-included batch row b1-%d", i)
		}
	}
	// Book 1's aggregate: b1, not the global-newest b2.
	book1Resp := decodeList(t, raw1)
	if book1Resp.Batch == nil || book1Resp.Batch.BatchID != "b1" || book1Resp.Batch.Total != 3 {
		t.Fatalf("book-1 aggregate = %+v; want b1 with total 3", book1Resp.Batch)
	}
	for i := 1; i <= 3; i++ {
		if want := fmt.Sprintf("b1-%d", i); book1Resp.Batch.JobIDs[i-1] != want {
			t.Errorf("book-1 aggregate job id %d = %q; want %q", i, book1Resp.Batch.JobIDs[i-1], want)
		}
	}

	// Mirror image: book 2's scope carries its own aggregate, and its 3
	// rows sit inside its (uncontested) recent window, so the list holds
	// them without an append.
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=100&book_id=2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("book-2 list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	raw2 := w.Body.Bytes()
	byID, ids = jobIDsFromList(t, w)
	if n := len(ids); n != 3 {
		t.Fatalf("book-2 list len = %d; want its 3 batch rows only", n)
	}
	for i := 1; i <= 3; i++ {
		if _, ok := byID[fmt.Sprintf("b2-%d", i)]; !ok {
			t.Errorf("book-2 list missing b2-%d", i)
		}
	}
	for id := range byID {
		if strings.HasPrefix(id, "b1-") {
			t.Errorf("book-1 batch row %s present in book-2 list", id)
		}
	}
	book2Resp := decodeList(t, raw2)
	if book2Resp.Batch == nil || book2Resp.Batch.BatchID != "b2" || book2Resp.Batch.Total != 3 {
		t.Fatalf("book-2 aggregate = %+v; want b2 with total 3", book2Resp.Batch)
	}

	// The paged batch fetch honors the book scope too: book 1's page of
	// b2 is empty, book 2's page carries its rows.
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?batch_id=b2&limit=100&book_id=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scoped batch page status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	scopedPage := decodeList(t, w.Body.Bytes())
	if n := len(scopedPage.Jobs); n != 0 {
		t.Errorf("book-1 scoped page of b2 = %d rows; want 0", n)
	}
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?batch_id=b2&limit=100&book_id=2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("book-2 batch page status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	ownPage := decodeList(t, w.Body.Bytes())
	if n := len(ownPage.Jobs); n != 3 {
		t.Fatalf("book-2 page of b2 = %d rows; want 3", n)
	}
	if ownPage.Jobs[0]["id"] != "b2-1" || ownPage.Jobs[2]["id"] != "b2-3" {
		t.Errorf("book-2 page = (%v..%v); want b2-1..b2-3 in queue order", ownPage.Jobs[0]["id"], ownPage.Jobs[2]["id"])
	}
}

// Regression: the book page renders /jobs?book_id=ID&limit=20 AS the book's
// full activity history (it has no on-demand batch fetch). Before the
// bounded aggregate, ListJobs appended the whole latest batch to the list;
// the aggregate replaced that for the queue page, so a book whose latest
// merge job is followed by >= limit of its own audit rows loses the
// terminal merge row from the recent window. The book-scoped list must
// re-include the latest batch's rows (bounded: only that batch, via the
// batch_id index), so the history stays whole.
func TestListJobsBookScopeReincludesLatestBatchRows(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'Book', 'done')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// The book's finished 2-job batch, created first (queue_seq 1..2).
	for i := 1; i <= 2; i++ {
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, created_at, queue_seq, batch_id) VALUES (?, 1, 'done', 'merge', '2026-01-01 00:00:00', ?, 'b1')",
			fmt.Sprintf("b1-%d", i), i,
		); err != nil {
			t.Fatalf("seed batch job %d: %v", i, err)
		}
	}
	// 25 newer audit rows of the SAME book: with limit=20 (the book page's
	// bound) the recent window holds only audit rows — the batch's rows
	// sort out of it entirely.
	for i := 1; i <= 25; i++ {
		mins := 10 * (i - 1)
		if _, err := h.svc.DB.Exec(
			"INSERT INTO processing_jobs (id, book_id, status, action, detail, created_at) VALUES (?, 1, 'done', 'import', ?, ?)",
			fmt.Sprintf("audit-%02d", i), fmt.Sprintf("/in/%02d.m4b", i), fmt.Sprintf("2026-01-02 %02d:%02d:00", 9+mins/60, mins%60),
		); err != nil {
			t.Fatalf("seed audit %d: %v", i, err)
		}
	}

	w := httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?book_id=1&limit=20", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	byID, ids := jobIDsFromList(t, w)
	// 20 recent audit rows + 2 re-included batch rows; nothing else.
	if n := len(ids); n != 22 {
		t.Fatalf("list len = %d; want 20 recent + 2 re-included batch rows", n)
	}
	for i := 1; i <= 2; i++ {
		if status, ok := byID[fmt.Sprintf("b1-%d", i)]; !ok || status != "done" {
			t.Errorf("terminal batch row b1-%d missing from book history (or wrong status %q)", i, status)
		}
	}
	// The re-included rows come after the window, deduped (no row appears
	// twice — the raw list, not the id map, exposes a duplicate).
	seen := make(map[string]int, len(ids))
	for _, id := range ids {
		seen[id]++
		if seen[id] > 1 {
			t.Errorf("duplicate row %s in list", id)
		}
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "b1-") {
			if id != ids[len(ids)-2] && id != ids[len(ids)-1] {
				t.Errorf("batch row %s not at the tail of the list: %v", id, ids)
			}
		}
	}

	// The unscoped list must NOT re-include: it pages batch rows on
	// demand via ?batch_id=, so its body stays the plain recent window.
	w = httptest.NewRecorder()
	h.ListJobs(w, httptest.NewRequest("GET", "/api/jobs?limit=20", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("unscoped list status = %d; want 200", w.Code)
	}
	byID, ids = jobIDsFromList(t, w)
	if n := len(ids); n != 20 {
		t.Fatalf("unscoped list len = %d; want the 20-row recent window", n)
	}
	for id := range byID {
		if strings.HasPrefix(id, "b1-") {
			t.Errorf("unscoped list carries re-included batch row %s", id)
		}
	}
}

// seedBatchBooks creates n books, each with a capture row and an on-disk
// source file, so QueueBatch's prepare pass (snapshot load + destination
// freeze) succeeds for every item.
func seedBatchBooks(t *testing.T, h *Handler, n int) []queueBatchItem {
	t.Helper()
	items := make([]queueBatchItem, 0, n)
	for i := range n {
		id := int64(i + 1)
		if _, err := h.svc.DB.Exec(
			`INSERT INTO books (id, title, status) VALUES (?, ?, 'pending')`, id, fmt.Sprintf("Book %d", id)); err != nil {
			t.Fatalf("seed book %d: %v", id, err)
		}
		seedSnapshot(t, h.svc.DB, id)
		src := filepath.Join(t.TempDir(), fmt.Sprintf("src-%d.m4b", i))
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		items = append(items, queueBatchItem{BookID: id, SrcPaths: []string{src}})
	}
	return items
}

func jobsMapSize(s *ProcessingService) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.jobs)
}

// TestQueueBatchMidInsertFailureRollsBackRowsAndStates pins the batch
// rollback contract: a mid-batch insert failure rejects the whole request,
// leaves NO committed rows (the transaction rolls back), and removes the
// in-memory job states that were registered before the commit (a worker
// never runs a job whose row was not committed, so the registrations would
// otherwise stay in the map for the process's lifetime).
func TestQueueBatchMidInsertFailureRollsBackRowsAndStates(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	items := seedBatchBooks(t, h, 3)
	if _, err := h.svc.DB.Exec(
		`CREATE TRIGGER fail_batch_insert AFTER INSERT ON processing_jobs
		 FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	ids, err := svc.QueueBatch(t.Context(), "batch-rb", queueDestination{}, svc.proc.QueueSnapshot(), items)
	if err == nil {
		t.Fatalf("QueueBatch succeeded; want the injected insert failure")
	}
	if ids != nil {
		t.Errorf("ids = %v; want nil on failure", ids)
	}
	var n int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("processing_jobs rows = %d; want 0 (the batch transaction must roll back)", n)
	}
	if got := jobsMapSize(svc); got != 0 {
		t.Errorf("service.jobs size = %d; want 0 (unregisterStates must remove the pre-committed registrations)", got)
	}
}

// TestQueueBatchRegistersStatesBeforeRowsClaimable pins the publication
// ordering: after QueueBatch returns, every queued job ID has a state in
// s.jobs (a row is claimable the instant the transaction commits, so the
// registration must already have landed under the service mutex), and a
// failing insert leaves NO registrations for that batch behind.
func TestQueueBatchRegistersStatesBeforeRowsClaimable(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	items := seedBatchBooks(t, h, 4)
	ids, err := svc.QueueBatch(t.Context(), "batch-order-1", queueDestination{}, svc.proc.QueueSnapshot(), items[:2])
	if err != nil {
		t.Fatalf("QueueBatch: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v; want 2", ids)
	}
	svc.mu.RLock()
	for _, id := range ids {
		if st := svc.jobs[id]; st == nil {
			svc.mu.RUnlock()
			t.Fatalf("queued job %s has no state in service.jobs; the registration must land before the commit makes the row claimable", id)
		}
	}
	svc.mu.RUnlock()

	// A second batch whose insert fails must not leave ITS registrations
	// in the map: the first batch's states remain, and nothing more.
	if _, err := h.svc.DB.Exec(
		`CREATE TRIGGER fail_batch_insert2 AFTER INSERT ON processing_jobs
		 FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.QueueBatch(t.Context(), "batch-order-2", queueDestination{}, svc.proc.QueueSnapshot(), items[2:4]); err == nil {
		t.Fatalf("second QueueBatch succeeded; want the injected insert failure")
	}
	if got := jobsMapSize(svc); got != 2 {
		t.Errorf("service.jobs size = %d; want 2 (the first batch's states only — the failed batch's registrations must be rolled back)", got)
	}
}
