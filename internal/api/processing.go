package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// ProcessingService manages background audiobook processing jobs.
type ProcessingService struct {
	db       *sql.DB
	proc     *audio.Processor
	workers  int
	jobs     map[string]*jobState
	mu       sync.RWMutex
	workerCh chan *models.ProcessingJob
	wg       sync.WaitGroup
}

type jobState struct {
	job      models.ProcessingJob
	progress chan string
	// progressMu guards every progress send (including the one in
	// finalize) so a send can never race the channel close in finalize.
	progressMu sync.Mutex
	// closed marks that finalize has closed the progress channel.
	closed bool
	done   chan error
	// jobCtx/cancel let the API kill the job's m4b-merge process mid-run.
	jobCtx context.Context
	cancel context.CancelFunc
	// finalized ensures the terminal progress/done signal is sent exactly once,
	// no matter how many components (cancel request, worker, timeout) finalize.
	finalized sync.Once
}

// NewProcessingService creates a ProcessingService backed by the given database
// and audio processor, with the specified number of concurrent workers.
// On startup it sweeps jobs left active by a previous run: they are marked
// error and their books reset to 'pending' so they can be re-queued.
func NewProcessingService(db *sql.DB, proc *audio.Processor, numWorkers int) *ProcessingService {
	s := &ProcessingService{
		db:       db,
		proc:     proc,
		workers:  numWorkers,
		jobs:     make(map[string]*jobState),
		workerCh: make(chan *models.ProcessingJob, 100),
	}
	s.sweepOrphans()
	return s
}

// sweepOrphans marks jobs that were queued/running when the service last
// exited as error, and resets their stuck 'processing' books to 'pending'.
func (s *ProcessingService) sweepOrphans() {
	res, err := s.db.Exec(
		"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE status IN (?, ?)",
		jobStatusError, "orphaned: service restarted while job was active", jobStatusQueued, jobStatusRunning,
	)
	if err != nil {
		log.Printf("orphan job sweep: %v", err)
		return
	}
	n, _ := res.RowsAffected()
	// Book ids of the swept jobs, so only their stuck books are reset.
	var bookIDs []int64
	rows, err := s.db.Query(
		"SELECT book_id FROM processing_jobs WHERE status = ? AND book_id IS NOT NULL AND error = ?",
		jobStatusError, "orphaned: service restarted while job was active",
	)
	if err == nil {
		for rows.Next() {
			var bid int64
			if rows.Scan(&bid) == nil {
				bookIDs = append(bookIDs, bid)
			}
		}
		rows.Close()
	}
	for _, bid := range bookIDs {
		s.db.Exec("UPDATE books SET status = 'pending', status_message = '' WHERE id = ? AND status = 'processing'", bid)
	}
	if n > 0 {
		log.Printf("orphan job sweep: marked %d job(s) as error, reset %d stuck book(s) to pending", n, len(bookIDs))
	}
}

