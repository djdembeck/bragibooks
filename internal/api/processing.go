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
	// dispatching is the set of job IDs already handed to a worker (on
	// workerCh or being processed), so the dispatcher never sends the same
	// job twice. Guarded by mu.
	dispatching map[string]struct{}
	// dispatchSig wakes the dispatcher (one slot coalesces bursts).
	dispatchSig chan struct{}
	// dispatchOnce ensures a single dispatcher goroutine even if
	// RunWorkers is called more than once.
	dispatchOnce sync.Once
	// workerStartHook (test-only, production nil) runs after the worker
	// dequeues its first job and before processing, pinning the
	// enqueue-then-reconfigure window for snapshot tests.
	workerStartHook func(jobID string)
	// bookCoversMu serializes, PER BOOK, the enqueue's snapshot read against
	// a capture's snapshot write + superseded-cover prune: without it a
	// capture could prune the cover file in the gap between the enqueue's
	// loadSnapshotForQueue and its job-row insert, and the job would then
	// name a file that is gone.
	bookCoversMu sync.Map // bookID (int64) -> *sync.Mutex
}

// lockBookCover returns unlock, the per-book lock that pairs a capture's
// write+prune with the enqueue's snapshot read (finding: prune-vs-enqueue
// gap). The mutex is held for the whole critical section and is never
// reused after the first unlock, so a second call for the same book
// re-locks the same mutex instance.
func (s *ProcessingService) lockBookCover(bookID int64) (unlock func()) {
	v, _ := s.bookCoversMu.LoadOrStore(bookID, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type jobState struct {
	job models.ProcessingJob
	// progressSubs is the per-connection lifecycle-event fan-out: every
	// open SSE stream registers a one-slot channel before its select loop.
	// sendProgress (and finalize) fan the event out to ALL of them, so two
	// tabs streaming the same job both see "running"/"started processing"
	// — a single shared channel was single-consumer, so a second tab saw
	// nothing. Each wake is non-blocking: a slow reader is dropped, never
	// stalling the worker. Buffered one slot coalesces, and a dropped
	// terminal event is recovered by the stream's done-sync clone.
	progressSubs map[chan string]struct{}
	// progressMu guards progressSubs and closed and serializes sends against
	// finalize's channel closes.
	progressMu sync.Mutex
	// closed marks that finalize has closed every progress subscriber.
	closed bool
	// doneSync is the terminal-result broadcast that every open SSE stream
	// reads after its progress loop ends. It is a condition variable, not a
	// buffered channel, because a channel's value is single-reader: with two
	// tabs streaming one job, the first stream to finish would eat the done
	// value and the second would block. finalize reports exactly once; any
	// number of streams may await it. Pointer: a doneSync contains a
	// sync.Mutex (via its Cond) and must never be copied.
	doneSync *doneSync
	// jobCtx/cancel let the API kill the job's m4b-merge process mid-run.
	jobCtx context.Context
	cancel context.CancelFunc
	// finalized ensures the terminal progress/done signal is sent exactly once,
	// no matter how many components (cancel request, worker, timeout) finalize.
	finalized sync.Once
	// outputMu guards liveOutput/outputOffset and outputSubs. The buffer
	// holds m4b-merge stdout for an active job so a reconnecting stream can
	// recover lines it missed; the terminal row still persists the complete
	// output.
	outputMu     sync.Mutex
	liveOutput   string
	outputOffset int
	// outputSubs is the per-connection wake set: every open SSE stream
	// registers a one-slot channel before its select loop. appendOutput fans
	// the wake out to ALL of them, so two tabs streaming the same job both
	// see live output. The buffered one slot coalesces wakes — each wake
	// makes the reader resend the current full state, so a dropped
	// notification loses nothing, and a slow reader can never stall the
	// child's stdout drain.
	outputSubs map[chan struct{}]struct{}
}

// NewProcessingService creates a ProcessingService backed by the given database
// and audio processor, with the specified number of concurrent workers.
// On startup it sweeps jobs left active by a previous run: they are marked
// error and their books reset to 'pending' so they can be re-queued.
func NewProcessingService(db *sql.DB, proc *audio.Processor, numWorkers int) *ProcessingService {
	s := &ProcessingService{
		db:          db,
		proc:        proc,
		workers:     numWorkers,
		jobs:        make(map[string]*jobState),
		workerCh:    make(chan *models.ProcessingJob, 100),
		dispatching: make(map[string]struct{}),
		dispatchSig: make(chan struct{}, 1),
	}
	s.sweepOrphans()
	return s
}

// signalDispatcher wakes the dispatcher if one is running. The one-slot
// buffer coalesces bursts: a pending signal already means the dispatcher
// will rescan the DB queue, so additional enqueues need not each wake it.
// A signal sent before RunWorkers is a harmless no-op (nobody reads the
// slot yet); the dispatcher's initial scan on startup catches those jobs.
func (s *ProcessingService) signalDispatcher() {
	select {
	case s.dispatchSig <- struct{}{}:
	default:
	}
}

// sweepOrphans marks jobs that were queued/running when the service last
// exited as error, and resets their stuck 'processing' books back to the
// status the book had when the (latest) job was queued — the same
// pre-queue-status contract as the cancel path (finding: F28: one
// consistent answer across cancel and orphan-sweep). A book whose job
// predates the book_pre_status column, or that was 'pending' when queued,
// lands on 'pending' as before.
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
	// (book, pre-queue status) pairs of the swept jobs, so only their stuck
	// books are reset — to the status the newest swept job recorded.
	type bookReset struct {
		bookID int64
		pre    string
	}
	var resets []bookReset
	// rowid (monotonic insert order) picks the NEWEST swept job per book;
	// queue_seq is NULL for pre-dispatcher rows, so it cannot order them.
	rows, err := s.db.Query(`
		SELECT j.book_id, j.book_pre_status
		FROM processing_jobs j
		JOIN (
			SELECT book_id, MAX(rowid) AS newest
			FROM processing_jobs
			WHERE status = ? AND book_id IS NOT NULL AND error = ?
			GROUP BY book_id
		) l ON l.book_id = j.book_id AND l.newest = j.rowid
		WHERE j.status = ? AND j.book_id IS NOT NULL AND j.error = ?`,
		jobStatusError, "orphaned: service restarted while job was active",
		jobStatusError, "orphaned: service restarted while job was active",
	)
	if err == nil {
		for rows.Next() {
			var r bookReset
			var pre sql.NullString
			if rows.Scan(&r.bookID, &pre); err != nil {
				log.Printf("orphan job sweep: read reset: %v", err)
				break
			}
			if pre.Valid && pre.String != "" {
				r.pre = pre.String
			} else {
				// Pre-column row (or empty pre-status): keep the historic
				// behavior for books stuck in 'processing' after a restart.
				r.pre = "pending"
			}
			resets = append(resets, r)
		}
		rows.Close()
	}
	for _, r := range resets {
		s.db.Exec("UPDATE books SET status = ?, status_message = '' WHERE id = ? AND status = 'processing'", r.pre, r.bookID)
	}
	if n > 0 {
		log.Printf("orphan job sweep: marked %d job(s) as error, reset %d stuck book(s) to their pre-queue status", n, len(resets))
	}
}

// RunWorkers starts the dispatcher and the worker goroutines. The dispatcher
// is the SOLE producer of workerCh (finding: F7): it pulls 'queued' jobs from
// the durable DB queue in queue_seq order and hands each to a free worker, so
// QueueJob never blocks on a full channel and a large bulk action enqueues
// instantly. numWorkers < 1 (the "use all cores" convention for m4b-merge)
// would otherwise spawn no workers and leave every job queued forever, so it
// is clamped to at least one.
func (s *ProcessingService) RunWorkers(ctx context.Context, numWorkers int) {
	if numWorkers < 1 {
		numWorkers = 1
	}

	// One dispatcher per service, even if RunWorkers is called twice.
	s.dispatchOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.dispatch(ctx)
		}()
	})

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
						s.signalDispatcher()
						continue
					}
					if s.workerStartHook != nil {
						s.workerStartHook(job.ID)
					}
					s.processJob(ctx, job)
					// The job is terminal: forgetJob cleared the in-flight
					// set; wake the dispatcher to pull the next job (if any).
					s.signalDispatcher()
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

