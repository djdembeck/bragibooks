package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	done     chan error
	// jobCtx/cancel let the API kill the job's m4b-merge process mid-run.
	jobCtx context.Context
	cancel context.CancelFunc
	// finalized ensures the terminal progress/done signal is sent exactly once,
	// no matter how many components (cancel request, worker, timeout) finalize.
	finalized sync.Once
}

// NewProcessingService creates a ProcessingService backed by the given database
// and audio processor, with the specified number of concurrent workers.
func NewProcessingService(db *sql.DB, proc *audio.Processor, numWorkers int) *ProcessingService {
	return &ProcessingService{
		db:       db,
		proc:     proc,
		workers:  numWorkers,
		jobs:     make(map[string]*jobState),
		workerCh: make(chan *models.ProcessingJob, 100),
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
					// terminal status. Finalize so any open stream closes.
					if st := s.GetJob(job.ID); st != nil && st.jobCtx != nil && st.jobCtx.Err() != nil {
						st.finalize(cancelledProgressMsg, nil)
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

// QueueJob creates a processing job, persists it to the DB, and enqueues it.
func (s *ProcessingService) QueueJob(ctx context.Context, bookID int64, srcPaths []string) (string, error) {
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
		Status:       "queued",
		Error:        nil,
		OutputFile:   nil,
		StartedAt:    nil,
		CompletedAt:  nil,
	}

	// Insert into DB
	_, err = s.db.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES (?, ?, ?, ?)",
		job.ID, job.BookID.Int64, job.M4bMergeArgs, job.Status,
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

	// Mark the book before enqueueing so a fast worker cannot finish before
	// the request handler records the transitional state.
	if bookID > 0 {
		if _, err := s.db.Exec(
			"UPDATE books SET status = 'processing', status_message = '' WHERE id = ?",
			bookID,
		); err != nil {
			jobCancel() // the job will never run; release its context
			return "", fmt.Errorf("mark book processing: %w", err)
		}
	}

	s.workerCh <- job
	return id, nil
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

// finalize sends the terminal progress event and signals the done channel
// exactly once, so that the SSE stream closes and the done receiver unblocks
// regardless of which component (worker, timeout, or cancel request) won.
func (st *jobState) finalize(msg string, doneErr error) {
	st.finalized.Do(func() {
		select {
		case st.progress <- msg:
		default:
		}
		select {
		case st.done <- doneErr:
		default:
		}
	})
}

// CancelJob cancels a processing job and returns the job's book_id.
//
// Behavior by current DB status:
//   - queued:  the job is marked 'cancelled' in the DB immediately and the
//     book is reset to 'matched'; the worker will dequeue the job and skip it.
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
		if bookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'matched', status_message = '' WHERE id = ? AND status = 'processing'",
				bookID.Int64,
			)
		}
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
		return
	}

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
		select {
		case state.progress <- `{"status":"running","message":"started processing"}`:
		default:
		}
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
		return
	}

	result, err := s.proc.Run(runCtx, srcPaths, asin)
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
		return
	}

	outputFile := result.OutputFile
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'done', status_message = '', dest_path = ? WHERE id = ?",
			outputFile, job.BookID.Int64,
		)
	}
	s.db.Exec(
		"UPDATE processing_jobs SET status = ?, output = ?, output_file = ?, completed_at = datetime('now') WHERE id = ?",
		jobStatusDone, result.Stdout, outputFile, job.ID,
	)
	if state != nil {
		state.finalize(`{"status":"done","message":"processing complete"}`, nil)
	}
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
				if e.Type == "file" && isAudioFile(e.Name) {
					inputPaths = append(inputPaths, e.Path)
				}
			}
		} else if _, err := os.Stat(srcDir); err == nil && isAudioFile(srcDir) {
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
		if err != nil {
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

// ListJobs handles GET /api/jobs?limit=N&book_id=B.
// Returns recent jobs newest-first with clean JSON matching the single-job contract.
// When book_id is present and positive, only that book's jobs are returned.
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

	query := "SELECT id, book_id, status, output, error, output_file, started_at, completed_at, created_at FROM processing_jobs"
	args := make([]any, 0, 2)
	if bookID > 0 {
		query += " WHERE book_id = ?"
		args = append(args, bookID)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := h.svc.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query jobs: %v", err))
		return
	}
	defer rows.Close()

	jobs := make([]map[string]any, 0)
	for rows.Next() {
		var job models.ProcessingJob
		err := rows.Scan(&job.ID, &job.BookID, &job.Status, &job.Output,
			&job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt)
		if err != nil {
			continue
		}

		resp := map[string]any{
			"id":           job.ID,
			"book_id":      nil,
			"status":       job.Status,
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

		jobs = append(jobs, resp)
	}

	writeJSON(w, http.StatusOK, ListJobsResponse{Jobs: jobs})
}

// GetJobStatus handles GET /api/jobs/:id.
func (h *Handler) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chiURLParam(r, "id")

	row := h.svc.DB.QueryRow(
		"SELECT id, book_id, m4b_merge_args, status, output, error, output_file, started_at, completed_at, created_at FROM processing_jobs WHERE id = ?",
		jobID,
	)
	var job models.ProcessingJob
	err := row.Scan(&job.ID, &job.BookID, &job.M4bMergeArgs, &job.Status, &job.Output,
		&job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query job: %v", err))
		return
	}

	// Build clean JSON response (sql.NullXxx fields serialize as objects, not scalars)
	resp := map[string]any{
		"id":           job.ID,
		"status":       job.Status,
		"output":       job.Output,
		"error":        nil,
		"output_file":  nil,
		"started_at":   nil,
		"completed_at": nil,
		"created_at":   job.CreatedAt,
	}
	if job.BookID.Valid {
		resp["book_id"] = job.BookID.Int64
	} else {
		resp["book_id"] = nil
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

	writeJSON(w, http.StatusOK, resp)
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

	// Send initial state from DB
	row := h.svc.DB.QueryRow(
		"SELECT status, output, error, output_file, started_at, completed_at FROM processing_jobs WHERE id = ?",
		jobID,
	)
	var status, output, outputFile string
	var errStr, startedAt, completedAt string
	err := row.Scan(&status, &output, &errStr, &outputFile, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		fmt.Fprintf(w, "data: %s\n\n", json.RawMessage(`{"status":"error","message":"job not found"}`))
		flusher.Flush()
		return
	}
	if err != nil {
		return
	}

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
	case <-time.After(60 * time.Second):
		// Force close if no done signal
	}
}

// isAudioFile checks if a filename has an audio file extension.
func isAudioFile(name string) bool {
	ext := strings.ToLower(name)
	for _, audioExt := range []string{".mp3", ".m4a", ".m4b", ".mp4", ".aac", ".ogg", ".flac", ".wma"} {
		if strings.HasSuffix(ext, audioExt) {
			return true
		}
	}
	return false
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

// Ensure io import is used
var _ = io.EOF
