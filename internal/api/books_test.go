package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/models"
)

// schemaSQL creates the tables required for the books API tests.
const schemaSQL = `
CREATE TABLE books (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    asin TEXT,
    audiobookdb_book_id TEXT,
    audiobookdb_release_id TEXT,
    description TEXT DEFAULT '',
    release_date TEXT DEFAULT '',
    series TEXT DEFAULT '',
    publisher TEXT DEFAULT '',
    language TEXT DEFAULT '',
    runtime_length_minutes INTEGER DEFAULT 0,
    format_type TEXT DEFAULT '',
    src_path TEXT DEFAULT '',
    dest_path TEXT DEFAULT '',
    status TEXT DEFAULT 'pending',
    status_message TEXT DEFAULT '',
    cover_image_url TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    converted BOOLEAN DEFAULT 0
);

CREATE TABLE people (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('author', 'narrator')),
	audiobookdb_person_id TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE output_profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    output_dir TEXT NOT NULL DEFAULT '',
    output_scheme TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE book_source_metadata (
    book_id INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    payload TEXT NOT NULL,
    extracted_at TEXT NOT NULL DEFAULT (datetime('now'))
);
` + processingSchemaSQL

// newTestDB opens an in-memory SQLite database and creates the schema.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// newTestHandler returns a Handler backed by an in-memory SQLite DB.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{svc: &Services{DB: newTestDB(t)}}
}

