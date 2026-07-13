package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
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
	return &Handler{svc: Services{DB: newTestDB(t)}}
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