// dispatch is the worker-refill loop (finding: F7). It is the only code that
// sends on workerCh. It runs continuously (no drain-and-stop, which would
// re-introduce the blocking-send problem when the queue is deep), pulling
// the oldest queued DB job that has not already been handed to a worker and
// sending it — blocking only while a worker is busy, which is exactly the
// case where a slot is about to free.
func (s *ProcessingService) dispatch(ctx context.Context) {
	// The initial scan covers jobs queued before RunWorkers started.
	s.pump(ctx)
	for {
		select {
		case <-s.dispatchSig:
			s.pump(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// pump moves as many queued DB jobs onto workerCh as there are free slots,
// oldest (queue_seq) first. It blocks on the channel send only when every
// worker is busy — the normal, expected case — and re-scans on each delivery
// so a burst of free slots drains the backlog in order.
func (s *ProcessingService) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job := s.nextQueuedJob()
		if job == nil {
			return
		}
		s.markDispatched(job.ID)
		select {
		case s.workerCh <- job:
		case <-ctx.Done():
			s.markUndispatched(job.ID)
			return
		}
	}
}

// nextQueuedJob returns the oldest 'queued' job not already dispatched, or
// nil when the backlog is empty. The NOT IN dispatching set is enforced in
// Go (the set is small: at most workers + in-flight channel depth) so the
// query stays a plain indexed ORDER BY.
func (s *ProcessingService) nextQueuedJob() *models.ProcessingJob {
	s.mu.RLock()
	seen := make(map[string]struct{}, len(s.dispatching))
	for id := range s.dispatching {
		seen[id] = struct{}{}
	}
	s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, book_id, m4b_merge_args, status, action, detail, output, error, output_file, started_at, completed_at, created_at, COALESCE(book_pre_status, '') FROM processing_jobs WHERE status = ? ORDER BY queue_seq ASC",
		jobStatusQueued,
	)
	if err != nil {
		log.Printf("dispatcher: query queued jobs: %v", err)
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var job models.ProcessingJob
		var mergeArgs, preStatus sql.NullString
		if err := rows.Scan(&job.ID, &job.BookID, &mergeArgs, &job.Status, &job.Action,
			&job.Detail, &job.Output, &job.Error, &job.OutputFile, &job.StartedAt,
			&job.CompletedAt, &job.CreatedAt, &preStatus); err != nil {
			log.Printf("dispatcher: scan queued job: %v", err)
			return nil
		}
		if _, ok := seen[job.ID]; ok {
			continue
		}
		job.M4bMergeArgs = mergeArgs.String
		if preStatus.Valid {
			job.BookPreStatus = preStatus
		}
		return &job
	}
	if err := rows.Err(); err != nil {
		log.Printf("dispatcher: queued rows: %v", err)
		return nil
	}
	return nil
}

// markDispatched records that jobID has been handed to a worker.
func (s *ProcessingService) markDispatched(id string) {
	s.mu.Lock()
	s.dispatching[id] = struct{}{}
	s.mu.Unlock()
}

// markUndispatched removes jobID from the in-flight set so the dispatcher
// may hand it to a worker again.
func (s *ProcessingService) markUndispatched(id string) {
	s.mu.Lock()
	delete(s.dispatching, id)
	s.mu.Unlock()
}

// errActiveJobForBook is returned when the book already has a queued or
// running job, so the client gets a 409 instead of a duplicate process.
var errActiveJobForBook = errors.New("book already has an active job")

// queueDestination carries a request's resolved per-queue destination
// overrides into QueueJob. Both fields are the EFFECTIVE value for this
// request (a profile already resolved against the globals, or an explicit
// override): blank means "inherit the live global at enqueue time", and
// QueueJob freezes the final triple into the job args.
type queueDestination struct {
	OutputDirOverride  string
	PathFormatOverride string
}

// jobArgs is the typed payload stored in a job's m4b_merge_args. The
// optional destination fields are a fully-resolved queue-time snapshot of
// the selected output profile (effective directory + format, blank fields
// inherited from the globals at queue time); zero (omitted) for unprofiled
// jobs that run with the worker-time global settings.
type jobArgs struct {
	SrcPaths []string `json:"src_paths"`
	// OutputDir is the effective absolute output root FROZEN AT ENQUEUE
	// TIME: the global value is resolved and serialized here for every
	// job, so a settings change after enqueue cannot redirect a queued
	// job. No omitempty: a post-cutover row always carries the resolved
	// value ("" only ever means a pre-cutover legacy row, which the
	// worker refuses before running).
	OutputDir string `json:"output_dir"`
	// OutputScheme is the effective relative path format frozen at
	// enqueue time ("" = pre-cutover legacy row only).
	OutputScheme string `json:"output_scheme"`
	// CompletedDir is the engine's move-on-success source directory,
	// frozen at enqueue time ("" = disabled).
	CompletedDir string `json:"completed_dir,omitempty"`
	// Snapshot is the authoritative capture the job runs on: the effective
	// metadata, the engine identity it was captured under, and the immutable
	// chapter/artwork references. Non-nil for jobs queued after the
	// cutover; nil (snapshot_version 0) for legacy rows, which the worker
	// refuses with an explicit re-preview/requeue path rather than guessing.
	Snapshot *snapshotJSON `json:"snapshot,omitempty"`
}

// errNoSnapshotForBook is the pre-enqueue refusal for a book that has no
// authoritative capture row. The operator path is re-match (or re-save) the
// book so its selection is captured; the message says exactly that.
var errNoSnapshotForBook = errors.New("book has no captured metadata snapshot; re-match the book before processing")

// snapshotVersionCurrent is the durable snapshot schema version written at
// queue time. v1 is the authoritative-snapshot shape (effective metadata +
// engine identity + chapter/cover references).
const snapshotVersionCurrent = 1

// loadSnapshotForQueue reads the book's authoritative capture and pairs it
// with the CURRENT engine identity — the identity the job must report back,
// and the gate the worker re-checks before execution. A missing engine
// identity (capabilities not verified) is a hard error: a job captured under
// an unknown resolver must not queue.
func (s *ProcessingService) loadSnapshotForQueue(bookID int64) (*snapshotJSON, error) {
	caps := s.proc.Capabilities()
	if caps == nil {
		return nil, errors.New("engine capabilities not verified; cannot capture a job snapshot")
	}
	snap, err := loadBookSnapshot(s.db, bookID)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, nil
	}
	return &snapshotJSON{
		Version:  snapshotVersionCurrent,
		Engine:   audio.EngineIdentity{SchemaVersion: caps.SchemaVersion, ResolverVersion: caps.ResolverVersion},
		Snapshot: *snap,
	}, nil
}

