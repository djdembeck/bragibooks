package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/models"
)

// Bounds of the input-directory index scan. Depth and entry caps guard a
// hostile or accidental tree; hitting either truncates the walk (and the
// scan reports it) instead of running unbounded.
const (
	indexMaxDepth   = 30
	indexMaxEntries = 20000
	// indexWriteBatch bounds every bulk write: a 20 000-row single
	// transaction would pin the SQLite writer past the 5 s busy timeout
	// while concurrent Settings/intake writes wait.
	indexWriteBatch = 500
	// indexScanBudget bounds the per-scan wall time spent in ffprobe
	// extraction; paths not extracted before the budget is used are
	// stored degraded and re-attempted on the next refresh.
	indexScanBudget = 30 * time.Minute
)

// IndexState describes the last completed (or in-flight) index scan.
type IndexState struct {
	Root       string `json:"root"`
	Entries    int    `json:"entries"`
	LastRunAt  string `json:"last_run_at"` // "" if never
	Status     string `json:"status"`      // "never"|"queued"|"running"|"done"|"truncated"|"interrupted"|"error"
	Detail     string `json:"detail"`
	Generation int64  `json:"generation"` // incremented per accepted scan; poll until terminal for this generation
}

// IndexStatus is the wire shape of GET /api/index.
type IndexStatus struct {
	Enabled bool       `json:"enabled"`
	State   IndexState `json:"state"`
}

// IndexEvidence is the per-path projection served by
// GET /api/directories?...&index=1. It is derived on read from the stored
// audio.SourceMetadata; it is NOT what is persisted (the stored blob is the
// full snapshot, so intake can reuse it and a future predicted-match block
// has a home in the same contract).
type IndexEvidence struct {
	ASIN  *models.SourceASIN `json:"asin,omitempty"`  // deriveSourceASIN(snap); nil ONLY for a nil/undecodable snapshot
	Title string             `json:"title,omitempty"` // deriveTitle(path)
}

// IndexService is a single-goroutine, mutex-serialized scanner of the
// input directory. It maintains the source_index table (one row per path,
// pre-extracted audio.SourceMetadata payload) and the index_state row.
// Pending refresh requests coalesce into one scan via the cap-1 req
// channel; the generation counter lets a caller poll for ITS scan's
// terminal state without misreading a prior scan's.
type IndexService struct {
	db         *sql.DB
	cfg        *config.ConfigManager
	ffprobe    string
	mu         sync.Mutex
	req        chan struct{} // cap 1: coalesce pending refresh requests
	pending    bool          // a request has been accepted but scan() has not consumed it
	activeRoot string        // the root scan() is currently walking (or "" when idle)
	done       chan struct{} // closed when Run returns; Wait() blocks on it
	last       IndexState    // in-memory mirror of index_state for fast reads
}

// NewIndexService builds the service and seeds the in-memory state mirror
// from the persisted index_state row (a crashed prior run leaves its last
// terminal — or "running"/"queued" — state there; a new boot supersedes it
// on the next scan, and a terminal mirror is what Status() reports).
func NewIndexService(db *sql.DB, cfg *config.ConfigManager, ffprobe string) *IndexService {
	s := &IndexService{
		db:      db,
		cfg:     cfg,
		ffprobe: ffprobe,
		req:     make(chan struct{}, 1),
		done:    make(chan struct{}),
		last:    IndexState{Status: "never"},
	}
	if err := s.loadState(); err != nil {
		log.Printf("index: load state: %v", err)
	}
	return s
}

// Enabled reports the live opt-in flag (same value GET /api/settings
// reports; no separate DB read).
func (s *IndexService) Enabled() bool {
	return s.cfg.Config().Directories.IndexingEnabled
}