// RunWorkers starts the worker goroutines that consume jobs from the channel.
// numWorkers < 1 (the "use all cores" convention for m4b-merge) would
// otherwise spawn no workers and leave every job queued forever, so it is
// clamped to at least one.
func (s *ProcessingService) RunWorkers(ctx context.Context, numWorkers int) {
	if numWorkers < 1 {
		numWorkers = 1
	}
	for range numWorkers {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case job, ok := <-s.workerCh:
					if !ok {
						return
					}
					// Skip jobs cancelled while still queued: the per-job
					// context is already done and the cancel request set the
					// terminal status in the DB. Finalize so any open stream
					// closes, then drop the in-memory state (terminal jobs are
					// readable from the DB).
					if st := s.GetJob(job.ID); st != nil && st.jobCtx != nil && st.jobCtx.Err() != nil {
						st.finalize(cancelledProgressMsg, nil)
						s.forgetJob(job.ID)
						continue
					}
					s.processJob(ctx, job)
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

// errActiveJobForBook is returned when the book already has a queued or
// running job, so the client gets a 409 instead of a duplicate process.
var errActiveJobForBook = errors.New("book already has an active job")

// QueueJob creates a processing job, persists it to the DB, and enqueues it.
func (s *ProcessingService) QueueJob(ctx context.Context, bookID int64, srcPaths []string) (string, error) {
	if bookID > 0 && s.hasActiveJobForBook(bookID) {
		return "", errActiveJobForBook
	}

	id := uuid.New().String()

	// Serialize src paths as JSON for storage
	argsJSON, err := json.Marshal(map[string]any{
		"src_paths": srcPaths,
	})
	if err != nil {
		return "", fmt.Errorf("serialize job args: %w", err)
	}

	job := &models.ProcessingJob{
		ID:           id,
		BookID:       sql.NullInt64{Int64: bookID, Valid: true},
		M4bMergeArgs: string(argsJSON),
		Action:       "merge",
		Status:       "queued",
		Error:        nil,
		OutputFile:   nil,
		StartedAt:    nil,
		CompletedAt:  nil,
	}

	// Insert into DB
	_, err = s.db.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action) VALUES (?, ?, ?, ?, ?)",
		job.ID, job.BookID.Int64, job.M4bMergeArgs, job.Status, job.Action,
	)
	if err != nil {
		return "", fmt.Errorf("insert processing job: %w", err)
	}

	// The per-job context is cancelable from the API (POST /jobs/:id/cancel)
	// so a running m4b-merge process can be killed mid-flight. It is derived
	// from Background, not the request context, because the job outlives the
	// request that queued it.
	jobCtx, jobCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.jobs[id] = &jobState{
		job:      *job,
		progress: make(chan string, 10),
		done:     make(chan error, 1),
		jobCtx:   jobCtx,
		cancel:   jobCancel,
	}
	s.mu.Unlock()

	// The book is intentionally NOT flipped to 'processing' here: the
	// transition happens when a worker starts the job (processJob). A queued
	// job is a promise, not work — cancelling it then requires no book
	// status rollback, and the UI never shows a book as 'processing' while
	// the job is still queued. (Pre-queued status 'matched'/'pending' is
	// preserved as-is.)
	s.workerCh <- job
	return id, nil
}

// RecordBookAction writes a terminal-only audit row for a non-processing book
// action (import, metadata update). status is always jobStatusDone: the row is
// history, not a worker job, so it never touches queuing (hasActiveJobForBook
// counts only queued/running) and never the orphan sweep (which updates only
// queued/running). m4b_merge_args stays NULL — the column holds serialized
// CLI args ({"src_paths": [...]}) that processJob decodes, and audit rows are
// not worker jobs; the detail (import path, book title) lives in the dedicated
// detail column instead. Best-effort: a write failure is logged ("record book
// action") — a stuck audit row must not sink the book operation it decorates,
// matching the existing best-effort auxiliary writes (attachSourceCover); the
// failure stays operator-visible via the missing history row plus the log
// line.
func (s *ProcessingService) RecordBookAction(bookID int64, action, detail string) {
	if s == nil {
		return
	}
	_, err := s.db.Exec(
		`INSERT INTO processing_jobs (id, book_id, status, action, detail, started_at, completed_at)
		 VALUES (?, ?, 'done', ?, ?, datetime('now'), datetime('now'))`,
		uuid.New().String(), bookID, action, detail,
	)
	if err != nil {
		log.Printf("record book action: insert: %v", err)
	}
}

// hasActiveJobForBook reports whether the book already has a job that has
// not finished, guarding against duplicate queued runs of the same source.
func (s *ProcessingService) hasActiveJobForBook(bookID int64) bool {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM processing_jobs WHERE book_id = ? AND status IN (?, ?)",
		bookID, jobStatusQueued, jobStatusRunning,
	).Scan(&n)
	return err == nil && n > 0
}

// forgetJob removes a job's in-memory state under the service mutex. Terminal
// job state remains readable from the DB (ListJobs/GetJobStatus read SQL), so
// dropping the map entry frees memory without losing information.
func (s *ProcessingService) forgetJob(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

// GetJob returns the in-memory state for a job, or nil if not found.
func (s *ProcessingService) GetJob(jobID string) *jobState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[jobID]
}

// Terminal job statuses. Cancelled is a first-class terminal state: the DB may
// hold it, and ListJobs/GetJobStatus pass it through as-is.
const (
	jobStatusQueued    = "queued"
	jobStatusRunning   = "running"
	jobStatusDone      = "done"
	jobStatusError     = "error"
	jobStatusCancelled = "cancelled"
)