// QueueJob creates a processing job, persists it to the DB, and enqueues it.
// opts carries the resolved destination snapshot for profile-backed
// requests; the zero value preserves the global-settings behavior.
func (s *ProcessingService) QueueJob(ctx context.Context, bookID int64, srcPaths []string, opts queueDestination) (string, error) {
	// Queue-time validation: m4b-merge only accepts .mp3/.m4a/.m4b. The
	// broader IsAudioFile set (covers, discovery, browsing) must not feed a
	// job the engine would reject mid-run, so unsupported containers are
	// refused here with a clear message (finding: F22).
	if unsupported := firstUnsupportedMergePath(srcPaths); unsupported != "" {
		return "", fmt.Errorf("unsupported input format %q: m4b-merge accepts .mp3, .m4a, .m4b only", unsupported)
	}

	// The authoritative snapshot is a HARD prerequisite: a job runs on the
	// operator's captured selection, never on a worker-time lookup. A book
	// with no capture row (a historical row not yet matched/edited through
	// the new flow) is refused here with an explicit re-match/re-preview
	// path — never guessed, never silently run under a guessed schema.
	var snap *snapshotJSON
	if bookID > 0 {
		// Per-book lock held across the read→insert critical section (defer
		// runs at function end, past the atomic INSERT below): a capture's
		// snapshot write + superseded-cover prune must not interleave here,
		// or it could delete the cover file this job is about to name while
		// the active-job count is still zero (finding: prune vs enqueue).
		defer s.lockBookCover(bookID)()
		captured, err := s.loadSnapshotForQueue(bookID)
		if err != nil {
			return "", fmt.Errorf("load snapshot for queue: %w", err)
		}
		if captured == nil {
			return "", errNoSnapshotForBook
		}
		snap = captured
	}

	// The active-job guard is the advisory fast path (a clean 409 with no
	// wasted UUID); the atomic INSERT below is the authoritative one, so two
	// concurrent enqueues for the same book cannot BOTH pass and both insert.
	if bookID > 0 && s.hasActiveJobForBook(bookID) {
		return "", errActiveJobForBook
	}

	// The book's pre-queue status, stored on the job so a RUNNING-job cancel
	// can restore it instead of hard-coding 'matched' (finding: F28).
	var bookPreStatus sql.NullString
	if bookID > 0 {
		if err := s.db.QueryRow("SELECT status FROM books WHERE id = ?", bookID).Scan(&bookPreStatus); err != nil {
			bookPreStatus = sql.NullString{}
		}
	}

	id := uuid.New().String()

	// Freeze the COMPLETE destination at enqueue time for every job —
	// profile-backed, explicitly overridden, AND plain global jobs: the
	// effective output root, relative path format, and completed directory
	// are resolved NOW (override/profile wins, blank inherits the live
	// global) and serialized into the job args. processJob consumes only
	// these queued values, never the live SettingsSnapshot, so a settings
	// change after enqueue cannot redirect, reformat, or re-complete a job
	// that is already in the queue. An empty effective destination is a
	// misconfiguration the operator must fix, so it is refused here with a
	// clear message instead of surfacing deep in a worker.
	liveOut, liveCompleted, liveFormat := s.proc.SettingsSnapshot()
	args := jobArgs{SrcPaths: srcPaths, Snapshot: snap}
	effRoot := strings.TrimSpace(opts.OutputDirOverride)
	if effRoot == "" {
		effRoot = strings.TrimSpace(liveOut)
	}
	if effRoot == "" {
		return "", errors.New("output directory is not set (set the global output directory)")
	}
	if abs, aerr := filepath.Abs(effRoot); aerr == nil {
		effRoot = abs
	}
	effFormat := strings.TrimSpace(opts.PathFormatOverride)
	if effFormat == "" {
		effFormat = strings.TrimSpace(liveFormat)
	}
	if effFormat == "" {
		return "", errors.New("output path format is not set (set the global path format)")
	}
	// The INHERITED global format is validated here too, not only an
	// explicit override: a stored {asin} (global or inherited) must fail
	// at enqueue with a clear diagnosis, never only deep in a job run.
	if err := audio.ValidateRelativePathFormat(effFormat); err != nil {
		return "", fmt.Errorf("output path format: %w", err)
	}
	args.OutputDir = effRoot
	args.OutputScheme = effFormat
	args.CompletedDir = strings.TrimSpace(liveCompleted)
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("serialize job args: %w", err)
	}

	job := &models.ProcessingJob{
		ID:            id,
		BookID:        sql.NullInt64{Int64: bookID, Valid: true},
		M4bMergeArgs:  string(argsJSON),
		Action:        "merge",
		Status:        "queued",
		Error:         nil,
		OutputFile:    nil,
		StartedAt:     nil,
		CompletedAt:   nil,
		BookPreStatus: bookPreStatus,
	}

	snapshotVersion := 0
	if snap != nil {
		snapshotVersion = snap.Version
	}

	// Atomic enqueue: the INSERT only lands when the book has no queued/
	// running job (finding: F5). RowsAffected()==0 means a concurrent
	// enqueue won the race — translate the losing write to the same 409 the
	// fast path returns, so the HTTP contract is identical.
	res, err := s.db.Exec(
		`INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status, action, book_pre_status, queue_seq, snapshot_version)
		 SELECT ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(queue_seq), 0) + 1 FROM processing_jobs), ?
		 WHERE ? = 0 OR NOT EXISTS (
		     SELECT 1 FROM processing_jobs
		     WHERE book_id = ? AND status IN (?, ?))`,
		job.ID, job.BookID.Int64, job.M4bMergeArgs, job.Status, job.Action, job.BookPreStatus,
		snapshotVersion, bookID, job.BookID.Int64, jobStatusQueued, jobStatusRunning,
	)
	if err != nil {
		return "", fmt.Errorf("insert processing job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errActiveJobForBook
	}

	// The per-job context is cancelable from the API (POST /jobs/:id/cancel)
	// so a running m4b-merge process can be killed mid-flight. It is derived
	// from Background, not the request context, because the job outlives the
	// request that queued it.
	jobCtx, jobCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.jobs[id] = &jobState{
		job:          *job,
		progressSubs: make(map[chan string]struct{}),
		doneSync:     newDoneSync(),
		jobCtx:       jobCtx,
		cancel:       jobCancel,
		outputSubs:   make(map[chan struct{}]struct{}),
	}
	s.mu.Unlock()

	// The book is intentionally NOT flipped to 'processing' here: the
	// transition happens when a worker starts the job (processJob). A queued
	// job is a promise, not work — cancelling it then requires no book
	// status rollback, and the UI never shows a book as 'processing' while
	// the job is still queued. (Pre-queued status 'matched'/'pending' is
	// captured above on the job for the cancel-restore path.)
	//
	// Enqueue is NON-blocking (finding: F7): the DB queue is the backlog.
	// The dispatcher (started by RunWorkers) is the sole producer of
	// workerCh — it pulls 'queued' rows in queue_seq order as slots free,
	// so a 1000-book bulk action returns job IDs immediately instead of
	// blocking the HTTP request until workers drain. Signalling is
	// non-blocking (one slot coalesces a burst of enqueues).
	s.signalDispatcher()
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
		`INSERT INTO processing_jobs (id, book_id, status, action, detail, started_at, completed_at, queue_seq)
		 VALUES (?, ?, 'done', ?, ?, datetime('now'), datetime('now'), (SELECT COALESCE(MAX(queue_seq), 0) + 1 FROM processing_jobs))`,
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
// dropping the map entry frees memory without losing information. The job is
// terminal, so it also leaves the dispatcher's in-flight set (otherwise that
// set grows one entry per job for the process's lifetime).
func (s *ProcessingService) forgetJob(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	delete(s.dispatching, id)
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

// mergeSupportedExts are the ONLY containers m4b-merge accepts as input.
// The broader audio.IsAudioFile set (cover detection, folder discovery,
// library browsing) deliberately stays wide, but processing must refuse to
// queue a job the engine would reject (finding: F22).
var mergeSupportedExts = map[string]bool{
	".mp3": true, ".m4a": true, ".m4b": true,
}

// firstUnsupportedMergePath returns the first source path whose extension
// m4b-merge cannot handle, or "" when every path is queueable.
func firstUnsupportedMergePath(srcPaths []string) string {
	for _, p := range srcPaths {
		if !mergeSupportedExts[strings.ToLower(filepath.Ext(p))] {
			return filepath.Base(p)
		}
	}
	return ""
}

var errJobNotFound = errors.New("job not found")

var errJobTerminal = errors.New("job already finished")

const cancelledProgressMsg = `{"status":"cancelled","message":"job cancelled"}`

// sendProgress fans a lifecycle event out to EVERY open stream (finding:
// F8). A single shared channel was single-consumer: with two tabs streaming
// one job, only one saw "running"/"started processing". Each delivery is
// non-blocking — a slow reader drops the event and recovers it from the
// terminal clone after the loop (or keeps polling the DB, which the page
// does for status), so a sick subscriber can never stall the worker. After
// finalize runs, late sends (e.g. a worker reaching the "running" step
// milliseconds after a queued cancel) must be dropped.
func (st *jobState) sendProgress(msg string) {
	st.progressMu.Lock()
	defer st.progressMu.Unlock()
	if st.closed {
		return
	}
	for ch := range st.progressSubs {
		select {
		case ch <- msg:
		default: // Wake already pending; the terminal clone covers the loss.
		}
	}
}

// registerProgressSubscriber subscribes ch to lifecycle-event wakes and
// returns a deregister function. A one-slot buffer coalesces bursts; the
// reader re-reads the DB status on every wake, so a dropped non-terminal
// event loses nothing the page does not already recover.
func (st *jobState) registerProgressSubscriber(ch chan string) (deregister func()) {
	st.progressMu.Lock()
	if st.closed {
		// Finalized while the stream was connecting: close immediately so
		// the stream's loop exits and it reaches the done sync (which
		// already carries the terminal result) instead of waiting for a
		// terminal event that will never come.
		st.progressMu.Unlock()
		close(ch)
		return func() {}
	}
	st.progressSubs[ch] = struct{}{}
	st.progressMu.Unlock()
	return func() {
		st.progressMu.Lock()
		delete(st.progressSubs, ch)
		st.progressMu.Unlock()
	}
}

// appendOutputFn adapts the job state's line sink to the worker's callback
// surface; nil when the job has no in-memory state (a restarted process).
func appendOutputFn(st *jobState) func(line string) {
	if st == nil {
		return nil
	}
	return st.appendOutput
}

// appendOutput adds one stripped m4b-merge stdout line to the live buffer and
// wakes every open stream. Each wake send is non-blocking: a missing or slow
// reader must never stall the child's stdout drain.
func (st *jobState) appendOutput(line string) {
	st.outputMu.Lock()
	st.liveOutput += line + "\n"
	st.outputOffset = len(st.liveOutput)
	subscribers := make([]chan struct{}, 0, len(st.outputSubs))
	for ch := range st.outputSubs {
		subscribers = append(subscribers, ch)
	}
	st.outputMu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- struct{}{}:
		default: // Wake already pending; the reader resends full state on each.
		}
	}
}