// RequestScan enqueues a scan (coalesced with any queued one) and returns
// the status to poll. The returned State.Generation is stable for the
// accepted scan: a second caller that coalesces into the same scan sees
// the same generation, so both can poll to its terminal state.
func (s *IndexService) RequestScan() IndexStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	root := canonicalRoot(s.cfg.Config().Directories.InputDir)
	terminal := s.last.Status != "running" && s.last.Status != "queued"
	switch {
	case terminal:
		// Idle: accept a fresh scan.
		s.last.Generation++
		s.last.Status = "queued"
		s.persistLocked()
		s.sendLocked()
	case s.last.Status == "queued":
		// Already queued: coalesce into the pending scan. The generation
		// must NOT increment — the first caller is polling for it.
	case s.activeRoot != root:
		// Running on a different root: the input root changed mid-scan,
		// so queue a rescan for the new root. activeRoot is only
		// non-empty while scan() is mid-walk, so this is precise.
		s.last.Generation++
		s.pendingFlagLocked()
		s.persistLocked()
		s.sendLocked()
	default:
		// Running on the same root: coalesce.
	}
	return s.statusLocked()
}

// pendingFlagLocked records that a rescan must run after the current one;
// it is consumed by the worker's next req receive.
func (s *IndexService) pendingFlagLocked() {
	s.pending = true
}

// sendLocked does the non-blocking cap-1 enqueue.
func (s *IndexService) sendLocked() {
	select {
	case s.req <- struct{}{}:
	default:
	}
}

// Status returns the in-memory mirror, authoritative while the process is
// up.
func (s *IndexService) Status() IndexStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *IndexService) statusLocked() IndexStatus {
	return IndexStatus{Enabled: s.Enabled(), State: s.last}
}

// Run is the worker loop: one bounded scan per accepted request. Started
// as `go indexSvc.Run(ctx)` from main with the signal-derived ctx; a ctx
// cancel stops the in-flight scan (recorded "interrupted") and returns.
func (s *IndexService) Run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			s.markInterrupted()
			return
		case <-s.req:
			s.pending = false
			s.scan(ctx)
		}
	}
}

// Wait blocks until the worker loop has returned; main calls it after
// srv.Shutdown and before the DB close so the close never lands under a
// live index writer.
func (s *IndexService) Wait() {
	<-s.done
}

// markInterrupted records the terminal state for an accepted generation
// (running or queued) that ended by shutdown instead of by scan outcome.
// A queued-but-unconsumed request dying on ctx.Done() and a scan cancelled
// mid-walk both end here, so no generation is left without a terminal.
func (s *IndexService) markInterrupted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.Status == "running" || s.last.Status == "queued" {
		s.last.Status = "interrupted"
		s.last.Detail = "shutdown"
		s.persistLocked()
	}
}

// indexRow is one walked path plus its change-detection signals.
type indexRow struct {
	Path    string
	Name    string
	Type    string
	Size    int64
	ModTime string
	Payload string // set by extract; empty for unchanged rows (keep stored)
}

