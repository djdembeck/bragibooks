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
	if ok, _ := mustFile(filepath.Join(covers, "1-snapshot.jpg")); ok {
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
	// over (or delete on failure) an earlier capture's file. It takes the
	// SELECTED artwork's URL (the persisted cover_image_url), not a Book:
	// an empty URL is the no-cover case.
	n1, ok1 := h.storeCoverForBook(t.Context(), 1, &BookSnapshot{BookID: 1}, "") // no cover: fail path
	if ok1 || n1 != "" {
		t.Fatalf("no-cover store = (%q, %v); want (false, \"\")", n1, ok1)
	}
	// A failing download with a cover present must remove only its own new
	// file; the referenced one stays.
	if _, ok := h.storeCoverForBook(t.Context(), 1, &BookSnapshot{BookID: 1}, "http://127.0.0.1:1/x.jpg"); ok {
		t.Fatalf("loopback cover download must fail the capture, not write a file")
	}
	if ok, _ := mustFile(filepath.Join(covers, "1-111.jpg")); !ok {
		t.Fatalf("referenced cover file removed by a failed later capture")
	}
}

// TestCaptureSnapshotDegradedRetentionChangesRelease pins identity-scoped
// degraded retention: when the saved selection changes the RELEASE (same
// book) and the catalog is down, the old release's chapters must NOT survive
// (a re-match to a new release starts chapter-empty); the book-level genres
// and cover DO survive (same book).
func TestCaptureSnapshotDegradedRetentionChangesRelease(t *testing.T) {
	h := newTestHandler(t)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", "http://127.0.0.1:1")) // catalog down
	h.svc.AudiobookDB = av
	h.svc.CoversDir = t.TempDir()

	seedSnapshotBook(t, h, 1, "42", "1-old.jpg") // existing row: release '5'

	w := doUpdateBook(t, h, 1, UpdateBookRequest{AudiobookdbReleaseID: ptrStr("9")})
	if w.Code != http.StatusOK {
		t.Fatalf("catalog-down release re-match = %d; want 200 (degradation is not a failure)", w.Code)
	}
	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if s.ADBReleaseID != "9" {
		t.Fatalf("release = %q; want 9", s.ADBReleaseID)
	}
	if len(s.Chapters) != 0 {
		t.Errorf("chapters = %+v; want EMPTY (release changed, chapter details unavailable)", s.Chapters)
	}
	if len(s.Genres) != 2 || s.Genres[0] != "Mystery" {
		t.Errorf("genres = %v; want the book-level genres retained (same book)", s.Genres)
	}
	if s.CoverFile != "1-old.jpg" {
		t.Errorf("cover = %q; want the book-level cover retained (same book)", s.CoverFile)
	}
}