// outputSnapshot returns the live stdout accumulated so far, plus the byte
// offset the next suffix starts at — the reconnect/initial payload shape.
func (st *jobState) outputSnapshot() (string, int) {
	st.outputMu.Lock()
	defer st.outputMu.Unlock()
	return st.liveOutput, st.outputOffset
}

// outputFrom returns the live stdout appended after startOffset, i.e. the
// suffix since this stream was connected, plus the buffer's current end
// offset. A startOffset past the end never grows the buffer: it yields an
// empty suffix (coalesced wakes are safe). The full-buffer re-scan here is
// bounded by the coalescing above — each wake costs O(unread) bytes, not the
// whole accumulated log.
func (st *jobState) outputFrom(startOffset int) (string, int) {
	st.outputMu.Lock()
	defer st.outputMu.Unlock()
	if startOffset > len(st.liveOutput) {
		startOffset = len(st.liveOutput)
	}
	return st.liveOutput[startOffset:], st.outputOffset
}

// registerOutputSubscriber subscribes ch to output wakes (one slot is enough
// — wakes coalesce) and returns a deregister function.
func (st *jobState) registerOutputSubscriber(ch chan struct{}) (deregister func()) {
	st.outputMu.Lock()
	st.outputSubs[ch] = struct{}{}
	st.outputMu.Unlock()
	return func() {
		st.outputMu.Lock()
		delete(st.outputSubs, ch)
		st.outputMu.Unlock()
	}
}

// doneSync implements "wait until the job is terminal, then read its result"
// for an unlimited number of concurrent readers. It also carries the
// terminal progress message so a stream that observes the progress channel's
// close BEFORE the terminal value (a concurrent reader grabbed it) can still
// write the terminal SSE line itself from the broadcast — a closed channel
// yields its buffer first, so no drain of it can ever recover the message.
type doneSync struct {
	mu       sync.Mutex
	cond     *sync.Cond
	reported bool
	msg      string
	err      error
}

// newDoneSync returns an addressable doneSync: the Cond is bound to this
// instance's own mutex, which must never be copied out.
func newDoneSync() *doneSync {
	ds := &doneSync{}
	ds.cond = sync.NewCond(&ds.mu)
	return ds
}

func (ds *doneSync) report(msg string, err error) {
	ds.mu.Lock()
	ds.reported = true
	ds.msg = msg
	ds.err = err
	ds.cond.Broadcast()
	ds.mu.Unlock()
}

// await blocks until reported and returns the stored terminal progress
// message and error.
func (ds *doneSync) await() (string, error) {
	ds.mu.Lock()
	for !ds.reported {
		ds.cond.Wait()
	}
	msg, err := ds.msg, ds.err
	ds.mu.Unlock()
	return msg, err
}

// finalize ends every open stream's lifecycle loop and reports the terminal
// result (message plus error) on the done sync — exactly once, so the SSE
// streams detect termination (a closed progress subscription ends their
// loops; without it they would keep heartbeating after the terminal event)
// and the done awaiters unblock, regardless of which component (worker,
// timeout, or cancel request) won.
//
// The terminal PROGRESS line is broadcast to every subscriber (non-blocking,
// same fan-out as lifecycle events) BEFORE the channels close: the progress
// channel is the single source of the terminal progress message, so a
// single-consumer design would let one stream's read starve every other
// connected tab (finding: F8 part a). After the loop, every stream writes
// the terminal STATUS line from the done sync, which carries the TRUE
// terminal status (finding: F8 part b — a cancelled job ends with
// {"status":"cancelled"}, never a spurious {"status":"done"}). Subscribers
// registered AFTER finalize observes closed get their channel closed
// immediately, so a late stream exits its loop and reaches the done sync
// (which already has the result) rather than waiting for a terminal event
// that will never come.
func (st *jobState) finalize(msg string, doneErr error) {
	st.finalized.Do(func() {
		st.progressMu.Lock()
		if !st.closed {
			st.closed = true
			for ch := range st.progressSubs {
				select {
				case ch <- msg:
				default:
				}
				close(ch)
			}
			st.progressSubs = make(map[chan string]struct{})
		}
		st.progressMu.Unlock()
		st.doneSync.report(msg, doneErr)
	})
}

// CancelJob cancels a processing job and returns the job's book_id.
//
// Behavior by current DB status:
//   - queued:  the job is marked 'cancelled' in the DB immediately. The book
//     normally remains in its pre-queue state; a conditional restore covers a
//     worker-start interleaving that already moved it to 'processing'. The
//     worker will dequeue the job and skip it.
//   - running: the per-job context is cancelled so m4b-merge is killed via its
//     context; the worker's finalizeCancelled then writes the terminal
//     'cancelled' status and restores the book. CancelJob does not touch the
//     book itself here, so a job that finishes successfully in the race window
//     ends up consistently 'done'.
//   - done/error/cancelled: errJobTerminal is returned.
func (s *ProcessingService) CancelJob(jobID string) (sql.NullInt64, error) {
	var status string
	var bookID sql.NullInt64
	var bookPreStatus sql.NullString
	err := s.db.QueryRow(
		"SELECT status, book_id, book_pre_status FROM processing_jobs WHERE id = ?", jobID,
	).Scan(&status, &bookID, &bookPreStatus)
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
		// Conditional on the read status (finding: F9): an unconditional
		// UPDATE would overwrite a worker that won the queued->running
		// race milliseconds after our read, leaving the book 'done' and
		// the job 'cancelled'. When 0 rows were affected the job already
		// transitioned — the cancel is a no-op and we report the terminal
		// state the worker will own.
		res, err := s.db.Exec(
			"UPDATE processing_jobs SET status = ?, completed_at = datetime('now') WHERE id = ? AND status = ?",
			jobStatusCancelled, jobID, jobStatusQueued,
		)
		if err != nil {
			return sql.NullInt64{}, fmt.Errorf("cancel queued job: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return bookID, nil // the worker claimed the job and owns book restoration
		} else if bookID.Valid {
			pre := bookPreStatus.String
			if pre == "" {
				pre = s.bookPreStatus(jobID)
			}
			s.restoreBookOnCancel(bookID.Int64, pre)
		}
		// Close any open progress stream; the worker's skip path finalizes
		// idempotently via sync.Once.
		if state != nil {
			state.finalize(cancelledProgressMsg, nil)
		}
	} else if status == jobStatusRunning && state == nil {
		// Running job with no in-memory state (the process restarted while
		// the job was running): no worker owns it, so nobody would observe
		// the cancelled context. Persist the terminal state directly and
		// restore the book to its PRE-QUEUE status (finding: F28) — not a
		// hard-coded 'matched', which would demote a reprocessed 'done'
		// book or promote a 'pending' one.
		res, err := s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ? AND status = ?",
			jobStatusCancelled, "cancelled by user", jobID, jobStatusRunning,
		)
		if err != nil {
			return sql.NullInt64{}, fmt.Errorf("cancel running job: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 && bookID.Valid {
			s.restoreBookOnCancel(bookID.Int64, s.bookPreStatus(jobID))
		}
	}
	return bookID, nil
}