// scan performs one full-or-incremental walk of the input root and
// upserts/prunes the index. All status transitions are persisted.
func (s *IndexService) scan(ctx context.Context) {
	root := canonicalRoot(s.cfg.Config().Directories.InputDir)

	s.mu.Lock()
	s.last.Status = "running"
	s.last.Detail = ""
	s.activeRoot = root
	s.persistLocked()
	s.mu.Unlock()

	if _, err := os.Stat(root); err != nil {
		s.setTerminal("error", err.Error(), 0, root)
		return
	}

	rows, truncated, detail, cancelled := s.walk(ctx, root)
	if cancelled {
		s.markInterrupted()
		return
	}

	// Root change: clear old-root rows before treating everything as new.
	// Separator-aware prefix + bounded batches (one giant DELETE would pin
	// the writer past the busy timeout).
	if oldRoot, changed := s.storeRoot(); changed && oldRoot != root {
		if err := s.deletePrefix(oldRoot); err != nil {
			log.Printf("index: clear old root %s: %v", oldRoot, err)
		}
	}

	walked := make(map[string]bool, len(rows))
	for _, r := range rows {
		walked[r.Path] = true
	}

	// Prune only a complete walk: an incomplete walked-set would delete
	// previously indexed paths beyond the cutoff.
	if !truncated {
		if err := s.pruneVanished(walked); err != nil {
			log.Printf("index: prune: %v", err)
		}
	}

	// Classify walked rows: unchanged (size+modtime match AND stored
	// snapshot complete) vs needs-extraction (new, changed, or
	// stored-incomplete — degraded rows re-attempt on every refresh).
	unchanged, extract := s.classify(rows)

	// Extract the changed/new/incomplete set, bounded by the scan budget.
	budgetStart := time.Now()
	degraded := 0
	for i := range extract {
		if ctx.Err() != nil {
			// Cancellation mid-extraction: stop; the remaining paths stay
			// stored-incomplete and the next refresh retries them.
			s.markInterrupted()
			return
		}
		if time.Since(budgetStart) > indexScanBudget {
			log.Printf("index: scan of %s finished, %d paths not extracted (budget)", root, len(extract)-i)
			break
		}
		snap := s.extractOne(ctx, extract[i].Path)
		extract[i].Payload = mustJSON(snap)
	}
	for i, r := range extract {
		if r.Payload == "" {
			// Not extracted (budget exhausted): the degraded snapshot is
			// the honest representation of "extraction not run".
			extract[i].Payload = mustJSON(audio.DegradedSourceMetadata("index scan budget exhausted; extraction not run"))
			degraded++
		}
	}

	if err := s.upsertAll(append(unchanged, extract...)); err != nil {
		log.Printf("index: upsert: %v", err)
	}

	status := "done"
	if truncated {
		status = "truncated"
	}
	if degraded > 0 && status == "done" {
		detail = fmt.Sprintf("done with %d paths not extracted (budget)", degraded)
	}
	s.setTerminal(status, detail, len(rows), root)
}

// setTerminal records and persists a terminal state for the generation.
func (s *IndexService) setTerminal(status, detail string, entries int, root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last.Status = status
	s.last.Detail = detail
	s.last.Entries = entries
	s.last.Root = root
	s.last.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	s.persistLocked()
}

// walk collects the index rows for the tree under root, applying the
// depth/entry caps and skipping dotfiles (matching readDirectory).
func (s *IndexService) walk(ctx context.Context, root string) (rows []indexRow, truncated bool, detail string, cancelled bool) {
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			cancelled = true
			return filepath.SkipAll
		}
		if walkErr != nil {
			truncated = true
			detail = fmt.Sprintf("walk error: %v", walkErr)
			return filepath.SkipAll
		}
		depth, _ := filepath.Rel(root, p)
		if depth == "." {
			return nil // root itself
		}
		if strings.Count(depth, string(os.PathSeparator))+1 > indexMaxDepth {
			if !truncated {
				truncated = true
				detail = fmt.Sprintf("depth cap %d exceeded", indexMaxDepth)
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(rows) >= indexMaxEntries {
			truncated = true
			detail = fmt.Sprintf("entry cap %d exceeded", indexMaxEntries)
			return filepath.SkipAll
		}
		row := indexRow{Path: p, Name: d.Name(), Type: "file"}
		if d.IsDir() {
			row.Type = "dir"
		} else if info, err := d.Info(); err == nil {
			row.Size = info.Size()
			row.ModTime = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		truncated = true
		detail = fmt.Sprintf("walk error: %v", err)
	}
	return rows, truncated, detail, cancelled
}

// storeRoot returns the persisted index root and whether it differs from
// an empty (never-scanned) root.
func (s *IndexService) storeRoot() (string, bool) {
	var root string
	var gen int64
	if err := s.db.QueryRow(`SELECT root, generation FROM index_state WHERE id = 1`).Scan(&root, &gen); err != nil {
		return "", false
	}
	return root, root != ""
}

// deletePrefix removes all index rows under oldRoot (separator-aware:
// "root" itself plus "root/..."), in bounded batches.
func (s *IndexService) deletePrefix(oldRoot string) error {
	sep := string(os.PathSeparator)
	for {
		rows, err := s.db.Query(`SELECT path FROM source_index WHERE path = ? OR path LIKE ? ESCAPE '\' LIMIT ?`,
			oldRoot, likeEscape(oldRoot+sep+"%"), indexWriteBatch)
		if err != nil {
			return err
		}
		var paths []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			paths = append(paths, p)
		}
		rows.Close()
		if len(paths) == 0 {
			return nil
		}
		if _, err := s.db.Exec(fmt.Sprintf("DELETE FROM source_index WHERE path IN (%s)", placeholders(len(paths))), toArgs(paths)...); err != nil {
			return err
		}
		if len(paths) < indexWriteBatch {
			return nil
		}
	}
}