var errJobNotFound = errors.New("job not found")

var errJobTerminal = errors.New("job already finished")

const cancelledProgressMsg = `{"status":"cancelled","message":"job cancelled"}`

// sendProgress delivers a progress event without ever panicking on a closed
// channel: after finalize runs, late sends (e.g. a worker reaching the
// "running" step milliseconds after a queued cancel) must be dropped.
func (st *jobState) sendProgress(msg string) {
	st.progressMu.Lock()
	defer st.progressMu.Unlock()
	if st.closed {
		return
	}
	select {
	case st.progress <- msg:
	default:
	}
}

// finalize sends the terminal progress event, closes the progress channel,
// and signals the done channel — exactly once, so the SSE stream detects
// termination (a closed progress channel ends its loop; without the close it
// would keep heartbeating after the terminal event) and the done receiver
// unblocks, regardless of which component (worker, timeout, or cancel
// request) won.
func (st *jobState) finalize(msg string, doneErr error) {
	st.finalized.Do(func() {
		st.progressMu.Lock()
		if !st.closed {
			select {
			case st.progress <- msg:
			default:
			}
			close(st.progress)
			st.closed = true
		}
		st.progressMu.Unlock()
		select {
		case st.done <- doneErr:
		default:
		}
	})
}

// CancelJob cancels a processing job and returns the job's book_id.
//
// Behavior by current DB status:
//   - queued:  the job is marked 'cancelled' in the DB immediately; the
//     book is left untouched because queueing no longer flips it to
//     'processing' (that transition happens at worker start, so a queued
//     job's book is still in its pre-queue state — 'matched'/'pending').
//     The worker will dequeue the job and skip it.
//   - running: the per-job context is cancelled so m4b-merge is killed via its
//     context; the worker's finalizeCancelled then writes the terminal
//     'cancelled' status and resets the book. CancelJob does not touch the
//     book itself here, so a job that finishes successfully in the race window
//     ends up consistently 'done'.
//   - done/error/cancelled: errJobTerminal is returned.
func (s *ProcessingService) CancelJob(jobID string) (sql.NullInt64, error) {
	var status string
	var bookID sql.NullInt64
	err := s.db.QueryRow(
		"SELECT status, book_id FROM processing_jobs WHERE id = ?", jobID,
	).Scan(&status, &bookID)
	if err == sql.ErrNoRows {
		return sql.NullInt64{}, errJobNotFound
	}
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("query job for cancel: %w", err)
	}
	if status != jobStatusQueued && status != jobStatusRunning {
		return sql.NullInt64{}, errJobTerminal
	}

	state := s.GetJob(jobID)
	if state != nil && state.cancel != nil {
		state.cancel()
	}

	if status == jobStatusQueued {
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusCancelled, jobID,
		)
		// Close any open progress stream; the worker's skip path finalizes
		// idempotently via sync.Once.
		if state != nil {
			state.finalize(cancelledProgressMsg, nil)
		}
	} else if status == jobStatusRunning && state == nil {
		// Running job with no in-memory state (the process restarted while
		// the job was running): no worker owns it, so nobody would observe
		// the cancelled context. Persist the terminal state directly.
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusCancelled, "cancelled by user", jobID,
		)
		if bookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'matched', status_message = '' WHERE id = ? AND status = 'processing'",
				bookID.Int64,
			)
		}
	}
	return bookID, nil
}