// bookPreStatus reads the job's stored pre-queue book status (v13), falling
// back to 'matched' for jobs queued before the column existed.
func (s *ProcessingService) bookPreStatus(jobID string) string {
	var pre sql.NullString
	if err := s.db.QueryRow(
		"SELECT book_pre_status FROM processing_jobs WHERE id = ?", jobID,
	).Scan(&pre); err == nil && pre.Valid && pre.String != "" {
		return pre.String
	}
	return "matched"
}

// restoreBookOnCancel resets a cancelled job's book from 'processing' back
// to preStatus, the status it had when the job was queued (finding: F28).
// A failed restore is logged, not swallowed: when the job is already
// terminal, the orphan sweep (which only touches active jobs) can never
// recover a book stranded in 'processing', so the failure must be visible.
func (s *ProcessingService) restoreBookOnCancel(bookID int64, preStatus string) {
	if _, err := s.db.Exec(
		"UPDATE books SET status = ?, status_message = '' WHERE id = ? AND status = 'processing'",
		preStatus, bookID,
	); err != nil {
		log.Printf("restore book %d to %q after cancel: %v", bookID, preStatus, err)
	}
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

	// Claim the job before moving its book to processing. This conditional
	// transition arbitrates against queued cancellation: only the winner of
	// queued -> running may proceed to processing.
	res, err := s.db.Exec(
		"UPDATE processing_jobs SET status = ?, started_at = datetime('now') WHERE id = ? AND status = ?",
		jobStatusRunning, job.ID, jobStatusQueued,
	)
	if err != nil {
		log.Printf("start processing job %s: %v", job.ID, err)
		s.forgetJob(job.ID)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// A queued cancellation won before this worker claimed the job. Restore
		// defensively in case the book was already marked processing by an older
		// worker path; the conditional restore cannot overwrite a newer status.
		if job.BookID.Valid {
			pre := ""
			if job.BookPreStatus.Valid {
				pre = job.BookPreStatus.String
			}
			if pre == "" {
				pre = s.bookPreStatus(job.ID)
			}
			s.restoreBookOnCancel(job.BookID.Int64, pre)
		}
		if state != nil {
			state.finalize(cancelledProgressMsg, nil)
		}
		s.forgetJob(job.ID)
		return
	}

	// Claiming the job first closes the queued-cancel race: cancellation can
	// only win before this transition, in which case the book was never moved
	// to processing; or after it, in which case the running cancel path owns
	// restoring the pre-queue status.
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'processing', status_message = '' WHERE id = ?",
			job.BookID.Int64,
		)
	}
	if state != nil && state.jobCtx != nil && state.jobCtx.Err() != nil {
		s.finalizeCancelled(job, "")
		return
	}

	if state != nil {
		state.sendProgress(`{"status":"running","message":"started processing"}`)
	}

	// Parse the stored typed args
	var args jobArgs
	argsErr := fmt.Errorf("failed to parse args")
	if err := json.Unmarshal([]byte(job.M4bMergeArgs), &args); err == nil {
		if len(args.SrcPaths) == 0 {
			argsErr = fmt.Errorf("invalid args: no src_paths")
		} else {
			argsErr = nil
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

	// The destination snapshot, when present, is the fully-resolved
	// queue-time profile pair persisted in the job args — it is NOT re-read
	// from any persisted setting, so the job keeps the destination the
	// requester selected even if the profile or globals change after
	// enqueueing. The authoritative snapshot is embedded the same way: the
	// engine runs on the operator's captured selection, never a worker-time
	// lookup.
	if args.Snapshot == nil {
		// A job captured before the authoritative cutover carries no
		// snapshot. It must NOT run under a guessed schema or the live
		// globals: mark it an explicit error with the re-preview/requeue
		// path, preserving the original source and any prior output.
		legacyErr := fmt.Errorf("this job predates the authoritative processing snapshot and will not run automatically; re-preview and requeue the book to process it under the current engine")
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				legacyErr.Error(), job.BookID.Int64,
			)
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, legacyErr.Error(), job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(legacyErr.Error())+`"}`, legacyErr)
		}
		s.forgetJob(job.ID)
		return
	}

	// Build the structured engine job from the authoritative snapshot. The
	// destination is the COMPLETE frozen queue-time triple (output root,
	// relative path format, completed directory) persisted in the job args
	// — it is NOT re-read from any persisted setting, so a settings change
	// after enqueue cannot redirect, reformat, or re-complete a queued job.
	// Re-validate the stored format as worker-side defense before authored
	// data can steer file writes.
	failInvalidStored := func(err error) {
		invalidErr := fmt.Errorf("invalid stored output snapshot: %v", err)
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				invalidErr.Error(), job.BookID.Int64,
			)
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, invalidErr.Error(), job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(invalidErr.Error())+`"}`, invalidErr)
		}
		s.forgetJob(job.ID)
	}
	effRoot := strings.TrimSpace(args.OutputDir)
	if effRoot == "" {
		failInvalidStored(errors.New("job carries no frozen output directory (row predates the frozen-destination schema); requeue the book"))
		return
	}
	effFormat := strings.TrimSpace(args.OutputScheme)
	if effFormat == "" {
		failInvalidStored(errors.New("job carries no frozen path format (row predates the frozen-destination schema); requeue the book"))
		return
	}
	if err := audio.ValidateRelativePathFormat(effFormat); err != nil {
		failInvalidStored(err)
		return
	}
	completedDir := strings.TrimSpace(args.CompletedDir)

	jf, stagedCover, err := buildEngineJob(args, job.ID, args.Snapshot.Snapshot, effRoot, effFormat)
	// The staged cover is Bragi's own input staging: the engine copies its
	// bytes into the output on success, so it is residue after RunJob in
	// every case (success, failure, cancellation). Removed best-effort in a
	// defer covering the whole run; on a build failure the run never
	// started, so the same removal applies before returning.
	if stagedCover != "" {
		defer os.Remove(stagedCover)
	}
	if err != nil {
		buildErr := fmt.Errorf("build engine job: %v", err)
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				buildErr.Error(), job.BookID.Int64,
			)
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, buildErr.Error(), job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(buildErr.Error())+`"}`, buildErr)
		}
		s.forgetJob(job.ID)
		return
	}

	// A completed directory, when the job was captured with one, is passed to
	// the engine so it moves the (validated) sources there only on a fully
	// successful group — the no-loss invariant: a failed run leaves the
	// sources in place and the recoverable staging behind.
	if completedDir != "" {
		jf.CompletedDir = &completedDir
	}

	result, err := s.proc.RunJob(runCtx, job.ID, jf, audio.JobRunOptions{
		OutputRoot: effRoot,
		Identity:   args.Snapshot.Engine,
		OnOutput:   appendOutputFn(state),
	})
	if err != nil {
		// A cancellation during the run kills m4b-merge via its context;
		// that is 'cancelled', not 'error'.
		if state != nil && state.jobCtx != nil && state.jobCtx.Err() != nil {
			stdout := ""
			if result != nil {
				stdout = result.Stderr
			}
			s.finalizeCancelled(job, stdout)
			return
		}
		if job.BookID.Valid {
			s.db.Exec(
				"UPDATE books SET status = 'error', status_message = ? WHERE id = ?",
				err.Error(), job.BookID.Int64,
			)
		}
		output := ""
		if result != nil {
			output = result.Stderr
			if result.MachineJSON != "" {
				output = strings.TrimRight(output, "\n") + "\n\n" + result.MachineJSON
			}
		}
		s.db.Exec(
			"UPDATE processing_jobs SET status = ?, error = ?, output = ?, completed_at = datetime('now') WHERE id = ?",
			jobStatusError, err.Error(), output, job.ID,
		)
		if state != nil {
			state.finalize(`{"status":"error","message":"`+escapeJSON(err.Error())+`"}`, err)
		}
		s.forgetJob(job.ID)
		return
	}

	// Success: the engine has published the validated artifacts under the
	// output root. The audio artifact is the book's output file.
	outputFile := result.PrimaryOutput
	if job.BookID.Valid {
		s.db.Exec(
			"UPDATE books SET status = 'done', status_message = '', dest_path = ?, format_type = ? WHERE id = ?",
			outputFile, outputFormat(outputFile), job.BookID.Int64,
		)
	}
	output := result.Stderr
	if result.MachineJSON != "" {
		output = strings.TrimRight(output, "\n") + "\n\n" + result.MachineJSON
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
// m4b-merge process was killed via the job context, persists the stdout the
// killed run had already produced, restores the book to its PRE-QUEUE status
// (only if it is still 'processing'), and emits the terminal progress
// signal. The queued-cancel path leaves the book untouched (it never left
// its pre-queue state); this is the running path. Restoring the stored
// pre-queue status — not a hard-coded 'matched' — keeps a reprocessed
// 'done' book at 'done' and a 'pending' book at 'pending' (finding: F28).
func (s *ProcessingService) finalizeCancelled(job *models.ProcessingJob, stdout string) {
	// Conditional on the job still being 'running': a run that finished
	// successfully in the kill window owns the terminal 'done' state.
	res, err := s.db.Exec(
		"UPDATE processing_jobs SET status = ?, error = ?, output = ?, completed_at = datetime('now') WHERE id = ? AND status = ?",
		jobStatusCancelled, "cancelled by user", stdout, job.ID, jobStatusRunning,
	)
	if err != nil {
		log.Printf("finalize cancelled job %s: %v", job.ID, err)
	} else if n, _ := res.RowsAffected(); n > 0 && job.BookID.Valid {
		pre := ""
		if job.BookPreStatus.Valid {
			pre = job.BookPreStatus.String
		}
		if pre == "" {
			pre = s.bookPreStatus(job.ID)
		}
		s.restoreBookOnCancel(job.BookID.Int64, pre)
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
	// OutputProfileID selects a saved output profile for this batch. Nil /
	// absent means global settings. The selection applies only to the jobs
	// created by THIS request, resolved once at queue time into a snapshot
	// stored in every job — it is never persisted on books and is not
	// reused by later queues.
	OutputProfileID *int64 `json:"output_profile_id"`
}

// ProcessingJobCreated is returned in the response of StartProcessing.
type ProcessingJobCreated struct {
	ID     string `json:"id"`
	BookID int64  `json:"book_id"`
}

// StartProcessingResponse is the JSON shape returned by StartProcessing:
// the created jobs plus the effective destination pair the batch was
// snapshotted under at queue time (both omitted for global-settings
// batches). The web derives each fresh job's Destination line from this
// pair instead of a client-cached profile, which may have been edited
// since the page loaded.
type StartProcessingResponse struct {
	Jobs         []ProcessingJobCreated `json:"jobs"`
	OutputDir    string                 `json:"output_dir,omitempty"`
	OutputScheme string                 `json:"output_scheme,omitempty"`
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

	// Resolve the selected output profile (if any) ONCE, before any job is
	// created, against exactly one config snapshot: blank fields inherit
	// the current global values independently into the effective pair, and
	// the complete pair is snapshotted into every job this request creates.
	// Any resolution failure 400s/404s the whole request with zero jobs.
	runOpts, ok := h.resolveDestination(w, req.OutputProfileID, "", "")
	if !ok {
		return
	}
	if !h.probeDestinations(w, runOpts) {
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

		jobID, err := h.svc.ProcessingSvc.QueueJob(r.Context(), bookID, inputPaths, runOpts)
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

	writeJSON(w, http.StatusOK, StartProcessingResponse{
		Jobs:         jobs,
		OutputDir:    runOpts.OutputDirOverride,
		OutputScheme: runOpts.PathFormatOverride,
	})
}

// resolveDestination validates profileID and the explicit overrides (exactly
// one of the two inputs may be present) and returns the resolved
// queueDestination: a profile resolves against one config snapshot with blank
// fields inheriting the globals independently; explicit overrides are
// validated field-by-field (a blank field omits that override, so each side
// may inherit independently). Writes the response on failure and returns ok
// false; runOpts is valid (zero = full globals) when ok is true.
func (h *Handler) resolveDestination(w http.ResponseWriter, profileID *int64, overrideDir, overrideFormat string) (runOpts queueDestination, ok bool) {
	// A field the client sent with ANY characters is an override, even
	// whitespace-only (which the empty-inherit branches below reject
	// explicitly): counting trimmed emptiness as "set" would let a sent
	// whitespace-only format slip past validation and inherit the global.
	overridesSet := overrideDir != "" || overrideFormat != ""
	if profileID != nil && overridesSet {
		writeError(w, http.StatusBadRequest, "Choose either an output profile or explicit output overrides, not both.")
		return queueDestination{}, false
	}
	if profileID != nil {
		if *profileID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid output profile id")
			return queueDestination{}, false
		}
		profile, err := h.getOutputProfile(*profileID)
		if err != nil {
			if errors.Is(err, errProfileNotFound) {
				writeError(w, http.StatusNotFound, "output profile not found")
			} else {
				writeError(w, http.StatusInternalServerError, "load output profile: "+err.Error())
			}
			return queueDestination{}, false
		}
		cm := h.svc.Config
		if cm == nil {
			writeError(w, http.StatusInternalServerError, "settings manager unavailable")
			return queueDestination{}, false
		}
		cfg := cm.Config()
		effDir := strings.TrimSpace(profile.OutputDir)
		if effDir == "" {
			effDir = strings.TrimSpace(cfg.Directories.OutputDir)
		}
		effFormat := strings.TrimSpace(profile.OutputScheme)
		if effFormat == "" {
			effFormat = strings.TrimSpace(cfg.Processing.PathFormat)
		}
		// An empty effective destination means the global value itself is
		// empty: refuse before any job exists. Never run filepath.Abs on ""
		// (it would silently substitute the process working directory).
		if effDir == "" {
			writeError(w, http.StatusBadRequest, "output directory is not set (set the profile override or the global output directory)")
			return queueDestination{}, false
		}
		if effFormat == "" {
			writeError(w, http.StatusBadRequest, "output path format is not set (set the profile format or the global path format)")
			return queueDestination{}, false
		}
		absDir, err := filepath.Abs(effDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot resolve output directory %s: %v", effDir, err))
			return queueDestination{}, false
		}
		// The relative-format validator is deliberately applied to the
		// effective format even when it was inherited from the global
		// setting: a profile-started job must land inside its root.
		if err := audio.ValidateRelativePathFormat(effFormat); err != nil {
			writeError(w, http.StatusBadRequest, "output path format: "+err.Error())
			return queueDestination{}, false
		}
		return queueDestination{OutputDirOverride: absDir, PathFormatOverride: effFormat}, true
	}
	if overridesSet {
		if dir := strings.TrimSpace(overrideDir); dir != "" {
			// An explicit dir is a destination root and must be absolute,
			// mirroring validateProfile's save-time rule (the queue page and
			// the record-page dialog copy it too). Empty dir is "inherit the
			// global", not an override.
			if !strings.HasPrefix(dir, "/") {
				writeError(w, http.StatusBadRequest, "output directory must be an absolute path")
				return queueDestination{}, false
			}
			absDir, err := filepath.Abs(dir)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot resolve output directory %s: %v", dir, err))
				return queueDestination{}, false
			}
			runOpts.OutputDirOverride = absDir
		}
		// A field the client sent with any characters is an override — a
		// whitespace-only format is not blank, so it must not silently
		// inherit the global format.
		if overrideFormat != "" {
			format := strings.TrimSpace(overrideFormat)
			if format == "" {
				writeError(w, http.StatusBadRequest, "output path format is empty")
				return queueDestination{}, false
			}
			if err := audio.ValidateRelativePathFormat(format); err != nil {
				writeError(w, http.StatusBadRequest, "output path format: "+err.Error())
				return queueDestination{}, false
			}
			runOpts.PathFormatOverride = format
		}
		return runOpts, true
	}
	return queueDestination{}, true
}

// probeDestinations probes every directory this request's jobs will write to
// before the first job exists: a profile, explicit-override, or global
// destination whose mount changed after it was saved must 400 the request
// with zero jobs instead of queueing one that dies at run time. Save-time
// probing cannot cover a later remount, uid change, or env-var override, so
// this is the second, request-level gate.
func (h *Handler) probeDestinations(w http.ResponseWriter, runOpts queueDestination) (ok bool) {
	cm := h.svc.Config
	if cm == nil {
		return true
	}
	cfg := cm.Config()
	effDir := strings.TrimSpace(cfg.Directories.OutputDir)
	if runOpts.OutputDirOverride != "" {
		effDir = runOpts.OutputDirOverride
	}
	if effDir != "" {
		if err := audio.CheckDirWritable(effDir); err != nil {
			writeError(w, http.StatusBadRequest, "output directory "+err.Error())
			return false
		}
	}
	if completed := strings.TrimSpace(cfg.Directories.CompletedDir); completed != "" {
		if err := audio.CheckDirWritable(completed); err != nil {
			writeError(w, http.StatusBadRequest, "completed directory "+err.Error())
			return false
		}
	}
	return true
}

// errReprocessNoRun / errReprocessNoInputs are the two reprocess refusal
// states, mapped to their operator sentences by the handlers.
var (
	errReprocessNoRun    = errors.New("no recorded processing run")
	errReprocessNoInputs = errors.New("no recorded input files")
)

// latestMergeJob returns the book's newest action='merge' job row with its
// parsed jobArgs. Error classes: errReprocessNoRun (no merge job row),
// errReprocessNoInputs (row exists, args empty/unparseable/no src_paths).
// Malformed legacy args never surface as a server error.
func (h *Handler) latestMergeJob(bookID int64) (jobArgs, models.ProcessingJob, error) {
	var job models.ProcessingJob
	// Legacy merge rows predate the column and carry NULL: scan through a
	// nullable type so NULL maps to the no-inputs path, never to a 500.
	var argsJSON sql.NullString
	err := h.svc.DB.QueryRow(
		"SELECT id, m4b_merge_args, status, created_at FROM processing_jobs WHERE book_id = ? AND action = 'merge' ORDER BY created_at DESC, rowid DESC LIMIT 1",
		bookID,
	).Scan(&job.ID, &argsJSON, &job.Status, &job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		job.M4bMergeArgs = ""
		return jobArgs{}, job, errReprocessNoRun
	}
	if err != nil {
		return jobArgs{}, job, err
	}
	job.M4bMergeArgs = argsJSON.String
	var args jobArgs
	if err := json.Unmarshal([]byte(job.M4bMergeArgs), &args); err != nil || len(args.SrcPaths) == 0 {
		return jobArgs{}, job, errReprocessNoInputs
	}
	return args, job, nil
}

// locateInputs resolves each recorded input path: preferred
// CompletedDir/<basename> (only when completedDir != ""), else the stored
// original path. It preserves srcPaths order and returns the resolved paths
// plus how many came from the completed dir vs. the original path and the
// basenames found in neither. A basename present in BOTH locations counts as
// completed (first candidate wins). Source files only, never the converted
// output (dest_path/output_file).
func locateInputs(srcPaths []string, completedDir string) (resolved []string, foundCompleted, foundOriginal int, missing []string) {
	resolved = make([]string, 0, len(srcPaths))
	for _, p := range srcPaths {
		base := filepath.Base(p)
		if completedDir != "" && isLocatedInput(filepath.Join(completedDir, base)) {
			resolved = append(resolved, filepath.Join(completedDir, base))
			foundCompleted++
			continue
		}
		if isLocatedInput(p) {
			resolved = append(resolved, p)
			foundOriginal++
			continue
		}
		missing = append(missing, base)
	}
	return resolved, foundCompleted, foundOriginal, missing
}

// isLocatedInput reports whether path exists as a REGULAR FILE: a directory
// in the completed dir named like a recorded input must not shadow the
// original file (m4b-merge reads inputs as files), and a recorded path that
// now holds a directory is equally not an input.
func isLocatedInput(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// GetBookReprocess handles GET /api/books/{id}/reprocess — the preview the
// record-page dialog shows before the operator starts: latest merge run
// snapshot + where each recorded input file currently lives.
func (h *Handler) GetBookReprocess(w http.ResponseWriter, r *http.Request) {
	bookID := parseInt64Param(chiURLParam(r, "id"))
	if bookID == -1 {
		writeError(w, http.StatusBadRequest, "invalid book ID")
		return
	}
	// 404 for an unknown book (the record page only links here from an
	// existing book, but the endpoint stays correct standalone).
	var one int64
	if err := h.svc.DB.QueryRow("SELECT 1 FROM books WHERE id = ?", bookID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "book not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "book lookup: "+err.Error())
		return
	}

	resp := map[string]any{"available": true}
	if h.svc.Config != nil {
		resp["completed_dir"] = strings.TrimSpace(h.svc.Config.Config().Directories.CompletedDir)
	}

	args, job, err := h.latestMergeJob(bookID)
	if err == errReprocessNoRun {
		writeJSON(w, http.StatusOK, reprocessUnavailable("No recorded processing run for this book — re-ingest the source to process it."))
		return
	}
	if err == errReprocessNoInputs {
		writeJSON(w, http.StatusOK, reprocessUnavailable("No original input files were recorded for this book — reprocess is not available."))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "latest merge job: "+err.Error())
		return
	}

	// Locate the run's inputs against the FROZEN completed directory recorded
	// on that job (finding: reprocess used only the live global, so a run whose
	// sources moved to its own completed dir — later cleared or re-pointed in
	// Settings — would report the inputs missing even though they remain in the
	// recorded job's directory). The live-global fallback is LEGACY-ROW ONLY
	// (args.Snapshot == nil): a post-cutover row with completion deliberately
	// disabled carries a frozen empty CompletedDir and must keep it — falling
	// back to the live global would locate an unrelated same-basename file in
	// a directory the run never used.
	completedDir := args.CompletedDir
	if args.Snapshot == nil && completedDir == "" {
		completedDir, _ = resp["completed_dir"].(string)
	}
	resolved, foundCompleted, foundOriginal, missing := locateInputs(args.SrcPaths, completedDir)
	found := len(resolved)
	// The wire contract is an array (possibly empty), never null: the dialog
	// reads missing.length directly.
	if missing == nil {
		missing = []string{}
	}

	resp["last_run"] = map[string]any{
		"job_id":        job.ID,
		"created_at":    job.CreatedAt,
		"output_dir":    args.OutputDir,
		"output_scheme": args.OutputScheme,
	}
	resp["inputs"] = map[string]any{
		"total":              len(args.SrcPaths),
		"found":              found,
		"found_in_completed": foundCompleted,
		"found_in_original":  foundOriginal,
		"missing":            missing,
	}
	writeJSON(w, http.StatusOK, resp)
}

// reprocessUnavailable shapes the available:false preview: the operator
// sentence in reason, last_run/inputs omitted entirely.
func reprocessUnavailable(reason string) map[string]any {
	return map[string]any{"available": false, "reason": reason}
}

// StartReprocessRequest is the JSON body for POST /api/process/reprocess.
// Only book_id is required; OutputProfileID and the blank-inherit overrides
// are the same edited-settings surface as the queue page (blanks inherit the
// current global settings at run time).
type StartReprocessRequest struct {
	BookID          int64  `json:"book_id"`
	OutputProfileID *int64 `json:"output_profile_id"`
	OutputDir       string `json:"output_dir"`
	PathFormat      string `json:"path_format"`
}

// StartReprocess handles POST /api/process/reprocess — queues one reprocess
// job for a single book from located original input files.
func (h *Handler) StartReprocess(w http.ResponseWriter, r *http.Request) {
	var req StartReprocessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BookID <= 0 {
		writeError(w, http.StatusBadRequest, "book_id is required")
		return
	}

	// Book row must exist. The endpoint does NOT gate on book status (same
	// as StartProcessing): the record page only offers the action on done
	// books, and the below active-job guard is the operative protection.
	var one int64
	if err := h.svc.DB.QueryRow("SELECT 1 FROM books WHERE id = ?", req.BookID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "book not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "book lookup: "+err.Error())
		return
	}

	if h.svc.ProcessingSvc.hasActiveJobForBook(req.BookID) {
		writeError(w, http.StatusConflict, fmt.Sprintf("book %d already has an active processing job", req.BookID))
		return
	}

	args, _, err := h.latestMergeJob(req.BookID)
	switch {
	case err == errReprocessNoRun:
		writeError(w, http.StatusBadRequest, "No recorded processing run for this book — re-ingest the source to process it.")
		return
	case err == errReprocessNoInputs:
		writeError(w, http.StatusBadRequest, "No original input files were recorded for this book — reprocess is not available.")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "latest merge job: "+err.Error())
		return
	}

	// Locate inputs against the FROZEN completed directory recorded on the run
	// (finding: the live global alone misses a run whose sources moved to its
	// own completed dir after the setting was cleared/re-pointed); a legacy row
	// (args.Snapshot == nil) with no frozen value falls back to the live
	// global. A post-cutover row with completion deliberately disabled carries
	// a frozen empty CompletedDir and must keep it — the live global would
	// locate an unrelated same-basename file in a directory the run never used.
	completedDir := args.CompletedDir
	if args.Snapshot == nil && completedDir == "" && h.svc.Config != nil {
		completedDir = strings.TrimSpace(h.svc.Config.Config().Directories.CompletedDir)
	}
	resolved, foundCompleted, _, missing := locateInputs(args.SrcPaths, completedDir)
	if len(missing) > 0 {
		locations := "in the recorded source paths"
		if completedDir != "" {
			locations = fmt.Sprintf("looked in %s and the recorded source paths", completedDir)
		}
		writeError(w, http.StatusBadRequest, "Original input files not found: "+strings.Join(missing, ", ")+
			" ("+locations+")")
		return
	}

	runOpts, ok := h.resolveDestination(w, req.OutputProfileID, req.OutputDir, req.PathFormat)
	if !ok {
		return
	}
	if !h.probeDestinations(w, runOpts) {
		return
	}

	jobID, err := h.svc.ProcessingSvc.QueueJob(r.Context(), req.BookID, resolved, runOpts)
	switch {
	case errors.Is(err, errActiveJobForBook):
		writeError(w, http.StatusConflict, fmt.Sprintf("book %d already has an active processing job", req.BookID))
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("queue job: %v", err))
		return
	}

	// Keep the provenance of the run visible in the history row. The job is
	// already queued and may already be running: a detail-write failure is a
	// log line, not a 500 that would fake the run as failed.
	detail := fmt.Sprintf("reprocess: %d of %d inputs from completed directory", foundCompleted, len(args.SrcPaths))
	if foundCompleted == 0 {
		detail = "reprocess: inputs from original source paths"
	}
	if _, err := h.svc.DB.Exec("UPDATE processing_jobs SET detail = ? WHERE id = ?", detail, jobID); err != nil {
		log.Printf("record reprocess provenance for job %s: %v", jobID, err)
	}

	writeJSON(w, http.StatusOK, StartProcessingResponse{
		Jobs:         []ProcessingJobCreated{{ID: jobID, BookID: req.BookID}},
		OutputDir:    runOpts.OutputDirOverride,
		OutputScheme: runOpts.PathFormatOverride,
	})
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
		"queue_order":  job.QueueSeq,
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
	// Queue-time destination snapshot (profiled batches only). Decoding is
	// best-effort: audit rows and legacy rows without merge args stay bare.
	if job.M4bMergeArgs != "" {
		var args jobArgs
		if err := json.Unmarshal([]byte(job.M4bMergeArgs), &args); err == nil {
			if args.OutputDir != "" {
				resp["output_dir"] = args.OutputDir
			}
			if args.OutputScheme != "" {
				resp["output_scheme"] = args.OutputScheme
			}
		}
	}
	return resp
}