// pruneVanished deletes rows for paths the complete walk did not see.
func (s *IndexService) pruneVanished(walked map[string]bool) error {
	var vanished []string
	rows, err := s.db.Query(`SELECT path FROM source_index`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if !walked[p] {
			vanished = append(vanished, p)
		}
	}
	rows.Close()
	for start := 0; start < len(vanished); start += indexWriteBatch {
		end := min(start+indexWriteBatch, len(vanished))
		batch := vanished[start:end]
		if _, err := s.db.Exec(fmt.Sprintf("DELETE FROM source_index WHERE path IN (%s)", placeholders(len(batch))), toArgs(batch)...); err != nil {
			return err
		}
	}
	return nil
}

// classify splits walked rows into unchanged (kept, seen_at bump only) and
// needs-extraction. A row is unchanged iff size and modtime match AND its
// stored snapshot is complete; degraded/incomplete rows are re-extracted
// on every refresh (self-healing).
func (s *IndexService) classify(rows []indexRow) (unchanged, extract []indexRow) {
	for start := 0; start < len(rows); start += indexWriteBatch {
		end := min(start+indexWriteBatch, len(rows))
		batch := rows[start:end]
		q := fmt.Sprintf(`SELECT path, size, modtime, payload FROM source_index WHERE path IN (%s)`, placeholders(len(batch)))
		dbRows, err := s.db.Query(q, toArgs(pathsOf(batch))...)
		if err != nil {
			log.Printf("index: classify query: %v", err)
			extract = append(extract, batch...)
			continue
		}
		type stored struct {
			Size    int64
			ModTime string
			Payload string
		}
		existing := make(map[string]stored, len(batch))
		for dbRows.Next() {
			var st stored
			var p string
			if err := dbRows.Scan(&p, &st.Size, &st.ModTime, &st.Payload); err != nil {
				dbRows.Close()
				extract = append(extract, batch...)
				break
			}
			existing[p] = st
		}
		dbRows.Close()
		changed := false
		for _, r := range batch {
			st, ok := existing[r.Path]
			if ok && st.Size == r.Size && st.ModTime == r.ModTime {
				var snap audio.SourceMetadata
				if json.Unmarshal([]byte(st.Payload), &snap) == nil && sourceMetadataComplete(&snap) {
					unchanged = append(unchanged, r)
					continue
				}
			}
			extract = append(extract, r)
			changed = true
		}
		_ = changed
	}
	return unchanged, extract
}

// extractOne runs the existing extractor (or the degraded cause-string
// form when ffprobe is unavailable). It never returns nil.
func (s *IndexService) extractOne(ctx context.Context, path string) *audio.SourceMetadata {
	if s.ffprobe == "" {
		return audio.DegradedSourceMetadata("ffprobe binary unavailable; extraction not run")
	}
	snap, err := audio.ExtractSourceMetadata(ctx, s.ffprobe, path)
	if err != nil || snap == nil {
		log.Printf("index: extract %s: %v", path, err)
		return audio.DegradedSourceMetadata(fmt.Sprintf("extraction failed: %v", err))
	}
	return snap
}