func (s *ProcessingService) processJob(ctx context.Context, job *models.ProcessingJob) {
	state := s.GetJob(job.ID)

	// The per-job context (created in QueueJob) is the cancel surface: the
	// API's CancelJob cancels it to kill m4b-merge via its exec ctx. A
	// 4-hour timeout is layered on top so a stuck process still terminates.
	var runCtx context.Context
	var runCancel context.CancelFunc
	if state != nil && state.jobCtx != nil {
		runCtx, runCancel = context.WithTimeout(state.jobCtx, 4*time.Hour)
		defer runCancel()
	} else {
		runCtx = ctx
	}

	if state != nil && state.jobCtx != nil && state.jobCtx.Err() != nil {
		// Cancelled between dequeue and here; the cancel request already set
		// the terminal status.
		state.finalize(cancelledProgressMsg, nil)
		s.forgetJob(job.ID)
		return
	}

	// The queued -> processing book transition (and job -> running) happens
	// HERE, at worker start, not in QueueJob: keeping the book in its
	// pre-queue state ('matched'/'pending') while the job waits in the queue
	// is what lets a queued cancel need no status rollback.
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'processing', status_message = '' WHERE id = ?",
			job.BookID.Int64,
		)
	}

	// Only a queued job may transition to running: a queued job cancelled
	// by CancelJob is already 'cancelled' and must not be overwritten.
	s.db.Exec(
		"UPDATE processing_jobs SET status = ?, started_at = datetime('now') WHERE id = ? AND status = ?",
		jobStatusRunning, job.ID, jobStatusQueued,
	)

	if state != nil {
		state.sendProgress(`{"status":"running","message":"started processing"}`)
	}

	// Get ASIN from the matched book
	var asin string
	if job.BookID.Valid {
		var bookAsin sql.NullString
		row := s.db.QueryRow("SELECT asin FROM books WHERE id = ?", job.BookID.Int64)
		if err := row.Scan(&bookAsin); err == nil && bookAsin.Valid {
			asin = bookAsin.String
		}
	}

	// Parse source paths from stored args
	var args map[string]any
	var srcPaths []string
	var argsErr error
	if err := json.Unmarshal([]byte(job.M4bMergeArgs), &args); err != nil {
		argsErr = fmt.Errorf("failed to parse args")
	} else if paths, ok := args["src_paths"].([]interface{}); !ok {
		argsErr = fmt.Errorf("invalid args: no src_paths")
	} else {
		srcPaths = make([]string, len(paths))
		for i, p := range paths {
			if s, ok := p.(string); ok {
				srcPaths[i] = s
			}
		}
	}

	if argsErr != nil {
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				argsErr.Error(), job.BookID.Int64,
			)
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, argsErr.Error(), job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(argsErr.Error())+`"}`, argsErr)
		}
		s.forgetJob(job.ID)
		return
	}

	result, err := s.proc.Run(runCtx, job.ID, srcPaths, asin)
	if err != nil {
		// A cancellation during the run kills m4b-merge via its context;
		// that is 'cancelled', not 'error'.
		if state != nil && state.jobCtx != nil && state.jobCtx.Err() != nil {
			s.finalizeCancelled(job)
			return
		}
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				err.Error(), job.BookID.Int64,
			)
		}
		stdout := ""
		if result != nil {
			stdout = result.Stdout
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, output = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, err.Error(), stdout, job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(err.Error())+`"}`, err)
		}
		s.forgetJob(job.ID)
		return
	}

	outputFile := result.OutputFile
	if job.BookID.Valid {
		// An empty discovered output is a warning, not a failure: the book
		// stays 'done' and the warning is recorded in its status message.
		msg := ""
		if outputFile == "" && result.OutputWarning != "" {
			msg = result.OutputWarning
		}
		s.db.Exec(
			"UPDATE books SET status = 'done', status_message = ?, dest_path = ?, format_type = ? WHERE id = ?",
			msg, outputFile, outputFormat(outputFile), job.BookID.Int64,
		)
	}
	output := result.Stdout
	if outputFile == "" && result.OutputWarning != "" {
		output = strings.TrimRight(output, "\n") + "\n" + result.OutputWarning
	}
	s.db.Exec(
		"UPDATE processing_jobs SET status = ?, output = ?, output_file = ?, completed_at = datetime('now') WHERE id = ?",
		jobStatusDone, output, outputFile, job.ID,
	)
	if state != nil {
		state.finalize(`{"status":"done","message":"processing complete"}`, nil)
	}
	s.forgetJob(job.ID)
}