const jobsColumns = "id, book_id, status, action, detail, output, error, output_file, started_at, completed_at, created_at, m4b_merge_args, COALESCE(queue_seq, rowid) AS queue_seq"

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
	recentQuery += " ORDER BY created_at DESC, queue_seq DESC LIMIT ?"
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
	activeQuery += " ORDER BY queue_seq ASC"
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
		var mergeArgs sql.NullString
		err := rows.Scan(&job.ID, &job.BookID, &job.Status, &job.Action, &job.Detail,
			&job.Output, &job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt, &mergeArgs, &job.QueueSeq)
		if err != nil {
			continue
		}
		job.M4bMergeArgs = mergeArgs.String
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// GetJobStatus handles GET /api/jobs/:id.
func (h *Handler) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chiURLParam(r, "id")

	row := h.svc.DB.QueryRow(
		"SELECT id, book_id, m4b_merge_args, status, action, detail, output, error, output_file, started_at, completed_at, created_at, COALESCE(queue_seq, rowid) FROM processing_jobs WHERE id = ?",
		jobID,
	)
	var job models.ProcessingJob
	var jobArgs sql.NullString // audit rows have no m4b-merge args (details live in the detail column)
	err := row.Scan(&job.ID, &job.BookID, &jobArgs, &job.Status, &job.Action, &job.Detail,
		&job.Output, &job.Error, &job.OutputFile, &job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.QueueSeq)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query job: %v", err))
		return
	}
	job.M4bMergeArgs = jobArgs.String
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
		// No in-memory state; stream DB state (including any output a
		// terminal-but-not-yet-polled row already has).
		initial := map[string]string{"status": status}
		if output != "" {
			initial["output"] = output
		}
		b, _ := json.Marshal(initial)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		return
	}

	// A reconnecting client replaces its local log with the server's
	// accumulated stdout (full snapshot + the offset new lines start at),
	// then follows appended suffixes. Registration happens BEFORE the
	// initial snapshot: a line appended in between races into the snapshot
	// itself, so no output slot is ever lost.
	wake := make(chan struct{}, 1)
	defer state.registerOutputSubscriber(wake)()
	live, offset := state.outputSnapshot()
	initial := map[string]any{"status": status}
	if live != "" {
		initial["output"] = live
	}
	initial["output_offset"] = offset
	b, _ := json.Marshal(initial)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()

	// Stream progress until finalize closes this stream's subscription
	// (finding: F8a). Every stream registers its OWN progress channel, so
	// lifecycle events ("running", "started processing") fan out to all
	// connected tabs — a shared single-consumer channel starved every
	// stream after the first. The terminal line is written after the loop
	// by EVERY stream from the done sync: that is the single source of the
	// true terminal status (e.g. "cancelled" for a cancelled job), so a
	// cancelled job can never end with a spurious {"status":"done"} frame.
	ctx := r.Context()
	progressChan := make(chan string, 4)
	defer state.registerProgressSubscriber(progressChan)()
	var lastProgress string
