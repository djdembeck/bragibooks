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
	"testing"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"

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
`

// newTestDB opens an in-memory SQLite database and creates the schema.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTitle(tc.path); got != tc.want {
				t.Errorf("deriveTitle(%q) = %q; want %q", tc.path, got, tc.want)
			}
		})
	}
}