// TestCaptureSnapshotDegradedRetentionChangesBook pins the book-level scope:
// when the saved selection re-matches to a DIFFERENT AudiobookDB book (or
// unlinks) and the catalog is down, the previous book's genres, cover, and
// chapters must all start empty — none of it belongs to the new selection.
func TestCaptureSnapshotDegradedRetentionChangesBook(t *testing.T) {
	h := newTestHandler(t)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", "http://127.0.0.1:1")) // catalog down
	h.svc.AudiobookDB = av
	h.svc.CoversDir = t.TempDir()

	seedSnapshotBook(t, h, 1, "42", "1-old.jpg")

	// Re-match to a different book.
	w := doUpdateBook(t, h, 1, UpdateBookRequest{AudiobookdbBookID: ptrStr("43")})
	if w.Code != http.StatusOK {
		t.Fatalf("catalog-down book re-match = %d; want 200", w.Code)
	}
	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if s.ADBBookID != "43" {
		t.Fatalf("book id = %q; want 43", s.ADBBookID)
	}
	if len(s.Genres) != 0 {
		t.Errorf("genres = %v; want EMPTY (previous book's content must not leak into the new selection)", s.Genres)
	}
	if s.CoverFile != "" {
		t.Errorf("cover = %q; want EMPTY (previous book's artwork)", s.CoverFile)
	}
	if len(s.Chapters) != 0 {
		t.Errorf("chapters = %+v; want EMPTY (previous selection's chapters)", s.Chapters)
	}

	// Unlink to no catalog link: same rules, empty book id.
	w = doUpdateBook(t, h, 1, UpdateBookRequest{AudiobookdbBookID: ptrStr(""), AudiobookdbReleaseID: ptrStr("")})
	if w.Code != http.StatusOK {
		t.Fatalf("catalog-down unlink = %d; want 200", w.Code)
	}
	s, err = h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if s.ADBBookID != "" {
		t.Errorf("book id = %q; want empty after unlink", s.ADBBookID)
	}
	if len(s.Genres) != 0 || s.CoverFile != "" || len(s.Chapters) != 0 {
		t.Errorf("genres/cover/chapters = %v/%q/%+v; want all empty after unlink with catalog down", s.Genres, s.CoverFile, s.Chapters)
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

// TestCaptureSnapshotRetainsReleaseSubtitleOnReleaseFailure pins the
// degraded-capture contract for the subtitle: with an unchanged selection,
// the previously captured RELEASE subtitle is restored up front, so a
// GetBook-success/GetRelease-failure refresh must KEEP it — the book
// fallback only applies when no retained value belongs to that selection.
func TestCaptureSnapshotRetainsReleaseSubtitleOnReleaseFailure(t *testing.T) {
	h := newTestHandler(t)

	// Catalog: /books/42 succeeds with a book-level subtitle; /releases/5
	// FAILS, so the release-specific subtitle cannot be re-established.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/books/42":
			fmt.Fprint(w, `{"id":"42","title":"Seed Book","subtitle":"Book Sub"}`)
		case "/releases/5":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"release detail exploded"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	// Seed the catalog-linked book and a capture that already holds the
	// release-specific subtitle ("Release Sub") for the SAME selection
	// (book 42, release 5).
	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (1, 'Seed Book', 'matched', '42', '5')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, subtitle, genres, cover_file, chapters, captured_at)
		 VALUES (1, '42', '5', 'Seed Book', '[]', '[]', 'Release Sub', '[]', '', '[]', datetime("now"))`,
	); err != nil {
		t.Fatalf("seed snapshot row: %v", err)
	}

	// Unchanged selection: the release id stays '5', so the retained
	// release subtitle belongs to this selection.
	w := doUpdateBook(t, h, 1, UpdateBookRequest{Title: ptrStr("Edited Title")})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d; want 200", w.Code)
	}

	s, err := h.loadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if s.Subtitle != "Release Sub" {
		t.Fatalf("subtitle = %q; want the retained release subtitle \"Release Sub\" (a release-detail failure must not replace it with the book fallback)", s.Subtitle)
	}
	if s.Title != "Edited Title" {
		t.Errorf("title = %q; want the refreshed local field", s.Title)
	}
}

// TestCaptureSnapshotRetainsReleaseSubtitleOnEmptyRelease pins the companion
// case: the release fetch SUCCEEDS but the release carries no subtitle of
// its own. The book-level subtitle must not then overwrite the retained
// release-specific subtitle — only the release's OWN subtitle (or nothing
// retained) reaches the book fallback.
func TestCaptureSnapshotRetainsReleaseSubtitleOnEmptyRelease(t *testing.T) {
	h := newTestHandler(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/books/42":
			fmt.Fprint(w, `{"id":"42","title":"Seed Book","subtitle":"Book Sub"}`)
		case "/releases/5":
			fmt.Fprint(w, `{"id":"5","title":"Seed Book"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	av := &atomic.Value{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	if _, err := h.svc.DB.Exec(
		"INSERT INTO books (id, title, status, audiobookdb_book_id, audiobookdb_release_id) VALUES (1, 'Seed Book', 'matched', '42', '5')",
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
			authors, narrators, subtitle, genres, cover_file, chapters, captured_at)
		 VALUES (1, '42', '5', 'Seed Book', '[]', '[]', 'Release Sub', '[]', '', '[]', datetime("now"))`,
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
	if s.Subtitle != "Release Sub" {
		t.Fatalf("subtitle = %q; want the retained release subtitle \"Release Sub\" (a release that carries no subtitle of its own must not trigger the book fallback over a retained value)", s.Subtitle)
	}
}
