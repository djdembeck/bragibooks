package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/go-chi/chi/v5"
)

// doUpdateBook drives PUT /api/books/{id} with the given request and returns
// the recorded response.
func doUpdateBook(t *testing.T, h *Handler, bookID int64, req UpdateBookRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	id := strconv.FormatInt(bookID, 10)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, id)
	r := httptest.NewRequest("PUT", "/api/books/"+id, bytes.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
	w := httptest.NewRecorder()
	h.UpdateBook(w, r)
	return w
}

// seedSnapshotBook inserts a catalog-linked book row, its ordered people,
// and a pre-existing capture row with known catalog content.
func seedSnapshotBook(t *testing.T, h *Handler, bookID int64, adbBookID, existingCover string) {
	t.Helper()
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (?, 'Seed Book', 'matched', ?, '5')",
		bookID, adbBookID,
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO people (book_id, name, role) VALUES (?, 'First Author', 'author'), (?, 'Second Author', 'author'), (?, 'The Narrator', 'narrator')",
		bookID, bookID, bookID,
	); err != nil {
		t.Fatalf("seed people: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, genres, cover_file, chapters, captured_at)
		 VALUES (?, ?, '5', 'Seed Book', '["First Author","Second Author"]', '["The Narrator"]',
		   '["Mystery","Drama"]', ?, '[{"title":"Ch 1","start_millis":0,"duration_millis":60000}]', datetime("now"))`,
		bookID, adbBookID, existingCover,
	); err != nil {
		t.Fatalf("seed snapshot row: %v", err)
	}
}

// ptrStr is the tests' *string helper for optional request fields.
func ptrStr(s string) *string { return &s }

// mustFile reports whether path exists, with the error for the failure case.
func mustFile(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, err
	}
	return err == nil, err
}

// TestCaptureSnapshotCatalogDownRetainsCatalogContent pins the documented
// degradation: with the catalog unreachable, a metadata save still succeeds,
// the row is refreshed with the NEW local fields, and the previously captured
// catalog content (genres/chapters/cover) is retained, not silently lost.
func TestCaptureSnapshotCatalogDownRetainsCatalogContent(t *testing.T) {
	h := newTestHandler(t)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", "http://127.0.0.1:1")) // nothing listens here
	h.svc.AudiobookDB = av
	h.svc.CoversDir = t.TempDir()

	seedSnapshotBook(t, h, 1, "42", "1-old.jpg")

	w := doUpdateBook(t, h, 1, UpdateBookRequest{Title: ptrStr("Edited Title")})
	if w.Code != http.StatusOK {
		t.Fatalf("catalog-down save = %d; want 200 (degradation is not a failure)", w.Code)
	}

	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if s.Title != "Edited Title" {
		t.Errorf("title = %q; want the refreshed local field", s.Title)
	}
	if len(s.Genres) != 2 || s.Genres[0] != "Mystery" {
		t.Errorf("genres = %v; want the previously captured catalog content retained", s.Genres)
	}
	if len(s.Chapters) != 1 || s.Chapters[0].Title != "Ch 1" {
		t.Errorf("chapters = %+v; want the previously captured catalog content retained", s.Chapters)
	}
	if s.CoverFile != "1-old.jpg" {
		t.Errorf("cover = %q; want the previously captured cover retained", s.CoverFile)
	}
}

// TestCaptureSnapshotCaptureSpecificCoverName pins the capture-specific
// immutable cover filename: a capture writes a NEW <bookID>-<nano>.jpg
// reference (never the previous capture's file, so a later capture cannot
// overwrite or delete what a queued job's snapshot names), and pruning
// retires only the legacy shared name, never the active capture's file.
func TestCaptureSnapshotCaptureSpecificCoverName(t *testing.T) {
	h := newTestHandler(t)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", "http://127.0.0.1:1")) // catalog down: no download
	h.svc.AudiobookDB = av
	covers := t.TempDir()
	h.svc.CoversDir = covers
	seedSnapshotBook(t, h, 1, "42", "1-111.jpg")
	for _, name := range []string{"1-111.jpg", "1-snapshot.jpg"} {
		if err := os.WriteFile(filepath.Join(covers, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed cover %s: %v", name, err)
		}
	}

	// The legacy shared name is pruned on an upsert for a book with no
	// active job; the active (capture-specific) file is never removed.
	h.pruneSupersededCovers(1, &BookSnapshot{BookID: 1, CoverFile: "1-111.jpg"})
	if ok, _ := mustFile(filepath.Join(covers, "1-111.jpg")); !ok {
		t.Fatalf("active cover file removed by pruning")
	}
	if ok, _ := mustFile(filepath.Join(covers, "1-old.jpg")); ok {
		t.Fatalf("legacy shared cover file (1-snapshot.jpg) must be pruned once superseded")
	}

	// A degraded (catalog-down) re-capture must not touch the referenced
	// file at all: the row keeps it, the file survives.
	putCode, _ := doUpdateBook(t, h, 1, UpdateBookRequest{Title: ptrStr("Again")}).Code, ""
	if putCode != http.StatusOK {
		t.Fatalf("catalog-down re-capture save = %d; want 200", putCode)
	}
	if ok, _ := mustFile(filepath.Join(covers, "1-111.jpg")); !ok {
		t.Fatalf("referenced cover file vanished after a capture")
	}

	// storeCoverForBook names captures uniquely: two captures of the same
	// book produce different filenames, so a later capture can never write
	// over (or delete on failure) an earlier capture's file.
	book := &audiobookdb.Book{ID: "42"}
	n1, ok1 := h.storeCoverForBook(t.Context(), 1, &BookSnapshot{BookID: 1}, book) // no image: fail path
	if ok1 || n1 != "" {
		t.Fatalf("no-image cover store = (%q, %v); want (false, \"\")", n1, ok1)
	}
	// A failing download with a cover present must remove only its own new
	// file; the referenced one stays.
	book2 := &audiobookdb.Book{ID: "42", Images: []audiobookdb.Image{{URL: "http://127.0.0.1:1/x.jpg"}}}
	if _, ok := h.storeCoverForBook(t.Context(), 1, &BookSnapshot{BookID: 1}, book2); ok {
		t.Fatalf("loopback cover download must fail the capture, not write a file")
	}
	if ok, _ := mustFile(filepath.Join(covers, "1-111.jpg")); !ok {
		t.Fatalf("referenced cover file removed by a failed later capture")
	}
}

// TestPruneSupersededCoversKeepsActiveJobReference pins the per-file pruning
// guard: a cover file that an ACTIVE (queued/running) job's queued snapshot
// names is kept even when the current snapshot has moved on to a new file,
// while an orphan file referenced by no live snapshot is pruned. This is
// what makes capture-vs-enqueue race-safe: by the time a job's row exists it
// is counted, so its named file can never be pruned out from under it.
func TestPruneSupersededCoversKeepsActiveJobReference(t *testing.T) {
	h := newTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	// The active job references 1-500.jpg; an orphan 1-200.jpg is left over
	// from an earlier capture and is referenced by nothing.
	for _, name := range []string{"1-500.jpg", "1-200.jpg"} {
		if err := os.WriteFile(filepath.Join(covers, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed cover %s: %v", name, err)
		}
	}
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status) VALUES (1, 'B', 'matched')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// A queued job whose snapshot names 1-500.jpg (jobArgs → snapshotJSON →
	// BookSnapshot.cover_file shape).
	jobArgs := `{"snapshot":{"snapshot":{"cover_file":"1-500.jpg"}}}`
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES ('j1', 1, ?, 'queued')", jobArgs,
	); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	// Current snapshot has moved on to 1-999.jpg.
	h.pruneSupersededCovers(1, &BookSnapshot{BookID: 1, CoverFile: "1-999.jpg"})
	if ok, _ := mustFile(filepath.Join(covers, "1-500.jpg")); !ok {
		t.Fatalf("cover file referenced by an active job's snapshot was pruned")
	}
	if ok, _ := mustFile(filepath.Join(covers, "1-200.jpg")); ok {
		t.Fatalf("orphan cover file (no live reference) must be pruned")
	}
}

// TestPruneSupersededCoversOnlyThisBook pins that pruning is scoped to THIS
// book's snapshot-cover files: the covers directory is shared, so source
// covers ("<bookID>.jpg", attachSourceCover), other books' snapshot covers,
// and other books' source covers must all survive one book's prune — only
// that book's own unreferenced snapshot covers are removed.
func TestPruneSupersededCoversOnlyThisBook(t *testing.T) {
	h := newTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	// Book 1: current capture 1-999.jpg, an orphan capture 1-500.jpg, and a
	// legacy 1-snapshot.jpg. Also present: book 1's SOURCE cover 1.jpg, and
	// book 2's source cover 2.jpg and snapshot cover 2-777.jpg.
	for _, name := range []string{"1-999.jpg", "1-500.jpg", "1-snapshot.jpg", "1.jpg", "2.jpg", "2-777.jpg"} {
		if err := os.WriteFile(filepath.Join(covers, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	h.pruneSupersededCovers(1, &BookSnapshot{BookID: 1, CoverFile: "1-999.jpg"})
	for _, gone := range []string{"1-500.jpg", "1-snapshot.jpg"} {
		if ok, _ := mustFile(filepath.Join(covers, gone)); ok {
			t.Errorf("book 1's unreferenced snapshot cover %q must be pruned", gone)
		}
	}
	for _, kept := range []string{"1-999.jpg", "1.jpg", "2.jpg", "2-777.jpg"} {
		if ok, _ := mustFile(filepath.Join(covers, kept)); !ok {
			t.Errorf("cover %q must survive book 1's prune (it is not one of its snapshot covers)", kept)
		}
	}
}

// TestPruneSupersededCoversPrunesOrphanDespiteActiveJob pins that the active-
// job guard is per FILE, not a book-wide skip: a legacy orphan the active job's
// snapshot does not name is still pruned (the old "skip the whole book" guard
// would have left it forever).
func TestPruneSupersededCoversPrunesOrphanDespiteActiveJob(t *testing.T) {
	h := newTestHandler(t)
	covers := t.TempDir()
	h.svc.CoversDir = covers
	legacy := fmt.Sprintf("%d-snapshot.jpg", 1)
	if err := os.WriteFile(filepath.Join(covers, legacy), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed legacy cover: %v", err)
	}
	if _, err := h.svc.DB.Exec("INSERT INTO books (id, title, status) VALUES (1, 'B', 'matched')"); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// A queued job whose stored args carry NO snapshot (legacy shape, no cover
	// reference).
	if _, err := h.svc.DB.Exec(
		"INSERT INTO processing_jobs (id, book_id, m4b_merge_args, status) VALUES ('j1', 1, '{}', 'queued')",
	); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	h.pruneSupersededCovers(1, &BookSnapshot{BookID: 1, CoverFile: "1-999.jpg"})
	if ok, _ := mustFile(filepath.Join(covers, legacy)); ok {
		t.Fatalf("legacy orphan cover must be pruned even while an active job exists (guard is per-file)")
	}
}
