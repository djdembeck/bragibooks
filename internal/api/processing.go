package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
func (s *ProcessingService) RunWorkers(ctx context.Context, numWorkers int) {
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

	s.mu.Lock()
	s.jobs[id] = &jobState{
		job:      *job,
		progress: make(chan string, 10),
		done:     make(chan error, 1),
	}
	s.mu.Unlock()

	// Mark the book before enqueueing so a fast worker cannot finish before
	// the request handler records the transitional state.
	if bookID > 0 {
		if _, err := s.db.Exec(
			"UPDATE books SET status = 'processing', status_message = '' WHERE id = ?",
			bookID,
		); err != nil {
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

func (s *ProcessingService) processJob(ctx context.Context, job *models.ProcessingJob) {
	// Apply a 4-hour per-job timeout
	ctx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	defer cancel()
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'processing', status_message = '' WHERE id = ?",
			job.BookID.Int64,
		)
	}

	// Update status to 'running'
	s.db.Exec(
		"UPDATE processing_jobs SET status = ?, started_at = datetime('now') WHERE id = ?",
		"running", job.ID,
	)

	state := s.GetJob(job.ID)
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
	if err := json.Unmarshal([]byte(job.M4bMergeArgs), &args); err == nil {
		if paths, ok := args["src_paths"].([]interface{}); ok {
			srcPaths := make([]string, len(paths))
			for i, p := range paths {
				if s, ok := p.(string); ok {
					srcPaths[i] = s
				}
			}

			result, err := s.proc.Run(ctx, srcPaths, asin)
			if err != nil {
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
					"error", err.Error(), stdout, job.ID,
				)
				if state != nil {
					select {
					case state.progress <- `{"status":"error","message":"` + escapeJSON(err.Error()) + `"}`:
					default:
					}
					state.done <- err
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
				"done", result.Stdout, outputFile, job.ID,
			)
			if state != nil {
				select {
				case state.progress <- `{"status":"done","message":"processing complete"}`:
				default:
				}
				state.done <- nil
			}
		} else {
			if job.BookID.Valid {
				s.db.Exec(
					"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
					"invalid args: no src_paths", job.BookID.Int64,
				)
			}
			s.db.Exec(
				"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
				"error", "invalid args: no src_paths", job.ID,
			)
			if state != nil {
				state.done <- fmt.Errorf("invalid args: no src_paths")
			}
		}
	} else {
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				"failed to parse args", job.BookID.Int64,
			)
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			"error", "failed to parse args", job.ID,
		)
		if state != nil {
			state.done <- fmt.Errorf("failed to parse args")
		}
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

		// Collect input file paths from the source directory
		entries, err := readDirectory(srcDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot read src_dir %s: %v", srcDir, err))
			return
		}

		// Only consider audio files
		var inputPaths []string
		for _, e := range entries {
			if e.Type == "file" && isAudioFile(e.Name) {
				inputPaths = append(inputPaths, e.Path)
			}
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

// ListJobs handles GET /api/jobs?limit=N.
// Returns recent jobs newest-first with clean JSON matching the single-job contract.
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	limit := parseIntQueryParam(r, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := h.svc.DB.Query(
		"SELECT id, book_id, status, output, error, output_file, started_at, completed_at, created_at FROM processing_jobs ORDER BY created_at DESC LIMIT ?",
		limit,
	)
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
	if status == "done" || status == "error" {
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

// Ensure io import is used
var _ = io.EOF