// finalizeCancelled marks a running job cancelled in the DB after its
// m4b-merge process was killed via the job context, resets the book to
// 'matched' (only if it is still 'processing'), and emits the terminal
// progress signal. The queued-cancel path resets the book in CancelJob
// instead; this is the running path.
func (s *ProcessingService) finalizeCancelled(job *models.ProcessingJob) {
	var stdout string
	if job.BookID.Valid {
		var out sql.NullString
		if err := s.db.QueryRow("SELECT output FROM processing_jobs WHERE id = ?", job.ID).Scan(&out); err == nil {
			stdout = out.String
		}
	}
	s.db.Exec(
		"UPDATE processing_jobs SET status = ?, error = ?, output = ?, completed_at = datetime('now') WHERE id = ?",
		jobStatusCancelled, "cancelled by user", stdout, job.ID,
	)
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'matched', status_message = '' WHERE id = ? AND status = 'processing'",
			job.BookID.Int64,
		)
	}
	if state := s.GetJob(job.ID); state != nil {
		state.finalize(cancelledProgressMsg, nil)
	}
	s.forgetJob(job.ID)
}

// StartProcessingRequest is the JSON body for POST /api/process.
type StartProcessingRequest struct {
	BookIDs []int64  `json:"book_ids"`
	SrcDirs []string `json:"src_dirs"`
}

// ProcessingJobCreated is returned in the response of StartProcessing.
type ProcessingJobCreated struct {
	ID     string `json:"id"`
	BookID int64  `json:"book_id"`
}

// StartProcessing handles POST /api/process.
// Accepts {"book_ids": [1,2], "src_dirs": ["/input/dir1", "/input/dir2"]}.
// Each pair creates a ProcessingJob. Returns {"jobs": [{"id": "uuid", "book_id": 1}, ...]}.
func (h *Handler) StartProcessing(w http.ResponseWriter, r *http.Request) {
	var req StartProcessingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.BookIDs) == 0 {
		writeError(w, http.StatusBadRequest, "book_ids is required")
		return
	}
	if len(req.SrcDirs) == 0 {
		writeError(w, http.StatusBadRequest, "src_dirs is required")
		return
	}

	jobs := make([]ProcessingJobCreated, 0)
	for i, bookID := range req.BookIDs {
		// Use the corresponding src_dir (or wrap around if fewer dirs than books)
		srcDir := req.SrcDirs[i%len(req.SrcDirs)]

		// Collect input file paths: a source is either a directory of
		// audio files (the folder route) or a bare audio file itself.
		var inputPaths []string
		if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
			entries, err := readDirectory(srcDir)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot read src_dir %s: %v", srcDir, err))
				return
			}
			for _, e := range entries {
				if e.Type == "file" && audio.IsAudioFile(e.Name) {
					inputPaths = append(inputPaths, e.Path)
				}
			}
		} else if _, err := os.Stat(srcDir); err == nil && audio.IsAudioFile(srcDir) {
			inputPaths = append(inputPaths, srcDir)
		} else {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot read src_dir %s: %v", srcDir, err))
			return
		}

		if len(inputPaths) == 0 {
			log.Printf("no audio files found in %s for book %d", srcDir, bookID)
			continue
		}

		jobID, err := h.svc.ProcessingSvc.QueueJob(r.Context(), bookID, inputPaths)
		switch {
		case errors.Is(err, errActiveJobForBook):
			// The book already has a queued/running job: re-queuing would
			// double-process the same source, so refuse.
			writeError(w, http.StatusConflict, fmt.Sprintf("book %d already has an active processing job", bookID))
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("queue job: %v", err))
			return
		}

		jobs = append(jobs, ProcessingJobCreated{
			ID:     jobID,
			BookID: bookID,
		})
	}

	writeJSON(w, http.StatusOK, map[string][]ProcessingJobCreated{"jobs": jobs})
}

// ListJobsResponse is the JSON shape returned by GET /api/jobs.
type ListJobsResponse struct {
	Jobs []map[string]any `json:"jobs"`
}

// jobResp converts a jobs-table row to its clean JSON shape, shared by the
// list and active endpoints so the two payloads stay identical. sql.Null* is
// what the columns carry, hence the per-field nil/unwrapping.
func jobResp(job models.ProcessingJob) map[string]any {
	resp := map[string]any{
		"id":           job.ID,
		"book_id":      nil,
		"status":       job.Status,
		"action":       job.Action,
		"detail":       nil,
		"output":       job.Output,
		"error":        nil,
		"output_file":  nil,
		"started_at":   nil,
		"completed_at": nil,
		"created_at":   job.CreatedAt,
	}
	if job.BookID.Valid {
		resp["book_id"] = job.BookID.Int64
	}
	if job.Detail != nil {
		resp["detail"] = *job.Detail
	}
	if job.Error != nil {
		resp["error"] = *job.Error
	}
	if job.OutputFile != nil {
		resp["output_file"] = *job.OutputFile
	}
	if job.StartedAt != nil {
		resp["started_at"] = *job.StartedAt
	}
	if job.CompletedAt != nil {
		resp["completed_at"] = *job.CompletedAt
	}
	return resp
}

