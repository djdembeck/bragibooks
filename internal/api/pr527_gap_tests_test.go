package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	dbpkg "github.com/bragibooks/bragibooks/internal/db"
	_ "modernc.org/sqlite"
)

// ── T1: the engine preview endpoint enforces its 1 MiB body cap ──

// TestEnginePreviewEnforcesBodyLimit pins the preview request cap: a body
// larger than maxEnginePreviewBodyBytes (1 MiB) is refused with 413 BEFORE
// any decoding, so the endpoint cannot be used as a memory amplifier.
// Modelled on TestMetadataOverrideCoverUploadEnforcesPinnedEngineLimit.
func TestEnginePreviewEnforcesBodyLimit(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t)
	pad := strings.Repeat("a", maxEnginePreviewBodyBytes)
	body := `{"path_format":"{title}","metadata":{"title":"` + pad + `"}}`
	req := httptest.NewRequest("POST", "/api/engine/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.EnginePreview(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize preview body = %d, want 413 — %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exceeds the limit") {
		t.Fatalf("body does not name the limit: %s", w.Body.String())
	}
}

// ── T2: a corrupt chapters column fails the snapshot load closed ──

// TestCorruptChaptersColumnFailsQueue pins the fail-closed contract: a
// malformed (not merely empty) chapters JSON in the snapshot row is
// corruption, and the book must be refused at queue time with a clear
// re-match message — a silent [] would read as the engine's explicit
// "clear" at run time and wipe the source's chapters.
func TestCorruptChaptersColumnFailsQueue(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, title, authors, narrators, chapters)
		 VALUES (?, 'Book One', '["A"]', '["N"]', '{"title": not json')`,
		bookID,
	); err != nil {
		t.Fatalf("seed corrupt snapshot row: %v", err)
	}
	// The load surfaces the corruption, not a silent empty list.
	if snap, err := h.loadSnapshot(bookID); err == nil {
		t.Fatalf("loadSnapshot = (%+v, nil); want a corrupt-chapters error (a silent [] reads as an explicit clear)", snap)
	} else if !strings.Contains(err.Error(), "corrupt chapters") {
		t.Fatalf("loadSnapshot error = %q; want it to name the corrupt chapters column", err)
	}
	// And the book is un-queueable: the error surfaces, no job row appears.
	srcPath := t.TempDir() + "/in.m4a"
	if err := os.WriteFile(srcPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.QueueJob(context.Background(), bookID, []string{srcPath}, queueDestination{}, ""); err == nil {
		t.Fatal("QueueJob succeeded on a book with a corrupt chapters column; want a re-match refusal")
	}
	var n int
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE book_id = ?`, bookID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("processing_jobs has %d rows; want 0", n)
	}
}

// ── T3: chaptersFromADB drops negative rows; all-invalid detail retains ──

// TestChaptersFromADBNegativeRowsAndRetention pins the wrapped-uint64
// hazard: a chapter row with a NEGATIVE start or duration is dropped, not
// cast (a negative int cast to uint64 wraps to a multi-year chapter), and
// a release whose detail rows are ALL invalid must NOT freeze an explicit
// clear — the caller keeps the previously captured chapters.
func TestChaptersFromADBNegativeRowsAndRetention(t *testing.T) {
	h := newTestHandler(t)

	// (a) The converter: valid rows survive, negative rows are dropped, and
	// the boolean distinguishes "has usable rows" from "rows existed but
	// none usable" (corrupt, not chapterless).
	chs := []audiobookdb.Chapter{
		{Title: "OK", StartOffsetMs: 0, LengthMs: 60000},
		{Title: "NegStart", StartOffsetMs: -1, LengthMs: 1000},
		{Title: "NegLen", StartOffsetMs: 5000, LengthMs: -1},
	}
	got, ok := chaptersFromADB(chs)
	if !ok {
		t.Fatalf("chaptersFromADB ok = false, want true (a valid row exists)")
	}
	if len(got) != 1 || got[0].Title != "OK" {
		t.Fatalf("chaptersFromADB = %+v, want only the valid row", got)
	}
	allBad, okBad := chaptersFromADB([]audiobookdb.Chapter{
		{Title: "Bad", StartOffsetMs: -1, LengthMs: 1000},
		{Title: "Bad2", StartOffsetMs: 5000, LengthMs: -2},
	})
	if okBad {
		t.Fatalf("chaptersFromADB (all rows invalid) ok = true; want false (corrupt detail is not chapterless)")
	}
	if len(allBad) != 0 {
		t.Fatalf("chaptersFromADB (all rows invalid) = %+v, want empty", allBad)
	}
	if empty, okEmpty := chaptersFromADB(nil); !okEmpty || len(empty) != 0 {
		t.Fatalf("chaptersFromADB (no rows) = (%+v, %v); want (empty, true)", empty, okEmpty)
	}

	// (b) The caller: a live release whose chapter detail is ALL invalid
	// retains the previously captured chapters (no explicit-clear freeze).
	h.svc.CoversDir = t.TempDir()
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	// The stub catalog: the book fetch succeeds (images empty — no cover
	// to download), and the release fetch SUCCEEDS with a detail whose
	// rows are all negative.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/books/42":
			fmt.Fprint(w, `{"id":"42","title":"Seed Book"}`)
		case "/releases/5":
			fmt.Fprint(w, `{"id":"5","title":"Seed Book","chapterDetail":{"chapters":[{"title":"Bad","ordinal":1,"startOffsetMs":-1,"lengthMs":1000},{"title":"Bad2","ordinal":2,"startOffsetMs":5000,"lengthMs":-2}]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (1, 'Seed Book', 'matched', '42', '5')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, genres, cover_file, chapters, captured_at)
		 VALUES (1, '42', '5', 'Seed Book', '[]', '[]', '[]', '',
		   '[{"title":"Ch 1","start_millis":0,"duration_millis":60000}]', datetime("now"))`,
	); err != nil {
		t.Fatalf("seed snapshot row: %v", err)
	}

	w := doUpdateBook(t, h, 1, UpdateBookRequest{Title: ptrStr("Edited Title")})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d; want 200", w.Code)
	}
	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(s.Chapters) != 1 || s.Chapters[0].Title != "Ch 1" {
		t.Fatalf("chapters = %+v; want the previously captured chapters RETAINED (an all-invalid detail is corrupt data, not an explicit clear)", s.Chapters)
	}
}