func TestUpdateBookMultiFieldSQLConstruction(t *testing.T) {
	h := newTestHandler(t)

	// Seed a book so the UPDATE has a target.
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'Original Title', 'pending')`)

	newTitle := "New Title"
	newStatus := "done"
	reqBody, err := json.Marshal(UpdateBookRequest{
		Title:  &newTitle,
		Status: &newStatus,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, "1")
	req := httptest.NewRequest("PUT", "/api/books/1", bytes.NewReader(reqBody))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	w := httptest.NewRecorder()
	h.UpdateBook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d — body: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Verify the book was actually updated (the broken SQL would error out).
	var title, status string
	err = h.svc.DB.QueryRow("SELECT title, status FROM books WHERE id = ?", 1).Scan(&title, &status)
	if err != nil {
		t.Fatalf("query updated book: %v", err)
	}
	if title != newTitle {
		t.Errorf("title = %q; want %q", title, newTitle)
	}
	if status != newStatus {
		t.Errorf("status = %q; want %q", status, newStatus)
	}
}

func TestUpdateBookMissingReturns404(t *testing.T) {
	h := newTestHandler(t)

	newTitle := "Ghost Title"
	reqBody, err := json.Marshal(UpdateBookRequest{Title: &newTitle})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, "9999")
	req := httptest.NewRequest("PUT", "/api/books/9999", bytes.NewReader(reqBody))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	w := httptest.NewRecorder()
	h.UpdateBook(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing book status = %d, want 404 — body: %s", w.Code, w.Body.String())
	}
}

func TestListBooksLimitClamp(t *testing.T) {
	h := newTestHandler(t)
	for i := range 3 {
		if _, err := h.svc.DB.Exec("INSERT INTO books (title) VALUES (?)", fmt.Sprintf("Title %d", i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// 500 is the new maximum and must be honored, not reset.
	req := httptest.NewRequest("GET", "/api/books?limit=500", nil)
	w := httptest.NewRecorder()
	h.ListBooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
	var resp ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Limit != 500 {
		t.Errorf("limit=500 -> resp limit %d; want 500", resp.Limit)
	}

	// Above 500 is clamped down, not reset to the default.
	req = httptest.NewRequest("GET", "/api/books?limit=5000", nil)
	w = httptest.NewRecorder()
	h.ListBooks(w, req)
	var resp2 ListBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.Limit != 500 {
		t.Errorf("limit=5000 -> resp limit %d; want 500", resp2.Limit)
	}
}

func TestCreateBookWithNULLReleaseDate(t *testing.T) {
	h := newTestHandler(t)

	srcPath := "/some/path/Author_Book_Title.m4b"
	reqBody, err := json.Marshal(CreateBooksRequest{
		Books: []CreateBookEntry{
			{SrcPath: srcPath},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/books", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.CreateBooks(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d — body: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	var resp CreateBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("expected 1 book in response, got %d", len(resp.Books))
	}

	book := resp.Books[0]

	// The title should be derived from the src_path since none was provided.
	expectedTitle := "Author Book Title"
	if book.Title != expectedTitle {
		t.Errorf("title = %q; want %q", book.Title, expectedTitle)
	}

	// The book should have been persisted with status "pending".
	var dbStatus string
	err = h.svc.DB.QueryRow("SELECT status FROM books WHERE id = ?", book.ID).Scan(&dbStatus)
	if err != nil {
		t.Fatalf("query book status: %v", err)
	}
	if dbStatus != "pending" {
		t.Errorf("db status = %q; want %q", dbStatus, "pending")
	}
}

// stubFFmpegForCovers returns a fake ffmpeg that records its argv and
// writes deterministic bytes to the final argument (the output path).
func stubFFmpegForCovers(t *testing.T, argsFile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nfor last; do :; done\nprintf 'fake-jpeg' > \"$last\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffmpeg: %v", err)
	}
	return path
}

// stubFFprobeForMetadata returns a fake ffprobe plus the directory holding
// its per-file responses. The script derives the input basename from its
// last argv and cats <respDir>/<basename>.json; a missing sidecar makes it
// fail, so per-file failures are expressible.
func stubFFprobeForMetadata(t *testing.T) (string, string) {
	t.Helper()
	respDir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
for last; do :; done
base=$(basename "$last")
if [ -f "` + respDir + `/${base}.json" ]; then
  cat "` + respDir + `/${base}.json"
else
  echo "probe: no such file" >&2
  exit 1
fi
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}
	return bin, respDir
}

func writeProbeSidecar(t *testing.T, respDir, name, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(respDir, name+".json"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write probe sidecar %s: %v", name, err)
	}
}

// metadataPayloadFor fetches and decodes the book's source-metadata row.
func metadataPayloadFor(t *testing.T, db *sql.DB, bookID int64) map[string]any {
	t.Helper()
	var payload string
	if err := db.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = ?", bookID).Scan(&payload); err != nil {
		t.Fatalf("fetch source metadata row: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		t.Fatalf("decode source metadata payload: %v", err)
	}
	return snap
}

// postBook drives CreateBooks directly and decodes the created book.
func postBook(t *testing.T, h *Handler, srcPath string) models.BookWithPeople {
	t.Helper()
	reqBody, err := json.Marshal(CreateBooksRequest{Books: []CreateBookEntry{{SrcPath: srcPath}}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/books", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.CreateBooks(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201 — body: %s", w.Code, w.Body.String())
	}
	var resp CreateBooksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Books) != 1 {
		t.Fatalf("created %d books; want 1", len(resp.Books))
	}
	return resp.Books[0]
}

func TestCreateBooksAttachesSourceCover(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "cover.jpg"), []byte("source-art"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	coversDir := t.TempDir()
	h := &Handler{svc: &Services{
		DB:        newTestDB(t),
		CoversDir: coversDir,
		FFmpeg:    stubFFmpegForCovers(t, filepath.Join(t.TempDir(), "args")),
	}}

	book := postBook(t, h, srcDir)
	if book.CoverImageURL != "/covers/1.jpg" {
		t.Fatalf("cover_image_url = %q; want /covers/1.jpg", book.CoverImageURL)
	}

	// The served URL must resolve to the file the extractor wrote.
	got, err := os.ReadFile(filepath.Join(coversDir, "1.jpg"))
	if err != nil {
		t.Fatalf("read extracted cover: %v", err)
	}
	if string(got) != "fake-jpeg" {
		t.Errorf("extracted cover = %q; want the ffmpeg output", got)
	}
}

func TestCreateBooksWithoutSourceArtKeepsPlaceholder(t *testing.T) {
	coversDir := t.TempDir()
	h := &Handler{svc: &Services{
		DB:        newTestDB(t),
		CoversDir: coversDir,
		FFmpeg:    stubFFmpegForCovers(t, filepath.Join(t.TempDir(), "args")),
	}}

	// An empty folder carries neither a sidecar image nor audio.
	book := postBook(t, h, t.TempDir())
	if book.CoverImageURL != "" {
		t.Fatalf("cover_image_url = %q; want empty for an art-less source", book.CoverImageURL)
	}
	entries, err := os.ReadDir(coversDir)
	if err != nil {
		t.Fatalf("read covers dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("covers dir has %d files; want none", len(entries))
	}
}

func TestDeleteBookRemovesExtractedCover(t *testing.T) {
	coversDir := t.TempDir()
	h := &Handler{svc: &Services{DB: newTestDB(t), CoversDir: coversDir}}

	res, err := h.svc.DB.Exec("INSERT INTO books (title, status) VALUES ('Delete Me', 'pending')")
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	bookID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("book id: %v", err)
	}
	coverPath := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", bookID))
	if err := os.WriteFile(coverPath, []byte("art"), 0o644); err != nil {
		t.Fatalf("seed cover: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, fmt.Sprint(bookID))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/books/%d", bookID), nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	w := httptest.NewRecorder()
	h.DeleteBook(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204 — body: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(coverPath); !os.IsNotExist(err) {
		t.Errorf("cover file still present after delete: %v", err)
	}
}

func TestDeriveTitleStripsOnlyAudioExtensions(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "author initial before title",
			path: "/library/Ursula K. Le Guin - The Left Hand of Darkness",
			want: "Ursula K Le Guin The Left Hand Of Darkness",
		},
		{
			name: "author initial with audio extension",
			path: "/library/Ursula K. Le Guin - The Left Hand of Darkness.m4b",
			want: "Ursula K Le Guin The Left Hand Of Darkness",
		},
		{
			name: "volume number",
			path: "/library/Book Vol. 2",
			want: "Book Vol 2",
		},
		{
			name: "single audio file",
			path: "/some/path/Author_Book_Title.m4b",
			want: "Author Book Title",
		},
		{
			name: "apostrophe contraction",
			path: "/library/01 Kane's Fate.mp3",
			want: "01 Kane's Fate",
		},
		{
			name: "typographic apostrophe contraction",
			path: "/library/Charlotte’s Web.m4b",
			want: "Charlotte’s Web",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTitle(tc.path); got != tc.want {
				t.Errorf("deriveTitle(%q) = %q; want %q", tc.path, got, tc.want)
			}
		})
	}
}

// newTestHandlerWithJobs returns a handler whose ProcessingService is real,
// so RecordBookAction writes (and tests can assert) terminal audit rows.
func newTestHandlerWithJobs(t *testing.T) *Handler {
	t.Helper()
	db := newTestDB(t)
	return &Handler{svc: &Services{DB: db, ProcessingSvc: NewProcessingService(db, &audio.Processor{}, 1)}}
}

func TestCreateBooksRecordsImportAuditRow(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	srcPath := "/some/path/Author_Book_Title.m4b"
	reqBody, err := json.Marshal(CreateBooksRequest{Books: []CreateBookEntry{{SrcPath: srcPath}}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/books", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.CreateBooks(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201 — body: %s", w.Code, w.Body.String())
	}

	var count int
	if err = h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("processing_jobs rows = %d; want exactly 1", count)
	}
	var dbStatus, dbAction, dbDetail string
	var dbBookID int64
	err = h.svc.DB.QueryRow("SELECT status, action, book_id, detail FROM processing_jobs").Scan(&dbStatus, &dbAction, &dbBookID, &dbDetail)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if dbStatus != "done" {
		t.Errorf("status = %q; want done", dbStatus)
	}
	if dbAction != "import" {
		t.Errorf("action = %q; want import", dbAction)
	}
	if dbBookID != 1 {
		t.Errorf("book_id = %d; want 1", dbBookID)
	}
	if dbDetail != srcPath {
		t.Errorf("detail = %q; want the source path %q", dbDetail, srcPath)
	}
}

func TestUpdateBookRecordsMetadataAuditRow(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'Original Title', 'pending')`)

	newTitle := "Changed Title"
	reqBody, err := json.Marshal(UpdateBookRequest{Title: &newTitle})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("PUT", "/api/books/1", bytes.NewReader(reqBody))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.UpdateBook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}

	var count int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("processing_jobs rows = %d; want exactly 1", count)
	}
	var dbStatus, dbAction, dbDetail string
	var dbBookID int64
	if err := h.svc.DB.QueryRow("SELECT status, action, book_id, detail FROM processing_jobs").Scan(&dbStatus, &dbAction, &dbBookID, &dbDetail); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if dbStatus != "done" {
		t.Errorf("status = %q; want done", dbStatus)
	}
	if dbAction != "metadata_update" {
		t.Errorf("action = %q; want metadata_update", dbAction)
	}
	if dbBookID != 1 {
		t.Errorf("book_id = %d; want 1", dbBookID)
	}
	if dbDetail != "Changed Title" {
		t.Errorf("detail = %q; want the book title", dbDetail)
	}
}