const jobsColumns = "id, book_id, status, action, detail, output, error, output_file, started_at, completed_at, created_at"

// ListJobs handles GET /api/jobs?limit=N&book_id=B.
// Returns recent jobs newest-first with clean JSON matching the single-job contract.
// When book_id is present and positive, only that book's jobs are returned.
// Active (queued/running) jobs are appended after the capped recent history:
// a batch of audit rows (imports, metadata saves) must not push a running
// merge — the state the page's live counters and cancel action rely on — out
// of the limit window (the queue page derives its active set from this list).
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	limit := parseIntQueryParam(r, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var bookID int64
	if v, ok := r.URL.Query()["book_id"]; ok && len(v) > 0 {
		// Non-numeric values are treated as absent for tolerance.
		bookID, _ = strconv.ParseInt(v[0], 10, 64)
	}

	recentQuery := "SELECT " + jobsColumns + " FROM processing_jobs"
	recentArgs := make([]any, 0, 3)
	if bookID > 0 {
		recentQuery += " WHERE book_id = ?"
		recentArgs = append(recentArgs, bookID)
	}
	recentQuery += " ORDER BY created_at DESC LIMIT ?"
	recentArgs = append(recentArgs, limit)
	recent, err := h.queryJobs(recentQuery, recentArgs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query jobs: %v", err))
		return
	}

	// The active set honors the same book scope as the history, so a
	// book-scoped page only surfaces that book's live work.
	activeQuery := "SELECT " + jobsColumns + " FROM processing_jobs"
	activeArgs := make([]any, 0, 3)
	if bookID > 0 {
		activeQuery += " WHERE book_id = ? AND status IN (?, ?)"
		activeArgs = append(activeArgs, bookID)
	} else {
		activeQuery += " WHERE status IN (?, ?)"
	}
	activeQuery += " ORDER BY started_at DESC"
	activeArgs = append(activeArgs, jobStatusQueued, jobStatusRunning)
	active, err := h.queryJobs(activeQuery, activeArgs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query active jobs: %v", err))
		return
	}

	response := make([]map[string]any, 0, len(recent)+len(active))
	seen := make(map[string]bool, len(recent)+len(active))
	for _, job := range recent {
		seen[job.ID] = true
		response = append(response, jobResp(job))
	}
	for _, job := range active {
		if seen[job.ID] {
			continue
		}
		response = append(response, jobResp(job))
	}

	writeJSON(w, http.StatusOK, ListJobsResponse{Jobs: response})
}

