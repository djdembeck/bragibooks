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
// scan reports it) instead of running unbounded. The depth cap also stops
// descending into over-depth directories (filepath.SkipDir), so their
// contents are never visited.
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
	// Transient in-memory progress for a running scan. NOT persisted: progress
	// only means something while this process's scan is in flight, and a
	// restart interrupts the scan, so a freshly-seeded zero is honest.
	// Total = paths needing (re-)extraction this scan; Extracted = how many
	// of those are done. Total 0 while the (fast) walk is in flight; both
	// read 0 when idle.
	Extracted int `json:"extracted,omitempty"`
	Total     int `json:"total,omitempty"`
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
	db      *sql.DB
	cfg     *config.ConfigManager
	ffprobe string
	mu      sync.Mutex
	req     chan struct{} // cap 1: coalesce pending refresh requests
	// nextGen is the generation the worker must run on its next req
	// receive. It decouples "the generation a scan publishes" from
	// last.Generation, which a mid-scan root change may have already
	// bumped for the queued rescan — the running scan must still
	// terminate under ITS OWN generation.
	nextGen    int64
	activeRoot string        // root the accepted (active or queued) scan will target
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

// CurrentRoot is the canonical (symlink-resolved) form of the configured
// input dir — the same form scan() stores as IndexState.Root. "" when the
// configured dir is missing/invalid. Readers that compare stored-root vs
// configured-root MUST use this, not canonicalRoot of the raw setting.
func (s *IndexService) CurrentRoot() string {
	return s.resolveScanRoot()
}