// upsertAll writes the rows in bounded transactions. Unchanged rows carry
// an empty Payload, meaning "keep the stored payload, bump seen_at".
func (s *IndexService) upsertAll(rows []indexRow) error {
	for start := 0; start < len(rows); start += indexWriteBatch {
		end := min(start+indexWriteBatch, len(rows))
		batch := rows[start:end]
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, r := range batch {
			if r.Payload == "" {
				// Unchanged: keep payload, bump seen_at.
				if _, err := tx.Exec(
					`UPDATE source_index SET seen_at = datetime('now') WHERE path = ?`, r.Path); err != nil {
					tx.Rollback()
					return err
				}
			} else {
				if _, err := tx.Exec(`
					INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
					VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
					ON CONFLICT(path) DO UPDATE SET
						name = excluded.name,
						type = excluded.type,
						size = excluded.size,
						modtime = excluded.modtime,
						payload = excluded.payload,
						indexed_at = datetime('now'),
						seen_at = datetime('now')`,
					r.Path, r.Name, r.Type, r.Size, r.ModTime, r.Payload); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// loadState seeds the in-memory mirror from the persisted row.
func (s *IndexService) loadState() error {
	var st IndexState
	var gen int64
	var lastRunAt sql.NullString
	err := s.db.QueryRow(`SELECT root, entries, last_run_at, status, detail, generation FROM index_state WHERE id = 1`).
		Scan(&st.Root, &st.Entries, &lastRunAt, &st.Status, &st.Detail, &gen)
	if err == sql.ErrNoRows {
		s.persistLocked()
		return nil
	}
	if err != nil {
		return err
	}
	st.LastRunAt = lastRunAt.String
	st.Generation = gen
	// Crash recovery: a process killed without a graceful shutdown can
	// leave a non-terminal status persisted (the mirror is written at
	// request time, before the scan's terminal write). Treat it as
	// interrupted so the next refresh supersedes it instead of a future
	// RequestScan coalescing into the dead generation and never running.
	if st.Status == "running" || st.Status == "queued" {
		st.Status = "interrupted"
		st.Detail = "previous run ended abruptly"
		_, _ = s.db.Exec(
			`UPDATE index_state SET status = ?, detail = ? WHERE id = 1`,
			st.Status, st.Detail,
		)
	}
	s.last = st
	return nil
}

// persistLocked writes the mirror to index_state. Must hold s.mu.
func (s *IndexService) persistLocked() {
	_, err := s.db.Exec(`
		INSERT INTO index_state (id, root, entries, last_run_at, status, detail, generation)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			root = excluded.root,
			entries = excluded.entries,
			last_run_at = COALESCE(NULLIF(excluded.last_run_at, ''), index_state.last_run_at),
			status = excluded.status,
			detail = excluded.detail,
			generation = excluded.generation`,
		s.last.Root, s.last.Entries, nullableString(s.last.LastRunAt), s.last.Status, s.last.Detail, s.last.Generation)
	if err != nil {
		log.Printf("index: persist state: %v", err)
	}
}

// canonicalRoot is the one canonical form every index-mode path handling
// uses: absolute, cleaned, tilde-expanded. A relative root (".") and a
// trailing-separator root collapse to the same string the scan stored.
func canonicalRoot(p string) string {
	abs, err := filepath.Abs(filepath.Clean(expandTilde(p)))
	if err != nil {
		return p
	}
	return abs
}

// indexChildPrefix is the LIKE prefix for a directory's children:
// dir + separator, without doubling at the filesystem root ("//" never).
func indexChildPrefix(dir string) string {
	if dir == string(os.PathSeparator) {
		return dir
	}
	return dir + string(os.PathSeparator)
}

// indexImmediateChild reports whether candidate sits directly under dir.
func indexImmediateChild(dir, candidate string) bool {
	sep := string(os.PathSeparator)
	if dir == sep {
		return strings.HasPrefix(candidate, sep) && !strings.Contains(candidate[1:], sep)
	}
	return strings.HasPrefix(candidate, dir+sep) && !strings.Contains(candidate[len(dir)+1:], sep)
}

// likeEscape escapes the LIKE wildcards in a literal prefix.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func placeholders(n int) string {
	return strings.Repeat("?,", n-1) + "?"
}

func toArgs(ss []string) []any {
	args := make([]any, len(ss))
	for i, s := range ss {
		args[i] = s
	}
	return args
}

func pathsOf(rows []indexRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Path
	}
	return out
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// deriveIndexEvidence builds the per-path read projection from a stored
// payload. A decode failure leaves the evidence nil (the caller logs).
func deriveIndexEvidence(payload, path string) *IndexEvidence {
	if payload == "" || payload == "{}" {
		return nil
	}
	var snap audio.SourceMetadata
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		return nil
	}
	return &IndexEvidence{
		ASIN:  deriveSourceASIN(&snap),
		Title: deriveTitle(path),
	}
}

// GetIndexStatus handles GET /api/index. 200 even when disabled.
func (h *Handler) GetIndexStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.IndexSvc.Status())
}