// queryJobs runs a jobs SELECT over the shared column list, scanning every
// row with sql.Null*-safe destinations (active jobs carry several NULLs a
// plain string scan would reject).
func (h *Handler) queryJobs(query string, args []any) ([]models.ProcessingJob, error) {
	rows, err := h.svc.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]models.ProcessingJob, 0, 10)
	for rows.Next() {
		var job models.ProcessingJob
		err := rows.Scan(&job.ID, &job.BookID, &job.Status, &job.Action, &job.Detail,
			&job.Output, &job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt)
		if err != nil {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetJobStatus handles GET /api/jobs/:id.
func (h *Handler) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chiURLParam(r, "id")

	row := h.svc.DB.QueryRow(
		"SELECT id, book_id, m4b_merge_args, status, action, detail, output, error, output_file, started_at, completed_at, created_at FROM processing_jobs WHERE id = ?",
		jobID,
	)
	var job models.ProcessingJob
	var jobArgs sql.NullString // audit rows have no m4b-merge args (details live in the detail column)
	err := row.Scan(&job.ID, &job.BookID, &jobArgs, &job.Status, &job.Action, &job.Detail,
		&job.Output, &job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query job: %v", err))
		return
	}
	// Clean JSON shared with the list endpoints (jobResp unwraps the
	// sql.NullXxx fields that would serialize as objects).
	writeJSON(w, http.StatusOK, jobResp(job))
}

// StreamJob handles GET /api/jobs/:id/stream (SSE endpoint).
// Streams processing progress as Server-Sent Events.
func (h *Handler) StreamJob(w http.ResponseWriter, r *http.Request) {
	jobID := chiURLParam(r, "id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial state from DB. Nullable columns scan into NullString:
	// active jobs have NULL output_file/started_at/completed_at, which a
	// plain string scan rejects (silently killing the stream previously).
	row := h.svc.DB.QueryRow(
		"SELECT status, output, error, output_file, started_at, completed_at FROM processing_jobs WHERE id = ?",
		jobID,
	)
	var status, output string
	var dbErr, dbOutputFile sql.NullString
	var dbStartedAt, dbCompletedAt sql.NullString
	if err := row.Scan(&status, &output, &dbErr, &dbOutputFile, &dbStartedAt, &dbCompletedAt); err != nil {
		if err == sql.ErrNoRows {
			fmt.Fprintf(w, "data: %s\n\n", json.RawMessage(`{"status":"error","message":"job not found"}`))
			flusher.Flush()
			return
		}
		log.Printf("stream job %s initial state: %v", jobID, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query job: %v", err))
		return
	}
	errStr := dbErr.String
	outputFile := dbOutputFile.String

	// Check if already done
	if status == "done" || status == "error" || status == "cancelled" {
		msg := map[string]string{
			"status":  status,
			"message": "job finished",
		}
		if status == "error" {
			msg["error"] = errStr
		}
		if status == "done" {
			msg["output_file"] = outputFile
		}
		b, _ := json.Marshal(msg)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		return
	}

	// Get the in-memory job state and stream progress
	state := h.svc.ProcessingSvc.GetJob(jobID)
	if state == nil {
		// No in-memory state; stream DB state
		fmt.Fprintf(w, "data: %s\n\n", json.RawMessage(`{"status":"`+status+`"}`))
		flusher.Flush()
		return
	}

	// Stream progress
	ctx := r.Context()
	done := false
	for !done {
		select {
		case msg, ok := <-state.progress:
			if !ok {
				done = true
				break
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-time.After(30 * time.Second):
			// Periodic heartbeat
			fmt.Fprintf(w, "data: %s\n\n", `{"status":"alive"}`)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}

	// Wait for done signal
	select {
	case err := <-state.done:
		if err != nil {
			b, _ := json.Marshal(map[string]string{"status": "error", "message": escapeJSON(err.Error())})
			fmt.Fprintf(w, "data: %s\n\n", b)
		} else {
			fmt.Fprintf(w, "data: %s\n\n", `{"status":"done"}`)
		}
		flusher.Flush()
	case <-time.After(10 * time.Second):
		// Force close if no done signal
	}
}

// outputFormat returns the container format of a produced output file, e.g.
// "m4b", for the book's FILE PATHS card. Output discovery only publishes
// .m4b files (findNewestM4B), so the extension is the real format rather
// than a guess from arbitrary user input.
func outputFormat(path string) string {
	if path == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// escapeJSON replaces characters that would break inline JSON construction.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\\n`)
	s = strings.ReplaceAll(s, "\r", `\\r`)
	return s
}

// CancelJob handles POST /api/jobs/{id}/cancel.
//
//   - 200 {"id": "...", "book_id": N|null, "status": "cancelled"} on success
//   - 404 {"error": "job not found"} for an unknown id
//   - 409 {"error": "job already finished"} for a terminal job
func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := chiURLParam(r, "id")

	bookID, err := h.svc.ProcessingSvc.CancelJob(jobID)
	switch {
	case errors.Is(err, errJobNotFound):
		writeError(w, http.StatusNotFound, "job not found")
		return
	case errors.Is(err, errJobTerminal):
		writeError(w, http.StatusConflict, "job already finished")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancel job: %v", err))
		return
	}

	resp := map[string]any{
		"id":      jobID,
		"book_id": nil,
		"status":  jobStatusCancelled,
	}
	if bookID.Valid {
		resp["book_id"] = bookID.Int64
	}
	writeJSON(w, http.StatusOK, resp)
}