func TestUpdateBookLifecycleOnlyWritesNoAuditRow(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'My Book', 'matched')`)

	// The book page's re-match action: a bare status flip, plus the other
	// lifecycle fields (status message, dest path, format). None of these is
	// catalog metadata, so the record timeline must stay quiet.
	newStatus := "pending"
	newStatusMessage := "re-matching"
	newDestPath := "/out/My_Book.m4b"
	formatType := "m4b"
	reqBody, err := json.Marshal(UpdateBookRequest{
		Status:        &newStatus,
		StatusMessage: &newStatusMessage,
		DestPath:      &newDestPath,
		FormatType:    &formatType,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("PUT", "/api/books/1", bytes.NewReader(reqBody))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = append(rctx.URLParams.Keys, "id")
	rctx.URLParams.Values = append(rctx.URLParams.Values, "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.UpdateBook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}

	// The lifecycle fields still landed (the gate must not swallow the update)
	// — it only skips history for them.
	var status, dest, format string
	if err := h.svc.DB.QueryRow("SELECT status, dest_path, format_type FROM books WHERE id = 1").
		Scan(&status, &dest, &format); err != nil {
		t.Fatalf("read book: %v", err)
	}
	if status != newStatus || dest != newDestPath || format != formatType {
		t.Errorf("book update lost: (%q, %q, %q)", status, dest, format)
	}

	var count int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM processing_jobs").Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Errorf("processing_jobs rows = %d; want 0 (lifecycle-only update recorded no audit row)", count)
	}
}

func putBook(t *testing.T, h *Handler, bookID int64, body UpdateBookRequest) {
	t.Helper()
	reqBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, fmt.Sprint(bookID))
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/books/%d", bookID), bytes.NewReader(reqBody))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	w := httptest.NewRecorder()
	h.UpdateBook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; want 200 — body: %s", w.Code, w.Body.String())
	}
}

func sourceMetadataFields(t *testing.T, snap map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := snap["fields"].(map[string]any)
	if !ok {
		t.Fatalf("payload fields = %T, want object", snap["fields"])
	}
	out := make(map[string]map[string]any, len(raw))
	for key, value := range raw {
		field, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("payload fields[%s] = %T, want object", key, value)
		}
		out[key] = field
	}
	return out
}

func wantAllFieldStatuses(t *testing.T, snap map[string]any, status string) {
	t.Helper()
	fields := sourceMetadataFields(t, snap)
	if len(fields) != 11 {
		t.Errorf("payload carries %d fields, want 11", len(fields))
	}
	for key, field := range fields {
		if field["status"] != status {
			t.Errorf("field %s status = %v, want %s", key, field["status"], status)
		}
	}
}

func TestCreateBooksPersistsSourceMetadata(t *testing.T) {
	srcDir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	ffprobe, respDir := stubFFprobeForMetadata(t)
	writeProbeSidecar(t, respDir, "1.mp3",
		`{"format": {"duration": "300.0", "tags": {"title": "Stub Title", "album_artist": "Stub Author", "audible_asin": "B0STUBSTUB"}}, "streams": []}`)
	writeProbeSidecar(t, respDir, "2.mp3",
		`{"format": {"duration": "400.0", "tags": {"title": "Stub Title"}}, "streams": []}`)

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	book := postBook(t, h, srcDir)

	snap := metadataPayloadFor(t, h.svc.DB, book.ID)
	// New snapshots must carry the current schema: the pending-list
	// projection treats anything older as incomplete evidence, so a
	// producer that forgot the bump would silently disable prefilling.
	if snap["schema"] != float64(audio.SourceMetadataSchema) {
		t.Errorf("schema = %v, want %d", snap["schema"], audio.SourceMetadataSchema)
	}
	if snap["source_kind"] != "folder" {
		t.Errorf("source_kind = %v, want folder", snap["source_kind"])
	}
	if snap["files_total"] != float64(2) || snap["files_probed"] != float64(2) {
		t.Errorf("files_total/files_probed = %v/%v, want 2/2", snap["files_total"], snap["files_probed"])
	}
	if snap["duration_seconds"] != float64(700) {
		t.Errorf("duration_seconds = %v, want 700", snap["duration_seconds"])
	}
	fields := sourceMetadataFields(t, snap)
	if title, ok := fields["title"]; !ok || title["status"] != "present" {
		t.Fatalf("title field = %v, want present", title)
	}
	titleValues, _ := fields["title"]["values"].([]any)
	if len(titleValues) != 1 || titleValues[0] != "Stub Title" {
		t.Errorf("title values = %v, want [Stub Title]", titleValues)
	}
	if asin := fields["asin"]; asin["status"] != "present" || asin["source"] != "embedded" {
		t.Errorf("asin field = %v, want present/embedded", asin)
	}
}

func TestCreateBooksPersistsDegradedMetadataWithoutFFprobe(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	h := newTestHandler(t)
	book := postBook(t, h, srcDir)

	snap := metadataPayloadFor(t, h.svc.DB, book.ID)
	if snap["source_kind"] != "unknown" {
		t.Errorf("source_kind = %v, want unknown", snap["source_kind"])
	}
	wantAllFieldStatuses(t, snap, "error")
}

func TestCreateBooksIntakeSurvivesProbeFailure(t *testing.T) {
	srcDir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	// No sidecars: the stub exits 1 on every file.
	ffprobe, _ := stubFFprobeForMetadata(t)

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	book := postBook(t, h, srcDir)

	snap := metadataPayloadFor(t, h.svc.DB, book.ID)
	wantAllFieldStatuses(t, snap, "error")
	files, ok := snap["files"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("files = %v, want 2 per-file entries", snap["files"])
	}
	first, _ := files[0].(map[string]any)
	if first["error"] == nil || first["error"] == "" {
		t.Errorf("files[0].error empty, want the probe failure")
	}
}

// TestCreateBooksBatchBudgetSkipsLateMetadata checks that once the
// per-request metadata budget is exhausted the remaining books still get a
// row, but a degraded one with the budget-exceeded cause.
func TestCreateBooksBatchBudgetSkipsLateMetadata(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ffprobe, respDir := stubFFprobeForMetadata(t)
	writeProbeSidecar(t, respDir, "1.mp3",
		`{"format": {"duration": "300.0", "tags": {"title": "Stub Title"}}, "streams": []}`)

	oldBudget := maxSourceMetadataBatchBudget
	t.Cleanup(func() { maxSourceMetadataBatchBudget = oldBudget })
	// An already-passed deadline: the first book's attachment is skipped
	// too, which isolates the budget path from extraction.
	maxSourceMetadataBatchBudget = -time.Second

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	body, err := json.Marshal(CreateBooksRequest{Books: []CreateBookEntry{{SrcPath: srcDir}}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/books", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateBooks(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201 — body: %s", w.Code, w.Body.String())
	}

	var payload string
	if err := h.svc.DB.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = 1").Scan(&payload); err != nil {
		t.Fatalf("fetch source metadata row: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		t.Fatalf("decode source metadata payload: %v", err)
	}
	wantAllFieldStatuses(t, snap, "error")
	if !strings.Contains(snap["fields"].(map[string]any)["title"].(map[string]any)["error"].(string), "budget exhausted") {
		t.Errorf("title error = %v, want the budget-exceeded cause", snap["fields"])
	}
}

// TestCreateBooksBatchDeadlineCancelsInFlightExtraction checks that an
// extraction started just before the batch deadline is bounded by it: the
// probe pass is canceled when the budget expires, the book still gets a
// (degraded) row, and intake returns long before the probe would finish.
func TestCreateBooksBatchDeadlineCancelsInFlightExtraction(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// A slow ffprobe stand-in: sleeps 30s (detached from the pipe so the
	// kill does not leave a blocked writer) before emitting a successful
	// probe. If the deadline never canceled it, the row would say present.
	respDir := t.TempDir()
	writeProbeSidecar(t, respDir, "1.mp3",
		`{"format": {"duration": "300.0", "tags": {"title": "Stub Title"}}, "streams": []}`)
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
for last; do :; done
# The sleeper's stdio goes to /dev/null, not the inherited probe pipe:
# when the deadline SIGKILLs the shell, the orphan must not hold the
# write end open and block the probe reader for the full sleep.
sleep 30 >/dev/null 2>&1 &
wait
base=$(basename "$last")
cat "` + respDir + `/${base}.json"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}

	oldBudget := maxSourceMetadataBatchBudget
	t.Cleanup(func() { maxSourceMetadataBatchBudget = oldBudget })
	maxSourceMetadataBatchBudget = 300 * time.Millisecond

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: bin}}
	body, err := json.Marshal(CreateBooksRequest{Books: []CreateBookEntry{{SrcPath: srcDir}}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/books", bytes.NewReader(body))
	w := httptest.NewRecorder()
	start := time.Now()
	h.CreateBooks(w, req)
	elapsed := time.Since(start)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201 — body: %s", w.Code, w.Body.String())
	}

	// The probe was killed at the deadline, not run to success.
	snap := metadataPayloadFor(t, h.svc.DB, 1)
	wantAllFieldStatuses(t, snap, "error")
	fields := sourceMetadataFields(t, snap)
	if !strings.Contains(fields["title"]["error"].(string), "all 1 probed files failed") {
		t.Errorf("title error = %v, want the all-failed message", fields["title"])
	}
	files, ok := snap["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("files = %v, want one per-file entry", snap["files"])
	}
	first, _ := files[0].(map[string]any)
	if first["error"] == nil || first["error"] == "" {
		t.Errorf("files[0].error empty, want the canceled-probe failure")
	}
	if elapsed > 5*time.Second {
		t.Errorf("intake took %v, want the probe canceled at the ~300ms deadline", elapsed)
	}
}

func TestUpdateBookLeavesSourceMetadataUntouched(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ffprobe, respDir := stubFFprobeForMetadata(t)
	writeProbeSidecar(t, respDir, "1.mp3",
		`{"format": {"duration": "300.0", "tags": {"title": "Stub Title"}}, "streams": []}`)

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	book := postBook(t, h, srcDir)

	var before string
	if err := h.svc.DB.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = ?", book.ID).Scan(&before); err != nil {
		t.Fatalf("read payload before: %v", err)
	}

	newTitle := "Catalog Title"
	newASIN := "B0ZZZZZZZZ"
	newRuntime := 60
	putBook(t, h, book.ID, UpdateBookRequest{Title: &newTitle, ASIN: &newASIN, RuntimeLengthMinutes: &newRuntime})

	var title, asin string
	var runtime int
	if err := h.svc.DB.QueryRow("SELECT title, asin, runtime_length_minutes FROM books WHERE id = ?", book.ID).
		Scan(&title, &asin, &runtime); err != nil {
		t.Fatalf("query updated book: %v", err)
	}
	if title != newTitle || asin != newASIN || runtime != newRuntime {
		t.Errorf("book = %q/%q/%d, want %q/%q/%d", title, asin, runtime, newTitle, newASIN, newRuntime)
	}

	var after string
	if err := h.svc.DB.QueryRow("SELECT payload FROM book_source_metadata WHERE book_id = ?", book.ID).Scan(&after); err != nil {
		t.Fatalf("read payload after: %v", err)
	}
	if after != before {
		t.Errorf("source metadata payload changed on Save-match:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestDeleteBookCascadesSourceMetadata(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ffprobe, respDir := stubFFprobeForMetadata(t)
	writeProbeSidecar(t, respDir, "1.mp3",
		`{"format": {"duration": "300.0", "tags": {"title": "Stub Title"}}, "streams": []}`)

	h := &Handler{svc: &Services{DB: newTestDB(t), FFprobe: ffprobe}}
	book := postBook(t, h, srcDir)
	if _, err := h.svc.DB.Exec("SELECT 1 FROM book_source_metadata WHERE book_id = ?", book.ID); err != nil {
		t.Fatalf("row missing before delete: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, fmt.Sprint(book.ID))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/books/%d", book.ID), nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	w := httptest.NewRecorder()
	h.DeleteBook(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204 — body: %s", w.Code, w.Body.String())
	}

	var count int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM book_source_metadata WHERE book_id = ?", book.ID).Scan(&count); err != nil {
		t.Fatalf("count metadata rows: %v", err)
	}
	if count != 0 {
		t.Errorf("book_source_metadata rows = %d; want 0 (cascade)", count)
	}
}

// TestDeleteBookRefusesInFlightJob pins the F3 server guard: a book whose
// processing job is queued or running must not be deletable. Deleting it
// would leave the worker running against a book row the job no longer
// references (processing_jobs.book_id is ON DELETE SET NULL).
func TestDeleteBookRefusesInFlightJob(t *testing.T) {
	for _, status := range []string{jobStatusQueued, jobStatusRunning} {
		t.Run(status, func(t *testing.T) {
			h := newTestHandlerWithJobs(t)
			if _, err := h.svc.DB.Exec(
				`INSERT INTO books (id, title, status) VALUES (1, 'Busy Book', 'processing')`,
			); err != nil {
				t.Fatalf("seed book: %v", err)
			}
			if _, err := h.svc.DB.Exec(
				`INSERT INTO processing_jobs (id, book_id, status) VALUES ('job-1', 1, ?)`, status,
			); err != nil {
				t.Fatalf("seed job: %v", err)
			}

			ctx := chi.NewRouteContext()
			ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
			ctx.URLParams.Values = append(ctx.URLParams.Values, "1")
			req := httptest.NewRequest("DELETE", "/api/books/1", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
			w := httptest.NewRecorder()
			h.DeleteBook(w, req)
			if w.Code != http.StatusConflict {
				t.Fatalf("delete status = %d; want 409 — body: %s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); !strings.Contains(body, "cancel") {
				t.Errorf("refusal body = %q; want the cancel remedy", body)
			}

			var books int
			if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = 1").Scan(&books); err != nil {
				t.Fatalf("count books: %v", err)
			}
			if books != 1 {
				t.Errorf("books = %d; want the book preserved while its job is in flight", books)
			}
		})
	}
}

// TestDeleteBookAllowsTerminalJobs is the guard's complement: a finished,
// failed, or cancelled job no longer blocks deletion.
func TestDeleteBookAllowsTerminalJobs(t *testing.T) {
	for _, status := range []string{"done", "error", jobStatusCancelled} {
		t.Run(status, func(t *testing.T) {
			h := newTestHandlerWithJobs(t)
			if _, err := h.svc.DB.Exec(
				`INSERT INTO books (id, title, status) VALUES (1, 'Free Book', 'done')`,
			); err != nil {
				t.Fatalf("seed book: %v", err)
			}
			if _, err := h.svc.DB.Exec(
				`INSERT INTO processing_jobs (id, book_id, status) VALUES ('job-1', 1, ?)`, status,
			); err != nil {
				t.Fatalf("seed job: %v", err)
			}

			ctx := chi.NewRouteContext()
			ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
			ctx.URLParams.Values = append(ctx.URLParams.Values, "1")
			req := httptest.NewRequest("DELETE", "/api/books/1", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
			w := httptest.NewRecorder()
			h.DeleteBook(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("delete status = %d; want 204 — body: %s", w.Code, w.Body.String())
			}
			// A 204 alone does not prove the delete ran: the handler also
			// answers 204 after a zero-row DELETE, so assert the row is gone
			// or a predicate that blocks terminal jobs would pass this test.
			var books int
			if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = 1").Scan(&books); err != nil {
				t.Fatalf("count books: %v", err)
			}
			if books != 0 {
				t.Errorf("books = %d; want 0 (a terminal job must not block the delete)", books)
			}
		})
	}
}

// TestDeleteBookFailsClosedWhenJobStateUnreadable pins the guard's
// fail-closed contract: a table-read failure (here: the jobs table is
// absent) must answer 500, never a 204, and the book row must survive —
// a book whose job state could not be established is never deleted.
func TestDeleteBookFailsClosedWhenJobStateUnreadable(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	if _, err := h.svc.DB.Exec(
		`INSERT INTO books (id, title, status) VALUES (1, 'Unreadable State', 'pending')`,
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	// Dropping the table makes ONLY the job-state read fail deterministically
	// ("no such table: processing_jobs"); the books table stays readable for
	// the post-call assertion.
	if _, err := h.svc.DB.Exec("DROP TABLE processing_jobs"); err != nil {
		t.Fatalf("drop processing_jobs: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, "1")
	req := httptest.NewRequest("DELETE", "/api/books/1", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	w := httptest.NewRecorder()
	h.DeleteBook(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d; want 500 — body: %s", w.Code, w.Body.String())
	}
	var books int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = 1").Scan(&books); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if books != 1 {
		t.Errorf("books = %d; want the book preserved when its job state is unreadable", books)
	}
}

// TestDeleteBookNeverSeededIDIsNoOp pins the guarded DELETE's zero-rows
// fallthrough: an id that was never in the books table makes the guarded
// statement match 0 rows, the re-count is 0, the exists check confirms 0,
// and the handler answers 204 (idempotent) — not 409/500. A cover file for
// that id is cleaned in the same pass: a rowless id with a stray cover is
// an orphan by definition, so the idempotent delete removes it too.
func TestDeleteBookNeverSeededIDIsNoOp(t *testing.T) {
	coversDir := t.TempDir()
	coverID := int64(99)
	h := &Handler{svc: &Services{DB: newTestDB(t), CoversDir: coversDir}}
	coverPath := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", coverID))
	if err := os.WriteFile(coverPath, []byte("art"), 0o644); err != nil {
		t.Fatalf("seed cover: %v", err)
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, fmt.Sprint(coverID))
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/books/%d", coverID), nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	w := httptest.NewRecorder()
	h.DeleteBook(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204 — body: %s", w.Code, w.Body.String())
	}
	var books int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = ?", coverID).Scan(&books); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if books != 0 {
		t.Errorf("books = %d; want 0 (the fallthrough must not create a row)", books)
	}
	if _, err := os.Stat(coverPath); !os.IsNotExist(err) {
		t.Errorf("stray cover for a rowless id must be cleaned by the idempotent delete: %v", err)
	}
}

// TestCreateBookSurvivesMetadataInsertFailure pins the log-only contract
// of persistSourceMetadata: when the book_source_metadata INSERT fails
// (here: the table is absent) intake must still return 201 and the book
// row must exist.
func TestCreateBookSurvivesMetadataInsertFailure(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	db := newTestDB(t)
	if _, err := db.Exec("DROP TABLE book_source_metadata"); err != nil {
		t.Fatalf("drop metadata table: %v", err)
	}

	h := &Handler{svc: &Services{DB: db}}
	book := postBook(t, h, srcDir)

	var status string
	if err := h.svc.DB.QueryRow("SELECT status FROM books WHERE id = ?", book.ID).Scan(&status); err != nil {
		t.Fatalf("query created book: %v", err)
	}
	if status != "pending" {
		t.Errorf("book status = %q; want pending", status)
	}
}

// TestUpdateBookAuthorsOnlyKeepsNarrators pins the F11 contract: a request
// that supplies ONLY authors replaces the author rows and leaves the
// narrator rows untouched — the old delete-everything-then-insert shape
// wiped narrators on any authors-only metadata update.
func TestUpdateBookAuthorsOnlyKeepsNarrators(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'My Book', 'matched')`)
	h.svc.DB.Exec(`INSERT INTO people (book_id, name, role) VALUES
		(1, 'Jane Author', 'author'),
		(1, 'Old Narrator', 'narrator')`)

	putBook(t, h, 1, UpdateBookRequest{
		Authors: []PersonUpdate{{Name: "New Author"}},
	})

	var authorCount, narratorCount int
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE book_id = 1 AND role = 'author'`).Scan(&authorCount); err != nil {
		t.Fatalf("count authors: %v", err)
	}
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE book_id = 1 AND role = 'narrator'`).Scan(&narratorCount); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if authorCount != 1 {
		t.Errorf("author rows = %d; want 1 (the replacement)", authorCount)
	}
	if narratorCount != 1 {
		t.Errorf("narrator rows = %d; want 1 (must survive an authors-only update)", narratorCount)
	}

	var authorName string
	if err := h.svc.DB.QueryRow(`SELECT name FROM people WHERE book_id = 1 AND role = 'author'`).Scan(&authorName); err != nil {
		t.Fatalf("read author name: %v", err)
	}
	if authorName != "New Author" {
		t.Errorf("author name = %q; want New Author", authorName)
	}
}