loop:
	for {
		select {
		case msg, ok := <-progressChan:
			if !ok {
				break loop
			}
			lastProgress = msg
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-wake:
			suffix, next := state.outputFrom(offset)
			if suffix == "" {
				// Coalesced wake whose bytes arrived with the snapshot, or a
				// heartbeat-shaped nudge: the reader state is already current.
				offset = next
				break
			}
			offset = next
			b, _ := json.Marshal(map[string]any{"output": suffix, "output_offset": offset})
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-time.After(30 * time.Second):
			// Periodic heartbeat
			fmt.Fprintf(w, "data: %s\n\n", `{"status":"alive"}`)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}

	// Drain the buffer before awaiting the terminal: a wake for the last
	// appended line may already be pending when select picks the closed
	// progress channel and breaks the loop — the connection must never end
	// mid-log, so emit whatever bytes were appended after this stream's
	// cursor before the terminal sequence starts.
	if suffix, next := state.outputFrom(offset); suffix != "" {
		b, _ := json.Marshal(map[string]any{"output": suffix, "output_offset": next})
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// Write this stream's own terminal status line. Every concurrent
	// stream does this — the done sync is a broadcast, so each stream
	// waits its turn and never blocks on a peer consuming the value.
	// The line carries the TRUE terminal status parsed from the terminal
	// progress message (finding: F8b): a cancelled job ends with
	// {"status":"cancelled"}, never a spurious {"status":"done"}.
	doneMsg, doneErr := state.doneSync.await()
	// A stream that registered AFTER finalize (or whose buffered slot
	// dropped the fan-out) never saw the terminal PROGRESS line in the
	// loop: emit it from the done sync so every stream ends with the same
	// terminal progress + status pair. Streams that already consumed the
	// line in the loop are deduped by lastProgress.
	if doneMsg != "" && doneMsg != lastProgress {
		fmt.Fprintf(w, "data: %s\n\n", doneMsg)
		flusher.Flush()
	}
	final := map[string]any{}
	var terminal struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(doneMsg), &terminal) == nil && terminal.Status != "" {
		final["status"] = terminal.Status
	} else {
		final["status"] = jobStatusDone
	}
	if doneErr != nil {
		final["message"] = doneErr.Error()
	}
	b, _ = json.Marshal(final)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// outputFormat returns the container format of a produced output file, e.g.
// "m4b", for the book's FILE PATHS card. The engine publishes .m4b audio,
// so the extension is the real format rather than a guess from arbitrary
// user input.
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