// RequestScan enqueues a scan (coalesced with any queued one) and returns
// the status to poll. The returned State.Generation is stable for the
// accepted scan: callers that coalesce into the same scan (a repeated
// refresh, or a refresh landing while a rescan is already queued) see the
// same generation, so all can poll to its terminal state. A root change
// mid-scan queues exactly one rescan generation; repeated requests in that
// window coalesce into it instead of bumping the generation out from
// under its first caller.
func (s *IndexService) RequestScan() IndexStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	root := canonicalRoot(s.cfg.Config().Directories.InputDir)
	switch {
	case s.last.Status != "running" && s.last.Status != "queued":
		// Idle: accept a fresh scan.
		s.last.Generation++
		s.nextGen = s.last.Generation
		s.last.Status = "queued"
		s.activeRoot = root
		s.persistLocked()
		s.sendLocked()
	case s.last.Status == "queued":
		// A rescan (or first scan) is already queued: coalesce into that
		// stable generation — never increment while a caller polls for it.
	case s.activeRoot == root:
		// Running on the same root: coalesce into the in-flight scan.
	default:
		// Running on a different root: the input root changed mid-scan.
		// Queue ONE rescan for the new root; nextGen stays stable under
		// repeated requests (the "queued" case above), so the rescan's
		// generation cannot move under its first caller. The in-flight
		// scan keeps its own generation via its captured nextGen.
		s.last.Generation++
		s.nextGen = s.last.Generation
		s.last.Status = "queued"
		s.activeRoot = root
		s.persistLocked()
		s.sendLocked()
	}
	return s.statusLocked()
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
// cancel stops the in-flight scan (recorded "interrupted") and returns. On
// that shutdown path, a rescan token that is still queued (a newer accepted
// generation whose scan never started) is also marked interrupted, so every
// accepted generation reaches a terminal before exit.
func (s *IndexService) Run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			gen := s.last.Generation
			// A queued rescan token (a mid-scan root change bumped a newer
			// accepted generation) dies here too. The running scan's
			// terminal may have already regressed the mirror to its OWN
			// generation, leaving the rescan's generation terminal-less —
			// so interrupt the HIGHEST accepted generation, whichever
			// generation the mirror currently sits on. Non-blocking drain:
			// the usual shutdown has no token queued, and this must not
			// block the worker (let alone hold the mutex while waiting).
			select {
			case <-s.req:
				if s.nextGen > gen {
					gen = s.nextGen
				}
			default:
			}
			s.mu.Unlock()
			s.markInterrupted(gen)
			return
		case <-s.req:
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
// (running or queued) that ended by shutdown (or cancellation mid-walk)
// instead of by scan outcome. A queued-but-unconsumed request dying on
// ctx.Done() and a scan cancelled mid-walk both end here, so no generation
// is left without a terminal.
//
// It also advances a mirror that sits on an OLDER generation's terminal:
// the running scan's done/truncated terminal can regress the mirror to
// its own generation while a newer rescan generation is still queued, and
// the shutdown path interrupts the rescan's generation. A terminal is
// superseded by a later generation's terminal the same way a rescan
// supersedes a scan (the old poller already sees the later terminal), so
// overwriting it keeps every accepted generation terminal without
// touching an equal-or-newer published terminal.
func (s *IndexService) markInterrupted(gen int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.Status == "running" || s.last.Status == "queued" || s.last.Generation < gen {
		s.last.Generation = gen
		s.last.Status = "interrupted"
		s.last.Detail = "shutdown"
		s.last.Extracted = 0
		s.last.Total = 0
		s.activeRoot = ""
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
// upserts/prunes the index. All status transitions are persisted. Each
// accepted generation paired with a req-channel token runs exactly one
// such scan: RequestScan sets nextGen and sends a token together, and
// coalesced requests set neither, so this scan captures ITS OWN
// generation at start and publishes its terminal under it — even if a
// mid-scan root change has already bumped last.Generation for the queued
// rescan.
func (s *IndexService) scan(ctx context.Context) {
	s.mu.Lock()
	// Capture the generation this token accepted. nextGen is the accepted
	// scan's; a queued rescan (last.Generation > nextGen) runs on its own
	// token after this one.
	gen := s.nextGen
	// Publish "running" at the captured generation WITHOUT touching
	// root/entries: a failed scan must not publish its attempted root as
	// authoritative (the listing guard would then serve stale/empty rows
	// for it). Only a terminal done/truncated scan publishes the root.
	// Progress counters restart: each scan reports its own extraction.
	s.last.Generation = gen
	s.last.Status = "running"
	s.last.Detail = ""
	s.last.Extracted = 0
	s.last.Total = 0
	s.persistLocked()
	s.mu.Unlock()

	root := s.resolveScanRoot()
	if root == "" {
		s.mu.Lock()
		missing := s.last.Root
		s.mu.Unlock()
		s.setTerminal(gen, "error", "input directory not found", 0, missing)
		return
	}

	rows, truncated, detail, cancelled := s.walk(ctx, root)
	if cancelled {
		s.markInterrupted(gen)
		return
	}

	// Root change: clear old-root rows before treating everything as new.
	// Separator-aware prefix + bounded batches (one giant DELETE would pin
	// the writer past the busy timeout). A failure here is terminal —
	// the mixed old/new rows would be served as the index.
	if oldRoot, changed := s.storeRoot(); changed && oldRoot != root {
		if err := s.deletePrefix(oldRoot); err != nil {
			s.setTerminal(gen, "error", fmt.Sprintf("clear old root %s: %v", oldRoot, err), 0, s.last.Root)
			return
		}
	}

	walked := make(map[string]bool, len(rows))
	for _, r := range rows {
		walked[r.Path] = true
	}

	// Prune only a complete walk: an incomplete walked-set would delete
	// previously indexed paths beyond the cutoff. Failure is terminal:
	// vanished rows would be served as if still present.
	if !truncated {
		if err := s.pruneVanished(walked); err != nil {
			s.setTerminal(gen, "error", fmt.Sprintf("prune: %v", err), 0, s.last.Root)
			return
		}
	}

	// Classify walked rows: unchanged (size+modtime match AND stored
	// snapshot complete) vs needs-extraction (new, changed, or
	// stored-incomplete — degraded rows re-attempt on every refresh).
	unchanged, extract := s.classify(rows, root)

	// Extraction is the slow phase (one ffprobe per path, up to a 30-minute
	// budget); the walk that just finished is fast. Publish the total so the
	// UI can show "X of N" once extraction starts, then tick Extracted as
	// paths complete. Both are transient in-memory progress (see IndexState).
	s.mu.Lock()
	s.last.Total = len(extract)
	s.mu.Unlock()

	// Extract the changed/new/incomplete set, bounded by the scan budget.
	// ffprobe runs at most ONCE per audio file: a file that is a direct
	// child of a directory extracted in this pass is NOT probed on its own —
	// the folder pass already read its tags, so the file's snapshot is
	// derived from the parent's (audio.FileSnapshotFromFolder).
	//
	// Safe because classify marks a parent dirty when any child is
	// new/changed/vanished, so a file needing extraction always has its
	// parent extracted in the same pass; extract is in path order (a
	// directory sorts before its children), so the parent is processed
	// first. A file at the input root (no indexed parent row) or under an
	// incomplete parent (truncated pass) probes on its own.
	//
	// Non-audio files keep the pre-built complete (empty) snapshot classify
	// set — there is nothing for ffprobe to probe, and the empty complete
	// row is what makes them classify stable on every later refresh.
	snapshotByParent := make(map[string]*audio.SourceMetadata, len(extract))
	budgetStart := time.Now()
	degraded := 0
	for i := range extract {
		if ctx.Err() != nil {
			// Cancellation mid-extraction: stop; the remaining paths stay
			// stored-incomplete and the next refresh retries them.
			s.markInterrupted(gen)
			return
		}
		if time.Since(budgetStart) > indexScanBudget {
			log.Printf("index: scan of %s finished, %d paths not extracted (budget)", root, len(extract)-i)
			break
		}
		r := &extract[i]
		switch {
		case r.Type == "file" && !audio.IsAudioFile(r.Name):
			// Keep the pre-set empty snapshot; never run ffprobe on it.
		case r.Type == "file":
			if parent, ok := snapshotByParent[filepath.Dir(r.Path)]; ok {
				r.Payload = mustJSON(audio.FileSnapshotFromFolder(parent, r.Path))
			} else {
				r.Payload = mustJSON(s.extractOne(ctx, r.Path))
			}
		default:
			snap := s.extractOne(ctx, r.Path)
			r.Payload = mustJSON(snap)
			if snap.Complete() {
				snapshotByParent[r.Path] = snap
			}
		}
		s.mu.Lock()
		s.last.Extracted = i + 1
		s.mu.Unlock()
	}
	for i, r := range extract {
		if r.Payload == "" {
			// Not extracted (budget exhausted): the degraded snapshot is
			// the honest representation of "extraction not run".
			extract[i].Payload = mustJSON(audio.DegradedSourceMetadata("index scan budget exhausted; extraction not run"))
			degraded++
		}
	}

	// A persistence failure is terminal "error": the walk's rows are NOT
	// stored, so publishing done would claim an index the table does not
	// have (and the attempted root must not become authoritative).
	if err := s.upsertAll(append(unchanged, extract...)); err != nil {
		s.setTerminal(gen, "error", fmt.Sprintf("persist index: %v", err), 0, s.last.Root)
		return
	}

	status := "done"
	if truncated {
		status = "truncated"
	}
	if degraded > 0 {
		detail = appendDetail(detail, fmt.Sprintf("%d paths not extracted (budget)", degraded))
	}
	s.setTerminal(gen, status, detail, len(rows), root)
}

// resolveScanRoot returns the canonical directory to scan, or "" when the
// configured input dir is missing/invalid. "" configured is an error, not
// the process cwd. A symlinked root is resolved so WalkDir (which never
// follows a symlinked root) sees the real directory: the resolved path is
// stored, so canonicalRoot-comparing readers must compare the same form.
func (s *IndexService) resolveScanRoot() string {
	p := strings.TrimSpace(s.cfg.Config().Directories.InputDir)
	if p == "" {
		return ""
	}
	root := canonicalRoot(p)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		log.Printf("index: stat %s: %v", root, err)
		return ""
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		if err != nil {
			log.Printf("index: stat %s: %v", resolved, err)
		}
		return ""
	}
	return resolved
}

// appendDetail joins a non-empty extra message onto a detail string so a
// truncated walk and a budget-exhausted extraction both stay visible.
func appendDetail(detail, extra string) string {
	if detail == "" {
		return extra
	}
	return detail + "; " + extra
}

// setTerminal records and persists a terminal state for the given
// generation. A scan passes the generation it captured at start: if a
// mid-scan root change already bumped last.Generation for the queued
// rescan, this publish (temporarily) regresses the mirror to the scan's
// own generation so its pollers reach a terminal; the rescan's own token
// then advances the mirror to the new root's terminal. It clears
// activeRoot: the worker is no longer mid-walk, so a later RequestScan
// must not treat a stale root as "scan in flight on a different root"
// and bump the generation out from under a coalesced caller.
func (s *IndexService) setTerminal(gen int64, status, detail string, entries int, root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last.Generation = gen
	s.last.Status = status
	s.last.Detail = detail
	s.last.Entries = entries
	s.last.Root = root
	s.last.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	// The scan is over: transient progress reads 0 again, so a poller that
	// lingers past the terminal never shows a stale "X of N".
	s.last.Extracted = 0
	s.last.Total = 0
	s.activeRoot = ""
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
			// Stop descending into over-depth directories: a plain nil
			// would keep walking their (unindexed) contents forever.
			if d.IsDir() {
				return filepath.SkipDir
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
		}
		if info, err := d.Info(); err == nil {
			row.Size = info.Size()
			// Nanosecond precision: a same-size rewrite within the same
			// second must still be detected as a change. A directory's
			// modtime moves on entry add/remove — the signal classify
			// relies on — so dir rows carry real signals, not zeros.
			row.ModTime = info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")
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
			oldRoot, likeEscape(oldRoot+sep)+"%", indexWriteBatch)
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
//
// Directory snapshots are a function of the files beneath them, so a dir
// row is re-extracted whenever any stored file under it changed, vanished,
// or is new — a dir's own mtime only moves on entry add/remove, never when
// a child file is rewritten in place.
//
// Non-audio files carry a pre-built complete (empty) snapshot: there is
// nothing for ffprobe to probe, and storing the empty complete snapshot is
// what makes the row classify stable on every later refresh.
//
// It streams source_index once in path (primary-key) order and merges it
// with the walked rows sorted the same way: one ordered index scan for the
// whole tree, never a per-batch IN-list lookup (which would scan the table
// per batch).
func (s *IndexService) classify(rows []indexRow, root string) (unchanged, extract []indexRow) {
	dbRows, err := s.db.Query(`SELECT path, size, modtime, payload FROM source_index ORDER BY path`)
	if err != nil {
		log.Printf("index: classify query: %v", err)
		return nil, rows
	}
	defer dbRows.Close()
	type stored struct {
		Size    int64
		ModTime string
		Payload string
	}
	var nextStored *stored
	var nextPath string
	// load advances the stored cursor; on exhaustion or a scan error it
	// CLEARS nextStored — a stale cursor would pin the merge loop on a
	// walked path that sorts after the last stored row (vanished-row
	// infinite loop).
	load := func() {
		if !dbRows.Next() {
			nextStored = nil
			return
		}
		var st stored
		if err := dbRows.Scan(&nextPath, &st.Size, &st.ModTime, &st.Payload); err != nil {
			log.Printf("index: classify scan: %v", err)
			nextStored = nil
			return
		}
		nextStored = &st
	}
	walked := make([]indexRow, len(rows))
	copy(walked, rows)
	sort.Slice(walked, func(i, j int) bool { return walked[i].Path < walked[j].Path })

	isComplete := func(payload string) bool {
		var snap audio.SourceMetadata
		return json.Unmarshal([]byte(payload), &snap) == nil && sourceMetadataComplete(&snap)
	}
	// A non-audio file has nothing to probe: a complete (empty) snapshot
	// is the honest stored form and keeps the row stable on refresh.
	emptySnap := mustJSON(audio.SourceMetadata{Schema: audio.SourceMetadataSchema, SourceKind: "file"})
	// dirDirty marks every walked directory that contains (at any depth) a
	// new, changed, or vanished file — the only directories whose snapshot
	// this pass can no longer claim. The root has no indexed row, so it is
	// the walk's boundary.
	dirDirty := map[string]bool{}
	markDirty := func(path string) {
		prefix := indexChildPrefix(path)
		for d := filepath.Dir(prefix); d != prefix && d != root; d = filepath.Dir(d) {
			dirDirty[d] = true
		}
	}
	signalsMatch := func(r indexRow, st *stored) bool {
		return st.Size == r.Size && st.ModTime == r.ModTime
	}
	for i := 0; i < len(walked) || nextStored != nil; {
		switch {
		case i >= len(walked):
			nextStored = nil // remaining stored rows are not walked: ignore
		case nextStored == nil:
			load()
			if nextStored != nil {
				continue // more stored rows to merge
			}
			// New path, no stored row.
			r := walked[i]
			if r.Type == "file" && !audio.IsAudioFile(r.Name) {
				r.Payload = emptySnap
			}
			extract = append(extract, r)
			markDirty(r.Path)
			i++
		case walked[i].Path < nextPath:
			// New path below the next stored row: same as above.
			r := walked[i]
			if r.Type == "file" && !audio.IsAudioFile(r.Name) {
				r.Payload = emptySnap
			}
			extract = append(extract, r)
			markDirty(r.Path)
			i++
		case walked[i].Path > nextPath:
			// Stored row not walked this pass: vanished (prune removes it).
			markDirty(nextPath)
			load()
		default:
			st := nextStored
			r := walked[i]
			if r.Type == "dir" {
				// A dir's own mtime only moves on entry add/remove, not on
				// a child rewrite. Its signals+completeness are checked
				// here; the child-change check needs the events of paths
				// BELOW it, which sort after it, so the second pass below
				// moves any dirty dir out of unchanged.
				if signalsMatch(r, st) && isComplete(st.Payload) {
					unchanged = append(unchanged, r)
					i++
					load()
					continue
				}
				extract = append(extract, r)
			} else if signalsMatch(r, st) && isComplete(st.Payload) {
				unchanged = append(unchanged, r)
				i++
				load()
				continue
			} else {
				markDirty(r.Path) // changed file dirties its ancestor dirs
				extract = append(extract, r)
			}
			i++
			load()
		}
	}
	// Second pass: a directory sorts BEFORE its children, so while it was
	// merged, dirDirty did not yet carry the events of the paths below it.
	// Every unchanged dir that gained/lost/changed a child must be
	// re-extracted so its snapshot reflects the new subtree.
	if len(dirDirty) > 0 {
		kept := unchanged[:0]
		for _, r := range unchanged {
			if r.Type == "dir" && dirDirty[r.Path] {
				extract = append(extract, r)
				continue
			}
			kept = append(kept, r)
		}
		unchanged = kept
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
// index table, returning entries with their pre-extracted evidence. It
// reports ok=false (filesystem fallback) when the stored rows belong to a
// different root than the current one — e.g. the input root was just
// re-pointed and the rescan has not landed — so a stale index never
// masquerades as an empty current directory.
func (h *Handler) listIndexedChildren(dir string) ([]IndexedDirectoryEntry, bool, error) {
	root := h.svc.IndexSvc.CurrentRoot()
	if root == "" || dir != root && !strings.HasPrefix(dir, indexChildPrefix(root)) {
		return nil, false, nil // root missing, or not under the root
	}
	if state := h.svc.IndexSvc.Status().State; state.Root != root {
		return nil, false, nil // rows are from a previous root (or no scan yet)
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