// TestUpdateBookEmptyAuthorsListClearsAuthors pins the F11 distinction
// between "role absent" (nil — leave untouched) and "role present but
// empty" (empty slice — replace with nothing).
func TestUpdateBookEmptyAuthorsListClearsAuthors(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'My Book', 'matched')`)
	h.svc.DB.Exec(`INSERT INTO people (book_id, name, role) VALUES
		(1, 'Jane Author', 'author'),
		(1, 'Sam Narrator', 'narrator')`)

	putBook(t, h, 1, UpdateBookRequest{
		Authors: []PersonUpdate{},
	})

	var authorCount, narratorCount int
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE book_id = 1 AND role = 'author'`).Scan(&authorCount); err != nil {
		t.Fatalf("count authors: %v", err)
	}
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE book_id = 1 AND role = 'narrator'`).Scan(&narratorCount); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if authorCount != 0 {
		t.Errorf("author rows = %d; want 0 (empty list clears)", authorCount)
	}
	if narratorCount != 1 {
		t.Errorf("narrator rows = %d; want 1 (must survive)", narratorCount)
	}
}

// TestUpdateBookPeopleOnlySucceeds pins the F20 contract: a request that
// carries ONLY people fields is a valid update (no 400 "no fields to
// update") and writes no books-column change.
func TestUpdateBookPeopleOnlySucceeds(t *testing.T) {
	h := newTestHandlerWithJobs(t)
	h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (1, 'My Book', 'matched')`)
	h.svc.DB.Exec(`INSERT INTO people (book_id, name, role) VALUES (1, 'Old Author', 'author')`)

	putBook(t, h, 1, UpdateBookRequest{
		Authors: []PersonUpdate{{Name: "Solo Author"}},
	})

	var authorCount int
	if err := h.svc.DB.QueryRow(`SELECT COUNT(*) FROM people WHERE book_id = 1 AND role = 'author'`).Scan(&authorCount); err != nil {
		t.Fatalf("count authors: %v", err)
	}
	if authorCount != 1 {
		t.Errorf("author rows = %d; want 1", authorCount)
	}
}