// RefreshIndex handles POST /api/index/refresh. Returns 202 with the
// status to poll: State.Generation is the generation the caller polls for
// (a stale terminal state from a prior scan cannot be misread as this
// request's completion).
func (h *Handler) RefreshIndex(w http.ResponseWriter, r *http.Request) {
	if !h.svc.IndexSvc.Enabled() {
		writeError(w, http.StatusBadRequest, "indexing is disabled")
		return
	}
	st := h.svc.IndexSvc.RequestScan()
	writeJSON(w, http.StatusAccepted, st)
}

// listIndexedChildren serves one level of the input directory from the
// index table, returning entries with their pre-extracted evidence.
func (h *Handler) listIndexedChildren(dir string) ([]IndexedDirectoryEntry, bool, error) {
	root := canonicalRoot(h.svc.Config.Config().Directories.InputDir)
	if dir != root && !strings.HasPrefix(dir, indexChildPrefix(root)) {
		return nil, false, nil // not under the root
	}
	q := fmt.Sprintf(`SELECT path, name, type, size, modtime, payload FROM source_index WHERE path LIKE ? ESCAPE '\'`)
	rows, err := h.svc.DB.Query(q, likeEscape(indexChildPrefix(dir))+"%")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []IndexedDirectoryEntry
	decodeFail := 0
	for rows.Next() {
		var (
			path, name, typ, payload string
			size                     int64
			modtime                  string
		)
		if err := rows.Scan(&path, &name, &typ, &size, &modtime, &payload); err != nil {
			return nil, false, err
		}
		if !indexImmediateChild(dir, path) {
			continue
		}
		e := DirectoryEntry{Name: name, Type: typ, Path: path, Size: size, ModTime: modtime}
		ie := IndexedDirectoryEntry{DirectoryEntry: e}
		ev := deriveIndexEvidence(payload, path)
		if ev == nil && payload != "" && payload != "{}" {
			decodeFail++
		}
		ie.Evidence = ev
		out = append(out, ie)
	}
	if decodeFail > 0 {
		log.Printf("index: %d entries with undecodable payloads in %s", decodeFail, dir)
	}
	// Same rule as the page's filesystem listing: dirs first, name
	// ascending.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	if out == nil {
		out = []IndexedDirectoryEntry{}
	}
	return out, true, nil
}

// IndexedDirectoryEntry extends DirectoryEntry with the per-path evidence
// projection served in index mode.
type IndexedDirectoryEntry struct {
	DirectoryEntry
	Evidence *IndexEvidence `json:"evidence,omitempty"`
}

// IndexedDirectoriesResponse is the JSON shape of
// GET /api/directories?...&index=1 in index mode.
type IndexedDirectoriesResponse struct {
	Path    string                  `json:"path"`
	Entries []IndexedDirectoryEntry `json:"entries"`
	Index   *IndexState             `json:"index,omitempty"` // last_run_at, entries, status
}