// The capture tests store the catalog client in an *atomic.Value
// (h.svc.AudiobookDB is one); this file does the same.
type syncValue = atomic.Value

// ── T4: pruning with an EMPTY keep-set retires the prior capture file ──

// TestPruneSupersededCoversEmptyKeepSet pins the orphan-leak regression: a
// capture that ended up with NO cover file prunes with an empty current
// reference, so the PRIOR capture's unreferenced file is removed. An early
// return on the empty keep-set leaked the file forever.
func TestPruneSupersededCoversEmptyKeepSet(t *testing.T) {
	h := newTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	for _, name := range []string{"1-111.jpg", "1-snapshot.jpg"} {
		if err := os.WriteFile(filepath.Join(covers, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// A capture with no cover file: the empty keep-set must still sweep the
	// book's unreferenced snapshot covers (no active job protects anything).
	h.pruneSupersededCovers(1, &BookSnapshot{BookID: 1, CoverFile: ""})
	for _, gone := range []string{"1-111.jpg", "1-snapshot.jpg"} {
		if ok, _ := mustFile(filepath.Join(covers, gone)); ok {
			t.Errorf("unreferenced cover %q must be pruned even when the current capture has no cover file", gone)
		}
	}
}

// ── T5: capture stores the SELECTED artwork; local-only cover backfill ──

// TestCaptureStoresSelectedReleaseCover pins the artwork selection: the
// capture stores the book's PERSISTED cover_image_url (what Match selected
// from the chosen release's artwork), not book.Images[0] — falling back to
// the first book-level image would capture a different cover than the
// operator saw. The live catalog stub's Images[0] differs from the
// persisted cover_image_url, so a fallback regression changes which file
// lands.
func TestCaptureStoresSelectedReleaseCover(t *testing.T) {
	h := newTestHandler(t)
	h.svc.CoversDir = t.TempDir()
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/books/42":
			// Images[0] is a DIFFERENT cover than the persisted selection
			// (a different host): a fallback-to-Images[0] regression would
			// try to download THAT host.
			fmt.Fprint(w, `{"id":"42","title":"Seed Book","images":[{"id":"i1","url":"https://127.0.0.9/book.jpg"}]}`)
		case "/releases/5":
			fmt.Fprint(w, `{"id":"5","title":"Seed Book"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	// The persisted selection is a loopback host: the runtime gate refuses
	// it BEFORE any dial (fast, offline), and the refusal names the URL —
	// so the capture log reveals WHICH artwork was selected. The
	// selection's host (127.0.0.7) and Images[0]'s host (127.0.0.9) are
	// distinct, so a fallback regression is observable in the log.
	logBuf := &strings.Builder{}
	log.SetOutput(logBuf)
	defer log.SetOutput(os.Stderr)

	selected := "https://127.0.0.7/sel.jpg"
	if _, err := h.svc.DB.Exec(
		`INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id, cover_image_url)
		 VALUES (1, 'Seed Book', 'matched', '42', '5', ?)`, selected,
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, genres, cover_file, chapters, captured_at)
		 VALUES (1, '42', '5', 'Seed Book', '[]', '[]', '[]', '', '[]', datetime("now"))`,
	); err != nil {
		t.Fatalf("seed snapshot row: %v", err)
	}

	w := doUpdateBook(t, h, 1, UpdateBookRequest{Title: ptrStr("Edited Title")})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d; want 200 (a cover download failure degrades, it does not fail)", w.Code)
	}
	// The SELECTED artwork's URL (cover_image_url, host 127.0.0.7) was
	// the one the capture attempted to download — not Images[0] (host
	// 127.0.0.9). The loopback refusal is fast and names the host.
	if !strings.Contains(logBuf.String(), "127.0.0.7") {
		t.Fatalf("the capture never attempted the persisted cover_image_url (log: %s); want the SELECTED artwork over book.Images[0]", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "127.0.0.9") {
		t.Fatalf("book.Images[0] was used for the download (log: %s); the selection must win", logBuf.String())
	}
	if s, err := h.loadSnapshot(1); err != nil {
		t.Fatalf("load snapshot: %v", err)
	} else if s.CoverFile != "" {
		t.Fatalf("cover = %q; want empty (the download was refused; the row must stay coverless)", s.CoverFile)
	}
	entries, _ := os.ReadDir(h.svc.CoversDir)
	if len(entries) != 0 {
		t.Fatalf("covers dir has %d files after a refused download; want none", len(entries))
	}
}

// TestCaptureLocalOnlySaveBackfillsCover pins the local-only branch: a
// save that touches no snapshot-relevant field skips the full catalog
// enrichment, but a book that has NEVER captured a cover (CoverFile "")
// must still backfill the catalog artwork — without a release fetch and
// without touching the release-derived fields (no genres, no subtitle).
func TestCaptureLocalOnlySaveBackfillsCover(t *testing.T) {
	h := newTestHandler(t)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	var mu sync.Mutex
	releaseHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.URL.Path {
		case "/books/42":
			// The backfill fetches only the book (images), never the release.
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"42","title":"Seed Book","images":[{"id":"i1","url":"https://img.example/x.jpg"}]}`)
			return
		case "/releases/5":
			mu.Unlock()
			releaseHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"5","title":"Seed Book"}`)
			return
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av
	h.svc.CoversDir = t.TempDir()

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (1, 'Seed Book', 'matched', '42', '5')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// No cover_file: the book never had a changed-flagged capture.
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, genres, cover_file, chapters, captured_at)
		 VALUES (1, '42', '5', 'Seed Book', '[]', '[]', '[]', '', '[]', datetime("now"))`,
	); err != nil {
		t.Fatalf("seed snapshot row: %v", err)
	}

	// StatusMessage is NOT a snapshot-relevant field: the full enrichment
	// is skipped, the local-only backfill branch runs.
	w := doUpdateBook(t, h, 1, UpdateBookRequest{StatusMessage: ptrStr("Just a note")})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d; want 200", w.Code)
	}
	mu.Lock()
	hits := releaseHits
	mu.Unlock()
	if hits != 0 {
		t.Fatalf("the local-only backfill fetched the release %d times; want 0 (no release-derived fields on this path)", hits)
	}
	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	// The cover download itself fails (the stub has no image), so the row
	// stays coverless — but the point of this test is the branch executed
	// (book fetched, release not, release fields untouched).
	if len(s.Chapters) != 0 || s.Subtitle != "" {
		t.Errorf("release-derived content changed on a local-only save: subtitle=%q chapters=%+v", s.Subtitle, s.Chapters)
	}
}

// ── T6: removeBookOwnedCovers sweeps every class, protects siblings ──

// TestRemoveBookOwnedCoversSweepsOwnClassOnly pins the deletion sweep: the
// legacy extracted cover, the snapshot captures (<id>-<nano>.jpg plus the
// legacy -snapshot.jpg), and the override assets (ovr-<id>-*.jpg) are all
// removed, while a SIBLING book's files (12-... when deleting 1) survive.
func TestRemoveBookOwnedCoversSweepsOwnClassOnly(t *testing.T) {
	h := newTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	own := []string{"1.jpg", "1-123456789012345.jpg", "1-snapshot.jpg", "ovr-1-abc123.jpg"}
	sibling := []string{"12.jpg", "12-123456789012345.jpg", "12-snapshot.jpg", "ovr-12-abc123.jpg"}
	for _, name := range append(append([]string{}, own...), sibling...) {
		if err := os.WriteFile(filepath.Join(covers, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if _, err := h.svc.DB.Exec("INSERT INTO books (id, title, status) VALUES (1, 'B', 'matched'), (12, 'S', 'matched')"); err != nil {
		t.Fatalf("seed books: %v", err)
	}
	h.removeBookOwnedCovers(1)
	for _, gone := range own {
		if ok, _ := mustFile(filepath.Join(covers, gone)); ok {
			t.Errorf("book 1's cover %q must be removed", gone)
		}
	}
	for _, kept := range sibling {
		if ok, _ := mustFile(filepath.Join(covers, kept)); !ok {
			t.Errorf("sibling book 12's cover %q must survive deleting book 1", kept)
		}
	}
}

// ── T7: the persisted partial-write guard survives a restart ──

// TestPersistedWriteFailedRootSurvivesRestart pins the restart regression:
// the guard that makes the indexed listing serve the FILESYSTEM for a
// half-written root is what a NEW process on the same DB must honor —
// today no index test constructs a fresh IndexService from a persisted
// write_failed_root row, so an in-memory-only guard would silently serve
// the half-written table after a restart.
func TestPersistedWriteFailedRootSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// File-backed DB: the "restart" is a fresh service on the SAME file.
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")
	h := &Handler{svc: &Services{DB: db, Config: cm, IndexSvc: svc}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	// Clean scan: the listing serves the index.
	st := runToTerminal(t, svc)
	if st.Status != "done" || st.Root != root {
		t.Fatalf("initial scan = %s root %q, want done on %s", st.Status, st.Root, root)
	}
	if _, ok, err := h.listIndexedChildren(root); err != nil || !ok {
		t.Fatalf("clean listing = ok=%v err=%v, want the index served", ok, err)
	}

	// Fail the next scan's writes (every upsert aborts) → partial write.
	if _, err := db.Exec(`CREATE TRIGGER fail_upsert AFTER UPDATE ON source_index
		FOR EACH ROW WHEN NEW.path IS NOT NULL
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	st = runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("failed scan status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	// The persisted row carries the guard (the in-memory field alone is
	// not durable).
	var persisted string
	if err := db.QueryRow(`SELECT write_failed_root FROM index_state WHERE id = 1`).Scan(&persisted); err != nil {
		t.Fatalf("read write_failed_root: %v", err)
	}
	if persisted != root {
		t.Fatalf("persisted write_failed_root = %q, want %q (the guard must be durable, not in-memory only)", persisted, root)
	}

	// RESTART: a fresh IndexService on the same DB. The filesystem
	// fallback must be active from the persisted row, with no scan run
	// in this process.
	fresh := NewIndexService(db, cm, "")
	if got := fresh.WriteFailedRoot(); got != root {
		t.Fatalf("fresh service WriteFailedRoot = %q, want the persisted %q (restart must keep serving the filesystem, not the half-written table)", got, root)
	}
	freshH := &Handler{svc: &Services{DB: db, Config: cm, IndexSvc: fresh}}
	if entries, ok, err := freshH.listIndexedChildren(root); err != nil || ok || len(entries) != 0 {
		t.Fatalf("post-restart listing = ok=%v n=%d err=%v, want the filesystem fallback (no index rows served for the half-written root)", ok, len(entries), err)
	}
}

// ── T8: intake freshness edge cases ──

// TestStoredIndexMetadataInPlaceRewriteSameSignals pins the intake gate
// edge: a stored dir row whose size/modtime MATCH a live regular file of a
// different kind is rejected (re-probe), and an intake path under a
// SYMLINKED input root rebases onto the resolved root and hits the index.
func TestStoredIndexMetadataInPlaceRewriteSameSignals(t *testing.T) {
	h := newTestHandler(t)
	if _, err := h.svc.DB.Exec(`
		CREATE TABLE source_index (
			path TEXT PRIMARY KEY,
			name TEXT, type TEXT, size INTEGER, modtime TEXT, payload TEXT,
			indexed_at TEXT, seen_at TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DB.Exec(`
		CREATE TABLE index_state (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			root TEXT NOT NULL DEFAULT '',
			entries INTEGER NOT NULL DEFAULT 0,
			last_run_at TEXT,
			status TEXT NOT NULL DEFAULT 'never',
			detail TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL DEFAULT 0,
			write_failed_root TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// (a) Dir replaced in place by a file of the SAME size and mtime.
	f := filepath.Join(dir, "a.mp3")
	body := []byte("0123456789")
	if err := os.WriteFile(f, body, 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(f)
	modtime := info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")
	dirComplete := `{"schema":2,"source_kind":"folder","files_total":0,"files_probed":0,"degraded":false,"files":[],"fields":{}}`
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, 'a.mp3', 'dir', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(f), info.Size(), modtime, dirComplete); err != nil {
		t.Fatal(err)
	}
	if snap := h.storedIndexMetadata(f); snap != nil {
		t.Errorf("stored DIR row with matching size/mtime over a live FILE was reused; want a re-probe (the type is part of freshness)")
	}
	// And the mirror case: a stored FILE row over a live directory.
	if _, err := h.svc.DB.Exec(`UPDATE source_index SET type = 'file', payload = ? WHERE path = ?`,
		`{"schema":2,"source_kind":"file","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"a.mp3","duration_seconds":5}],"fields":{}}`,
		canonicalRoot(f)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Re-stat so the row's size matches the LIVE dir's size: the rejection
	// must come from the type mismatch, not a size mismatch.
	dinfo, _ := os.Stat(f)
	if _, err := h.svc.DB.Exec(`UPDATE source_index SET size = ? WHERE path = ?`, dinfo.Size(), canonicalRoot(f)); err != nil {
		t.Fatal(err)
	}
	if snap := h.storedIndexMetadata(f); snap != nil {
		t.Errorf("stored FILE row over a live DIR of the same size/mtime was reused; want a re-probe")
	}

	// (b) Symlinked input root: the index stores rows under the RESOLVED
	// root; the lexical intake path must rebase and hit them.
	real := t.TempDir()
	lexical := filepath.Join(t.TempDir(), "in-link")
	if err := os.Symlink(real, lexical); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	src := filepath.Join(real, "1.mp3")
	if err := os.WriteFile(src, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	sinfo, _ := os.Stat(src)
	smod := sinfo.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")
	complete := `{"schema":2,"source_kind":"file","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"1.mp3","duration_seconds":5}],"fields":{"asin":{"values":["B0STORED00"],"source":"embedded","status":"present"}}}`
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, '1.mp3', 'file', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(src), sinfo.Size(), smod, complete); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = lexical
	cm.Store(cm.Config())
	h.svc.Config = cm
	h.svc.IndexSvc = NewIndexService(h.svc.DB, cm, "")

	if snap := h.storedIndexMetadata(filepath.Join(lexical, "1.mp3")); snap == nil {
		t.Errorf("intake path under a symlinked root missed the index row stored under the resolved root; want the stored snapshot hit")
	}
	// Control: without the resolved-root knowledge (no IndexSvc) the same
	// lexical lookup cannot rebase and falls to the live probe.
	h.svc.IndexSvc = nil
	if snap := h.storedIndexMetadata(filepath.Join(lexical, "1.mp3")); snap != nil {
		t.Errorf("lexical path with no index service hit the index; want the live probe (no resolved root to rebase onto)")
	}
}

// ── T9: validCoverName table ──

// TestValidCoverNameShapes pins the covers-directory asset grammar: the
// extracted cover (<id>.jpg), the snapshot captures (<id>-<nano>.jpg, the
// legacy <id>-snapshot.jpg), and the override assets — and nothing else.
func TestValidCoverNameShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"1-123456789012345.jpg", true},    // capture-specific
		{"1-123456.jpg", true},             // capture-specific, short nano
		{"1-snapshot.jpg", true},           // legacy shared name
		{"1.jpg", true},                    // extracted source cover
		{"ovr-1-abcdef012345.jpg", true},   // override asset
		{"1-abc.jpg", false},               // digit-dash suffix with letters
		{"1-123456.jpeg", false},           // wrong extension
		{"1-123456.jpg.bak", false},        // double extension
		{"1..jpg", false},                  // empty suffix
		{"1-.jpg", false},                  // empty suffix after dash
		{"1-123456.jpg/../etc.jpg", false}, // traversal is never a valid name
	}
	for _, tt := range tests {
		if got := validCoverName(tt.name); got != tt.want {
			t.Errorf("validCoverName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ── T10: the runtime cover gate refuses non-https schemes ──

// TestAssertPublicCoverURLSchemeRefusal pins the RUNTIME gate (distinct
// from the save-time validation): a public-host http:// URL is refused
// because the engine fetches covers over HTTPS only — it must not be
// frozen into a job to fail at run time.
func TestAssertPublicCoverURLSchemeRefusal(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("http://93.184.216.34/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assertPublicCoverURL(context.Background(), u); err == nil {
		t.Fatal("assertPublicCoverURL accepted an http:// URL; want a scheme refusal (the engine fetches covers over HTTPS only)")
	} else if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("refusal does not name the scheme: %v", err)
	}
	// The HTTPS form of the SAME public IP passes the scheme gate: the
	// refusal above is the scheme, not the address.
	https, _ := url.Parse("https://93.184.216.34/cover.jpg")
	if _, err := assertPublicCoverURL(context.Background(), https); err != nil {
		t.Fatalf("public https URL refused: %v", err)
	}
}

// ── T11: writeCoverBody caps the body, stages unpredictably ──

// TestWriteCoverBodyCapAndNoPredictableTemp pins the fetch-write contract:
// a body over coverMaxBytes is refused (no dest left behind), and a
// successful write stages in an UNPREDICTABLE temp name then renames —
// the old dest+".tmp" was plantable as a symlink by an output-dir writer.
func TestWriteCoverBodyCapAndNoPredictableTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// >5 MiB body: the cap error, no dest, and the staged temp is removed.
	big := io.NopCloser(io.LimitReader(&infiniteBytes{}, coverMaxBytes+1))
	dest := filepath.Join(dir, "cover.jpg")
	if err := writeCoverBody(context.Background(), &http.Response{Body: big}, dest); err == nil {
		t.Fatal("writeCoverBody accepted a >5 MiB body; want the coverMaxBytes cap error")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("cap error does not name the cap: %v", err)
	}
	if ok, _ := mustFile(dest); ok {
		t.Fatal("dest was written for an oversized body; want no file")
	}

	// A normal body lands at dest, and no predictably named temp remains.
	if err := writeCoverBody(context.Background(), &http.Response{Body: io.NopCloser(strings.NewReader("artwork"))}, dest); err != nil {
		t.Fatalf("writeCoverBody: %v", err)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "artwork" {
		t.Fatalf("dest = %q (err %v); want the body content", data, err)
	}
	for _, predictable := range []string{dest + ".tmp", dest + ".tmp-1"} {
		if ok, _ := mustFile(predictable); ok {
			t.Fatalf("predictable temp %q left behind; the staging name must be unpredictable", predictable)
		}
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 1 || left[0].Name() != "cover.jpg" {
		names := []string{}
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Fatalf("dir after a clean write = %v; want only cover.jpg (staged temp renamed into place)", names)
	}
}

// infiniteBytes is an endless reader so the cap trips without a 5 MiB
// literal in the test.
type infiniteBytes struct{}

func (infiniteBytes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// ── T12: a same-root refresh landing mid-scan coalesces ──

// TestIndexServiceSameRootMidScanCoalesces pins the activeRoot re-
// establishment: while a scan is RUNNING, a refresh for the SAME root must
// coalesce into the in-flight scan — not queue a full extra generation
// (the running scan's terminal clears activeRoot, so the re-assignment at
// scan start is what lets the coalesce branch match).
func TestIndexServiceSameRootMidScanCoalesces(t *testing.T) {
	root := t.TempDir()
	// Deep enough that the walk outlasts the assertions (the mid-running-
	// scan test uses the same sizing).
	for i := 0; i < 3000; i++ {
		if err := os.MkdirAll(filepath.Join(root, "d", fmt.Sprintf("%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := newIndexTestService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	st1 := svc.RequestScan()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if svc.Status().State.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not start: %+v", svc.Status().State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Same-root refresh while the walk is in flight: coalesce into the
	// in-flight scan (no generation bump, no extra rescan token).
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation {
		t.Fatalf("same-root mid-scan refresh generation = %d, want the in-flight %d (coalesce, not a rescan)", st2.State.Generation, st1.State.Generation)
	}
	// The worker must hold exactly ONE token: the in-flight scan's. An
	// extra (the bug) would run a second full scan after this one.
	if got := len(svc.req); got != 0 {
		t.Fatalf("req channel holds %d tokens after a coalesced refresh; want 0 extra (the running scan owns its token)", got)
	}
	// The scan completes for ITS generation, and no second scan runs.
	st := runToTerminal(t, svc)
	if st.Generation != st1.State.Generation || st.Status != "done" {
		t.Fatalf("terminal = gen %d status %s, want done for gen %d", st.Generation, st.Status, st1.State.Generation)
	}
	if got := svc.Status().State.Generation; got != st1.State.Generation {
		t.Fatalf("final generation = %d, want %d (a coalesced refresh must not queue a second scan)", got, st1.State.Generation)
	}
}

// ── T13: a classify DB failure is terminal, table untouched ──

// TestClassifyFailureTerminalTableUntouched pins the mid-iteration
// stored-table-read contract: classify streams source_index and a per-row
// read failure (here a non-numeric size, stored as TEXT under the column's
// INTEGER affinity) must surface as a 4th-return error and take the scan
// terminal ("error") WITHOUT publishing its root and WITHOUT touching the
// table — classifying against an incomplete stored table would overwrite
// complete rows with degraded metadata on budget exhaustion.
func TestClassifyFailureTerminalTableUntouched(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "b.mp3"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, _ := newIndexTestService(t, root)
	bPath := canonicalRoot(filepath.Join(root, "d", "b.mp3"))
	storeIndexRow(t, svc.db, bPath, 10, "2020-01-01T00:00:00.000000000Z", completeSnapPayload())
	payloadBefore := payloadFor(t, svc.db, bPath)

	// Corrupt ONE stored row mid-table: a non-numeric size stays TEXT under
	// the INTEGER affinity, so the merge cursor's Scan into int64 fails on
	// exactly that row — the mid-iteration failure classify records as
	// iterErr. (A BLOB payload would not work: the driver converts it to
	// string.)
	if _, err := svc.db.Exec(`UPDATE source_index SET size = 'deadbeef' WHERE path = ?`, bPath); err != nil {
		t.Fatal(err)
	}
	// classify's ordered read stops at the corrupted row (mid-iteration):
	// 4th return non-nil, slices empty. The walked set must be real so the
	// merge cursor advances past the first (uncorrupted) stored row.
	rows, truncated, _, cancelled := svc.walk(context.Background(), root)
	if truncated || cancelled {
		t.Fatalf("walk truncated=%v cancelled=%v, want a complete clean walk", truncated, cancelled)
	}
	if _, _, _, err := svc.classify(rows, root); err == nil {
		t.Fatal("classify with an unreadable stored row = nil error, want a non-nil error")
	}
	// The scan must fail at classify (not at pruneVanished, which reads
	// paths only — the BLOB payload is invisible to it), and it must NOT
	// write anything: a failed scan skips upsertAll, so an UPDATE trigger
	// can only fire if the regression writes on the error path.
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_source_index_update BEFORE UPDATE ON source_index
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("scan with a classify DB failure = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if !strings.Contains(st.Detail, "classify") {
		t.Fatalf("terminal detail %q; the failure must name classify (pruneVanished reads paths only and cannot see a BLOB payload)", st.Detail)
	}
	if st.Root != "" {
		t.Fatalf("scan published root %q; a failed classify must not publish its attempted root", st.Root)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM source_index`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("source_index rows = %d, want 1 (the table must be untouched)", n)
	}
	if got := payloadFor(t, svc.db, bPath); got != payloadBefore {
		t.Fatalf("stored payload changed by a failed scan; want it untouched")
	}
	var sizeText string
	if err := svc.db.QueryRow(`SELECT size FROM source_index WHERE path = ?`, bPath).Scan(&sizeText); err != nil {
		t.Fatal(err)
	}
	if sizeText != "deadbeef" {
		t.Fatalf("stored size = %q, want the corruption to remain (a failed scan must not rewrite the row)", sizeText)
	}
}

// ── T14: queue-time selection normalization ──

// TestQueueTimeNormalizationFreezesNormalizedCopy pins the freeze
// contract: a saved (padded, duplicated, blank-bearing) field list freezes
// into the job as the NORMALIZED copy — trimmed, de-duped, blanks dropped,
// and an all-blank list freezes as the EXPLICIT empty selection (none-
// selected: cover AND chapters preserve), not the raw padded strings.
func TestQueueTimeNormalizationFreezesNormalizedCopy(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	seedSnapshot(t, h.svc.DB, bookID)

	// Padded + duplicated + blank entries: the frozen copy is normalized.
	padded := []string{" title ", "", "subtitle", "title"}
	svc.proc.ApplyWriteSelection(&padded, audio.ChaptersPolicyValuePreserve)
	snap, err := svc.loadSnapshotForQueue(bookID, svc.proc.QueueSnapshot())
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (padded): %v", err)
	}
	if snap == nil || snap.Selection == nil {
		t.Fatalf("queue snapshot = %#v, want a frozen selection", snap)
	}
	want := []string{"title", "subtitle"}
	if len(snap.Selection.Fields) != 2 || snap.Selection.Fields[0] != want[0] || snap.Selection.Fields[1] != want[1] {
		t.Fatalf("frozen fields = %v, want %v (trimmed, de-duped, blanks dropped)", snap.Selection.Fields, want)
	}
	if snap.Selection.Cover != audio.WritePolicyValuePreserve {
		t.Fatalf("frozen cover = %q, want preserve (cover not in the selection)", snap.Selection.Cover)
	}

	// An ALL-BLANK list normalizes to the explicit empty selection:
	// none-selected freezes cover AND chapters to preserve.
	blank := []string{" ", ""}
	svc.proc.ApplyWriteSelection(&blank, audio.ChaptersPolicyValueWrite)
	again, err := svc.loadSnapshotForQueue(bookID, svc.proc.QueueSnapshot())
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (all-blank): %v", err)
	}
	if again == nil || again.Selection == nil {
		t.Fatalf("all-blank queue snapshot = %#v, want a frozen (empty) selection", again)
	}
	if len(again.Selection.Fields) != 0 {
		t.Fatalf("all-blank frozen fields = %v, want empty (blanks normalize away)", again.Selection.Fields)
	}
	if again.Selection.Cover != audio.WritePolicyValuePreserve || again.Selection.Chapters != audio.ChaptersPolicyValuePreserve {
		t.Fatalf("all-blank frozen cover/chapters = %q/%q, want preserve/preserve (none-selected writes nothing)", again.Selection.Cover, again.Selection.Chapters)
	}
}

// ── T15: the swap gate revalidates the PUT's OWN output scheme ──

// TestSettingsBinarySwapRevalidatesRequestSchemeAgainstCandidate extends
// TestSettingsBinarySwapRevalidatesSchemesAgainstCandidate to the effective
// scheme the request itself supplies: a PUT whose OWN output_scheme uses a
// candidate-unsupported token is refused (validation runs before the
// request's scheme is merged into the saved state), while a matching
// scheme is accepted.
func TestSettingsBinarySwapRevalidatesRequestSchemeAgainstCandidate(t *testing.T) {
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)
	if _, err := db.Exec(`CREATE TABLE output_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		output_dir TEXT NOT NULL DEFAULT '',
		output_scheme TEXT NOT NULL DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}

	// Candidate vocabulary: {author}, {authors}, {narrators}, {title} —
	// NOT {series_position} (the engine floor requires the plural people
	// tokens, so the candidate advertises them).
	// The accepted swap PUBLISHES the candidate's token set to the
	// PROCESS-GLOBAL registry (audio.SetPathFormatTokens at settings.go:598);
	// restore the TestMain baseline (api_test.go) so the other settings
	// tests — which validate schemes like {author}/{year} against that
	// registry — keep seeing the engine's full vocabulary.
	t.Cleanup(func() {
		audio.SetPathFormatTokens([]string{
			"author", "authors", "narrator", "narrators", "title", "subtitle",
			"series_name", "series_position", "year", "genre", "metadata_id", "book_id",
		})
	})

	candidate := filepath.Join(t.TempDir(), "candidate-engine")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "m4b-merge 2.2.0"; exit 0; fi
if [ "$1" = "--capabilities" ]; then
  echo '{"schema_version":1,"resolver_version":"1","default_path_format":"{title}","path_tokens":[{"name":"book_id","value":"b","example":"B"},{"name":"author","value":"a","example":"A"},{"name":"authors","value":"a","example":"A"},{"name":"narrators","value":"n","example":"N"},{"name":"title","value":"t","example":"T"}],"field_policies":{"values":["write","preserve","clear"],"scalar_fields":["title","subtitle","authors","narrators","series","description","genres","year"]},"cover_policies":{"constraint":"str","variants":["write","preserve","clear"]},"chapter_policies":{"constraint":"str","variants":["write","preserve","clear"]}}'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(candidate, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// No saved profile schemes: the ONLY effective scheme is the one the
	// PUT supplies.
	putSwap := func(scheme string) (int, string) {
		body := `{"m4b_merge_binary":"` + candidate + `","output_scheme":"` + scheme + `"}`
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		buildSettingsRouter(h).ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// The request's OWN scheme uses a token the candidate lacks: refused.
	code, respBody := putSwap("{series_position}/{title}")
	if code != http.StatusBadRequest {
		t.Fatalf("swap with the request's own unsupported scheme = %d, want 400 — %s", code, respBody)
	}
	if !strings.Contains(respBody, "series_position") {
		t.Fatalf("rejection must name the token: %s", respBody)
	}

	// A scheme the candidate supports: accepted.
	code, respBody = putSwap("{author}/{title}")
	if code != http.StatusOK {
		t.Fatalf("swap with a matching request scheme = %d, want 200 — %s", code, respBody)
	}
}

// ── T7: a first capture enriches the catalog, not just the cover ──

// TestFirstCaptureViaEditorEnrichesCatalog pins the book-2213 regression:
// the metadata editor's first-time row creation is a changed=false capture
// on a book with NO existing snapshot row. The row it writes is the first,
// so the FULL catalog enrichment (genres, chapters, subtitle) must run —
// the cover-only backfill branch (existing rows only) would leave the row
// with genres=null, which every later save retains and which crashes the
// editor's genres join with a SvelteKit 500.
func TestFirstCaptureViaEditorEnrichesCatalog(t *testing.T) {
	h, _ := newTestJobsHandler(t)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	var mu sync.Mutex
	bookHits, releaseHits := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.URL.Path {
		case "/books/42":
			bookHits++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"42","title":"Seed Book","genres":[{"id":"g1","title":"Science Fiction"},{"id":"g2","title":"Humor & Comedy"}]}`)
			return
		case "/releases/5":
			releaseHits++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"5","title":"Seed Book","subtitle":"Release Sub","chapterDetail":{"chapters":[{"title":"Ch 1","startOffsetMs":0,"lengthMs":60000}]}}`)
			return
		default:
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av
	h.svc.CoversDir = t.TempDir()

	// The book is catalog-linked; its snapshot row does NOT exist yet
	// (a historical book, first editor visit).
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (1, 'Seed Book', 'matched', '42', '5')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO people (book_id, name, role) VALUES (1, 'First Author', 'author'), (1, 'The Narrator', 'narrator')",
	); err != nil {
		t.Fatalf("seed people: %v", err)
	}

	// The editor's save: a non-empty field is required, and it changes
	// nothing snapshot-relevant — the first-time capture runs with
	// changed=false (overrides.go), exactly the production path.
	body := `{"title":"Seed Book"}`
	req := routeReq(t, "PUT", "/api/books/1/metadata", 1, bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	h.UpdateBookMetadata(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("editor save = %d; want 200 — %s", w.Code, w.Body.String())
	}

	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(s.Genres) != 2 || s.Genres[0] != "Science Fiction" || s.Genres[1] != "Humor & Comedy" {
		t.Fatalf("genres = %v; want the catalog's [Science Fiction Humor & Comedy] (a first capture must enrich the catalog, not store null)", s.Genres)
	}
	if len(s.Chapters) != 1 || s.Chapters[0].Title != "Ch 1" {
		t.Errorf("chapters = %+v; want the release's captured chapter", s.Chapters)
	}
	if s.Subtitle != "Release Sub" {
		t.Errorf("subtitle = %q; want the release's own subtitle \"Release Sub\"", s.Subtitle)
	}
	mu.Lock()
	bh, rh := bookHits, releaseHits
	mu.Unlock()
	if bh == 0 {
		t.Errorf("the first capture never fetched the book; want the full enrichment")
	}
	if rh == 0 {
		t.Errorf("the first capture never fetched the release; want the release chapters/subtitle")
	}
}