// TestListBooksAllIDsReportsTotalAndTruncation pins the F21 contract: the
// all_ids response carries the FULL match count (total) and a truncated
// flag, so a client never mistakes a capped ID list for the whole matching
// set. The over-cap case is TestListBooksAllIDSCapTruncates.
func TestListBooksAllIDsReportsTotalAndTruncation(t *testing.T) {
	h := newTestHandler(t)
	for i := range 5 {
		h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (?, 'Book ' || ?, 'pending')`, i+1, i+1)
	}
	for i := range 2 {
		h.svc.DB.Exec(`INSERT INTO books (id, title, status) VALUES (?, 'Other ' || ?, 'done')`, 100+i, i+1)
	}

	// Under the cap: total = matches, truncated false.
	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?all_ids=1&status=pending", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d — body: %s", w.Code, w.Body.String())
	}
	var under struct {
		Total     int     `json:"total"`
		Truncated bool    `json:"truncated"`
		IDs       []int64 `json:"ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &under); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if under.Total != 5 || len(under.IDs) != 5 || under.Truncated {
		t.Errorf("uncapped: total=%d ids=%d truncated=%v; want 5/5/false", under.Total, len(under.IDs), under.Truncated)
	}
}

// TestListBooksAllIDSCapTruncates seeds more books than maxBookIDs and pins
// that the response cuts at the cap while total/truncated report the rest.
func TestListBooksAllIDSCapTruncates(t *testing.T) {
	h := newTestHandler(t)
	const extra = 10
	// Seed one row per id, 1..maxBookIDs+extra, via a recursive CTE
	// (modernc sqlite has no generate_series table function).
	stmt := `INSERT INTO books (id, title, status)
		WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < ` + fmt.Sprint(maxBookIDs+extra) + `)
		SELECT n, 'Book ' || n, 'pending' FROM seq`
	if _, err := h.svc.DB.Exec(stmt); err != nil {
		t.Fatalf("seed books: %v", err)
	}

	w := httptest.NewRecorder()
	h.ListBooks(w, httptest.NewRequest("GET", "/api/books?all_ids=1&status=pending", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d — body: %s", w.Code, w.Body.String())
	}
	var res struct {
		Total     int     `json:"total"`
		Truncated bool    `json:"truncated"`
		IDs       []int64 `json:"ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantTotal := maxBookIDs + extra
	if res.Total != wantTotal {
		t.Errorf("total = %d; want %d (the full match count)", res.Total, wantTotal)
	}
	if !res.Truncated {
		t.Error("truncated = false; want true (payload cut at the cap)")
	}
	if len(res.IDs) != maxBookIDs {
		t.Errorf("ids = %d; want the cap %d", len(res.IDs), maxBookIDs)
	}
}

// TestStoredIndexMetadataFreshness pins the stored-snapshot freshness gate:
// an in-place source rewrite (same path, changed size or mtime) must NOT
// reuse the stored index snapshot — the live probe runs instead.
func TestStoredIndexMetadataFreshness(t *testing.T) {
	h := newTestHandler(t)
	if _, err := h.svc.DB.Exec(`
		CREATE TABLE source_index (
			path TEXT PRIMARY KEY,
			name TEXT, type TEXT, size INTEGER, modtime TEXT, payload TEXT,
			indexed_at TEXT, seen_at TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mp3")
	body := []byte("0123456789")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(src)
	modtime := info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")
	complete := `{"schema":2,"source_kind":"file","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"a.mp3","duration_seconds":5}],"fields":{}}`

	t.Run("fresh source reuses snapshot", func(t *testing.T) {
		if _, err := h.svc.DB.Exec(
			`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
			 VALUES (?, 'a.mp3', 'file', ?, ?, ?, datetime('now'), datetime('now'))`,
			canonicalRoot(src), info.Size(), modtime, complete); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(src); snap == nil {
			t.Fatal("fresh stored snapshot not reused")
		}
	})

	t.Run("size changed falls to live probe", func(t *testing.T) {
		if _, err := h.svc.DB.Exec(`UPDATE source_index SET size = ? WHERE path = ?`,
			999, canonicalRoot(src)); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(src); snap != nil {
			t.Fatal("stale (size-mismatched) snapshot was reused")
		}
	})

	t.Run("mtime changed falls to live probe", func(t *testing.T) {
		if _, err := h.svc.DB.Exec(`UPDATE source_index SET size = ? WHERE path = ?`,
			info.Size(), canonicalRoot(src)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.DB.Exec(`UPDATE source_index SET modtime = ? WHERE path = ?`,
			"2020-01-01T00:00:00.000000000Z", canonicalRoot(src)); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(src); snap != nil {
			t.Fatal("stale (mtime-mismatched) snapshot was reused")
		}
	})

	// Folder half: the rows are seeded through the REAL index worker's
	// write path (walk → upsertAll), so the stored signals are whatever
	// the walk recorded for the directory and its child — not hand-typed
	// constants. The gate must accept the unchanged folder, reject an
	// in-place child rewrite (which advances neither the dir's mtime nor
	// its size), and reject an entry-add mtime bump.
	bookDir := filepath.Join(dir, "book")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// An audio child in the folder: its stored row is the signal the gate
	// must consult for in-place child rewrites.
	bookChild := filepath.Join(bookDir, "child.mp3")
	if err := os.WriteFile(bookChild, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, _ := newIndexTestService(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("scan = %s (detail %q), want done", st.Status, st.Detail)
	}
	// Copy the worker-written directory and child rows into the handler's
	// DB so the stored signals match the walk's records.
	var dirRowName, dirRowType, dirRowModtime, childRowName, childRowType, childRowModtime string
	var dirRowSize, childRowSize int64
	if err := svc.db.QueryRow(`SELECT name, type, size, modtime FROM source_index WHERE path = ?`,
		canonicalRoot(bookDir)).Scan(&dirRowName, &dirRowType, &dirRowSize, &dirRowModtime); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT name, type, size, modtime FROM source_index WHERE path = ?`,
		canonicalRoot(bookChild)).Scan(&childRowName, &childRowType, &childRowSize, &childRowModtime); err != nil {
		t.Fatal(err)
	}
	// A complete folder snapshot referencing the child — the payload
	// sourceMetadataComplete accepts for this fixture.
	completeFolder := `{"schema":2,"source_kind":"folder","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"child.mp3","duration_seconds":5}],"fields":{"asin":{"values":["B0FOLDER01"],"source":"embedded","status":"present"}}}`
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(bookDir), dirRowName, dirRowType, dirRowSize, dirRowModtime, completeFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(bookChild), childRowName, childRowType, childRowSize, childRowModtime, completeFolder); err != nil {
		t.Fatal(err)
	}

	t.Run("unchanged folder reuses snapshot", func(t *testing.T) {
		if snap := h.storedIndexMetadata(bookDir); snap == nil {
			t.Fatal("fresh folder snapshot not reused (dir row signals do not match the live stat)")
		}
	})

	t.Run("in-place child rewrite falls to live probe", func(t *testing.T) {
		// Rewriting the child in place (same name, same entry set)
		// advances neither the directory's mtime nor its size — the hole
		// the child-signal gate plugs.
		if err := os.WriteFile(bookChild, []byte("rewritten-content"), 0o644); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(bookDir); snap != nil {
			t.Fatal("stale (child-rewritten) folder snapshot was reused")
		}
	})

	t.Run("bumped-mtime folder falls to live probe", func(t *testing.T) {
		// Adding an entry moves the directory's own mtime — the signal the
		// index relies on for folders.
		if err := os.WriteFile(filepath.Join(bookDir, "new.mp3"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(bookDir); snap != nil {
			t.Fatal("stale (mtime-bumped) folder snapshot was reused")
		}
	})

	// Depth-limit shape: the stored state is hand-crafted (not
	// worker-seeded) to mirror a walk truncated by the depth or entry cap:
	// the complete folder payload names two files, but the index holds a
	// row for only one — the omitted entry was never walked, so a rewrite
	// of it advances neither the dir's own mtime nor any stored child row.
	// The gate must reject on the payload's completeness, not find a
	// signal to compare against.
	omittedDir := filepath.Join(dir, "omitted")
	if err := os.MkdirAll(omittedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keptChild := filepath.Join(omittedDir, "kept.mp3")
	omittedChild := filepath.Join(omittedDir, "omitted.mp3")
	for _, p := range []string{keptChild, omittedChild} {
		if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirInfo, _ := os.Stat(omittedDir)
	keptInfo, _ := os.Stat(keptChild)
	omittedFolder := `{"schema":2,"source_kind":"folder","files_total":2,"files_probed":2,"degraded":false,"duration_seconds":10,"files":[{"file":"kept.mp3","duration_seconds":5},{"file":"omitted.mp3","duration_seconds":5}],"fields":{"asin":{"values":["B0FOLDER02"],"source":"embedded","status":"present"}}}`
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, 'omitted', 'dir', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(omittedDir), dirInfo.Size(), dirInfo.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"), omittedFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, 'kept.mp3', 'file', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(keptChild), keptInfo.Size(), keptInfo.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"), omittedFolder); err != nil {
		t.Fatal(err)
	}
	// No row for omitted.mp3: the cap-truncated walk never stored it.

	t.Run("payload file without a stored row falls to live probe", func(t *testing.T) {
		// Rewrite the omitted file in place: same name, same entry set, so
		// neither the dir's mtime nor any stored child row moves.
		if err := os.WriteFile(omittedChild, []byte("rewritten-content"), 0o644); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(omittedDir); snap != nil {
			t.Fatal("stale (omitted-file-rewritten) folder snapshot was reused")
		}
	})

	// Duplicate-basename shape: the omitted direct child's basename is
	// shadowed by a NESTED file that HAS a stored row (the differing
	// basename case above cannot be fooled this way). The gate matches the
	// EXACT indexed child path (dirPath + basename), so the nested row must
	// not satisfy the omitted child — the folder is not fresh.
	dupDir := filepath.Join(dir, "dup")
	dupNested := filepath.Join(dupDir, "nested")
	if err := os.MkdirAll(dupNested, 0o755); err != nil {
		t.Fatal(err)
	}
	dupChild := filepath.Join(dupDir, "dup.mp3")
	dupNestedFile := filepath.Join(dupNested, "dup.mp3")
	for _, p := range []string{dupChild, dupNestedFile} {
		if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dupInfo, _ := os.Stat(dupDir)
	nestedInfo, _ := os.Stat(dupNestedFile)
	dupFolder := `{"schema":2,"source_kind":"folder","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"dup.mp3","duration_seconds":5}],"fields":{"asin":{"values":["B0FOLDER03"],"source":"embedded","status":"present"}}}`
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, 'dup', 'dir', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(dupDir), dupInfo.Size(), dupInfo.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"), dupFolder); err != nil {
		t.Fatal(err)
	}
	// A stored row for the NESTED same-basename file (fresh): a basename
	// match would make the omitted direct child look stored.
	if _, err := h.svc.DB.Exec(
		`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		 VALUES (?, 'dup.mp3', 'file', ?, ?, ?, datetime('now'), datetime('now'))`,
		canonicalRoot(dupNestedFile), nestedInfo.Size(), nestedInfo.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"), dupFolder); err != nil {
		t.Fatal(err)
	}
	// No row for the direct dup.mp3: the entry-cap-truncated walk omitted it.

	t.Run("omitted child shadowed by a nested same-basename row falls to live probe", func(t *testing.T) {
		// Rewrite the omitted direct child in place: same name, same size
		// (same content), so the dir's mtime and every stored child row
		// (including the nested shadow) are untouched.
		if err := os.WriteFile(dupChild, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		if snap := h.storedIndexMetadata(dupDir); snap != nil {
			t.Fatal("stale (omitted child shadowed by a nested same-basename row) folder snapshot was reused")
		}
	})
}

// TestCreateBooksIntakeIndexGate pins the INTAKE-level Enabled() gate
// (the freshness gate above is storedIndexMetadata-only, reachable with
// the index off too): with a FRESH, complete stored row and a live probe
// available, a disabled index must run the probe (persisted row reflects
// the probe, not the stored snapshot) and an enabled index must reuse the
// stored snapshot (the probe never runs).
func TestCreateBooksIntakeIndexGate(t *testing.T) {
	// The stored snapshot carries a distinctive ASIN so a reuse is
	// observable; the live probe (stubbed ffprobe) carries a different one.
	const storedASIN = "B0STORED00"
	const probedASIN = "B0PROBED00"
	complete := `{"schema":2,"source_kind":"file","files_total":1,"files_probed":1,"degraded":false,"duration_seconds":5,"files":[{"file":"1.mp3","duration_seconds":5}],"fields":{"asin":{"values":["` + storedASIN + `"],"source":"embedded","status":"present"}}}`

	setup := func(t *testing.T, enabled bool) (*Handler, string, string) {
		t.Helper()
		srcDir := t.TempDir()
		src := filepath.Join(srcDir, "1.mp3")
		if err := os.WriteFile(src, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		modtime := info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")

		db := newTestDB(t)
		if _, err := db.Exec(`
			CREATE TABLE source_index (
				path TEXT PRIMARY KEY, name TEXT, type TEXT, size INTEGER, modtime TEXT, payload TEXT,
				indexed_at TEXT, seen_at TEXT
			)`); err != nil {
			t.Fatal(err)
		}
		// Fresh, complete row: size and modtime match the live file.
		if _, err := db.Exec(`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
			VALUES (?, '1.mp3', 'file', ?, ?, ?, datetime('now'), datetime('now'))`,
			canonicalRoot(src), info.Size(), modtime, complete); err != nil {
			t.Fatal(err)
		}

		// A stub ffprobe whose sidecar carries the probe's ASIN: if the
		// probe runs, the persisted snapshot shows B0PROBED00; a reuse of
		// the stored snapshot shows B0STORED00.
		ffprobe, respDir := stubFFprobeForMetadata(t)
		writeProbeSidecar(t, respDir, "1.mp3",
			`{"format": {"duration": "5.0", "tags": {"audible_asin": "`+probedASIN+`"}}, "streams": []}`)

		h := &Handler{svc: &Services{DB: db, FFprobe: ffprobe}}
		if enabled {
			cm := config.NewConfigManager()
			cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
			cm.Config().Directories.InputDir = srcDir
			cm.Config().Directories.IndexingEnabled = true
			cm.Store(cm.Config())
			h.svc.IndexSvc = NewIndexService(db, cm, "")
		}
		return h, src, probedASIN
	}

	asinOf := func(t *testing.T, db *sql.DB, bookID int64) string {
		t.Helper()
		snap := metadataPayloadFor(t, db, bookID)
		fields, ok := snap["fields"].(map[string]any)
		if !ok {
			t.Fatalf("payload fields = %T, want object", snap["fields"])
		}
		asin, ok := fields["asin"].(map[string]any)
		if !ok {
			t.Fatalf("payload has no asin field: %v", fields)
		}
		values, ok := asin["values"].([]any)
		if !ok || len(values) != 1 {
			t.Fatalf("asin values = %v, want a single value", asin["values"])
		}
		return values[0].(string)
	}

	t.Run("disabled index runs the live probe", func(t *testing.T) {
		h, src, probedASIN := setup(t, false)
		book := postBook(t, h, src)
		if got := asinOf(t, h.svc.DB, book.ID); got != probedASIN {
			t.Fatalf("persisted asin = %s, want %s (disabled index must run the live probe, not reuse the stored row)", got, probedASIN)
		}
	})

	t.Run("enabled index reuses the stored snapshot", func(t *testing.T) {
		h, src, _ := setup(t, true)
		book := postBook(t, h, src)
		if got := asinOf(t, h.svc.DB, book.ID); got != "B0STORED00" {
			t.Fatalf("persisted asin = %s, want B0STORED00 (enabled index with a fresh complete row must reuse the stored snapshot, no probe)", got)
		}
	})
}
