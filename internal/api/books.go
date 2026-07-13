package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bragibooks/bragibooks/internal/models"
)

// ListBooksResponse is the JSON shape returned by GET /api/books.
type ListBooksResponse struct {
	Books []models.BookWithPeople `json:"books"`
	Total int                    `json:"total"`
	Page  int                    `json:"page"`
	Limit int                    `json:"limit"`
}

// PersonUpdate is a single author or narrator supplied by the client.
type PersonUpdate struct {
	Name                string  `json:"name"`
	AudiobookdbPersonID *string `json:"audiobookdb_person_id,omitempty"`
}

// CreateBookEntry is a single source path to turn into a pending book.
type CreateBookEntry struct {
	SrcPath string `json:"src_path"`
	Title   string `json:"title,omitempty"`
}

// CreateBooksRequest is the JSON body accepted by POST /api/books.
type CreateBooksRequest struct {
	Books []CreateBookEntry `json:"books"`
}

// CreateBooksResponse is returned by POST /api/books.
type CreateBooksResponse struct {
	Books []models.BookWithPeople `json:"books"`
}

// UpdateBookRequest is the JSON body accepted by PUT /api/books/:id.
type UpdateBookRequest struct {
	Title                *string         `json:"title"`
	ASIN                 *string         `json:"asin"`
	AudiobookdbBookID    *string         `json:"audiobookdb_book_id"`
	AudiobookdbReleaseID *string         `json:"audiobookdb_release_id"`
	Description          *string         `json:"description"`
	ReleaseDate          *string         `json:"release_date"`
	Series               *string         `json:"series"`
	Publisher            *string         `json:"publisher"`
	Language             *string         `json:"language"`
	RuntimeLengthMinutes *int            `json:"runtime_length_minutes"`
	FormatType           *string         `json:"format_type"`
	SrcPath              *string         `json:"src_path"`
	DestPath             *string         `json:"dest_path"`
	Status               *string         `json:"status"`
	StatusMessage        *string         `json:"status_message"`
	CoverImageURL        *string         `json:"cover_image_url"`
	Authors              []PersonUpdate  `json:"authors,omitempty"`
	Narrators            []PersonUpdate  `json:"narrators,omitempty"`
}

// ListBooks handles GET /api/books with optional status filter and pagination.
func (h *Handler) ListBooks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	page := parseIntQueryParam(r, "page", 0)
	limit := parseIntQueryParam(r, "limit", 20)

	if limit < 1 || limit > 100 {
		limit = 20
	}
	if page < 0 {
		page = 0
	}

	offset := page * limit

	var books []models.Book
	var totalCount int

	switch status {
	case "":
		// List all
		rows, err := h.svc.DB.Query(
			"SELECT id, title, asin, audiobookdb_book_id, audiobookdb_release_id, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, status_message, cover_image_url, created_at, updated_at FROM books ORDER BY created_at DESC LIMIT ? OFFSET ?",
			limit, offset,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("query books: %v", err))
			return
		}
		defer rows.Close()
		books, err = scanBooks(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan books: %v", err))
			return
		}
		countRow := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books")
		if err := countRow.Scan(&totalCount); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("count books: %v", err))
			return
		}
	case "done", "processing", "error", "pending", "matched":
		rows, err := h.svc.DB.Query(
			"SELECT id, title, asin, audiobookdb_book_id, audiobookdb_release_id, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, status_message, cover_image_url, created_at, updated_at FROM books WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?",
			status, limit, offset,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("query books: %v", err))
			return
		}
		defer rows.Close()
		books, err = scanBooks(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan books: %v", err))
			return
		}
		countRow := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE status = ?", status)
		if err := countRow.Scan(&totalCount); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("count books: %v", err))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status filter: %s", status))
		return
	}

	booksWithPeople, err := h.enrichBooks(books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("enrich books: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, ListBooksResponse{
		Books: booksWithPeople,
		Total: totalCount,
		Page:  page,
		Limit: limit,
	})
}

// GetBook handles GET /api/books/:id, returning the book with authors and narrators.
func (h *Handler) GetBook(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	bookID := parseInt64Param(id)
	if bookID == -1 {
		writeError(w, http.StatusBadRequest, "invalid book ID")
		return
	}

	book, err := h.getBook(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get book: %v", err))
		return
	}
	if book == nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}

	people, err := h.getPeopleByBookID(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get people: %v", err))
		return
	}

	bookWithPeople := models.BookWithPeople{
		Book:      *book,
		Authors:   filterPeople(people, "author"),
		Narrators: filterPeople(people, "narrator"),
	}

	writeJSON(w, http.StatusOK, bookWithPeople)
}

// UpdateBook handles PUT /api/books/:id, updating the provided fields.
func (h *Handler) UpdateBook(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	bookID := parseInt64Param(id)
	if bookID == -1 {
		writeError(w, http.StatusBadRequest, "invalid book ID")
		return
	}

	var req UpdateBookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	setters := []string{}
	args := []interface{}{}

	if req.Title != nil {
		setters = append(setters, "title = ?")
		args = append(args, *req.Title)
	}
	if req.ASIN != nil {
		setters = append(setters, "asin = ?")
		args = append(args, *req.ASIN)
	}
	if req.AudiobookdbBookID != nil {
		setters = append(setters, "audiobookdb_book_id = ?")
		args = append(args, *req.AudiobookdbBookID)
	}
	if req.AudiobookdbReleaseID != nil {
		setters = append(setters, "audiobookdb_release_id = ?")
		args = append(args, *req.AudiobookdbReleaseID)
	}
	if req.Description != nil {
		setters = append(setters, "description = ?")
		args = append(args, *req.Description)
	}
	if req.ReleaseDate != nil {
		setters = append(setters, "release_date = ?")
		args = append(args, *req.ReleaseDate)
	}
	if req.Series != nil {
		setters = append(setters, "series = ?")
		args = append(args, *req.Series)
	}
	if req.Publisher != nil {
		setters = append(setters, "publisher = ?")
		args = append(args, *req.Publisher)
	}
	if req.Language != nil {
		setters = append(setters, "language = ?")
		args = append(args, *req.Language)
	}
	if req.RuntimeLengthMinutes != nil {
		setters = append(setters, "runtime_length_minutes = ?")
		args = append(args, *req.RuntimeLengthMinutes)
	}
	if req.FormatType != nil {
		setters = append(setters, "format_type = ?")
		args = append(args, *req.FormatType)
	}
	if req.SrcPath != nil {
		setters = append(setters, "src_path = ?")
		args = append(args, *req.SrcPath)
	}
	if req.DestPath != nil {
		setters = append(setters, "dest_path = ?")
		args = append(args, *req.DestPath)
	}
	if req.Status != nil {
		setters = append(setters, "status = ?")
		args = append(args, *req.Status)
	}
	if req.StatusMessage != nil {
		setters = append(setters, "status_message = ?")
		args = append(args, *req.StatusMessage)
	}
	if req.CoverImageURL != nil {
		setters = append(setters, "cover_image_url = ?")
		args = append(args, *req.CoverImageURL)
	}

	if len(setters) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	args = append(args, bookID)
	settersStr := strings.Join(setters, ", ")
	query := fmt.Sprintf("UPDATE books SET %s WHERE id = ?", settersStr)

	_, err := h.svc.DB.Exec(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("update book: %v", err))
		return
	}

	if req.Authors != nil || req.Narrators != nil {
		if err := replacePeople(h.svc.DB, bookID, req.Authors, req.Narrators); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("replace people: %v", err))
			return
		}
	}

	book, err := h.getBook(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get updated book: %v", err))
		return
	}

	people, err := h.getPeopleByBookID(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get people: %v", err))
		return
	}

	bookWithPeople := models.BookWithPeople{
		Book:      *book,
		Authors:   filterPeople(people, "author"),
		Narrators: filterPeople(people, "narrator"),
	}

	writeJSON(w, http.StatusOK, bookWithPeople)
}

// CreateBooks handles POST /api/books, creating a batch of pending books from source paths.
func (h *Handler) CreateBooks(w http.ResponseWriter, r *http.Request) {
	var req CreateBooksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Books) == 0 {
		writeError(w, http.StatusBadRequest, "at least one book is required")
		return
	}

	created := make([]models.Book, 0, len(req.Books))
	for _, entry := range req.Books {
		title := entry.Title
		if title == "" {
			title = deriveTitle(entry.SrcPath)
		}

		res, err := h.svc.DB.Exec(
			`INSERT INTO books (title, src_path, release_date, status, created_at, updated_at)
			 VALUES (?, ?, '', 'pending', datetime('now'), datetime('now'))`,
			title, entry.SrcPath,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("create book: %v", err))
			return
		}
		id, err := res.LastInsertId()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("last insert id: %v", err))
			return
		}
		book, err := h.getBook(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("get created book: %v", err))
			return
		}
		created = append(created, *book)
	}

	enriched, err := h.enrichBooks(created)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("enrich books: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, CreateBooksResponse{Books: enriched})
}

func deriveTitle(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, ".", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled"
	}
	return strings.Title(name)
}

func replacePeople(db *sql.DB, bookID int64, authors, narrators []PersonUpdate) error {
	if _, err := db.Exec("DELETE FROM people WHERE book_id = ?", bookID); err != nil {
		return fmt.Errorf("delete people: %w", err)
	}

	insert, err := db.Prepare(
		"INSERT INTO people (book_id, name, role, audiobookdb_person_id, created_at) VALUES (?, ?, ?, ?, datetime('now'))",
	)
	if err != nil {
		return fmt.Errorf("prepare people insert: %w", err)
	}
	defer insert.Close()

	add := func(role string, list []PersonUpdate) error {
		for _, p := range list {
			if strings.TrimSpace(p.Name) == "" {
				continue
			}
			var adbID sql.NullString
			if p.AudiobookdbPersonID != nil {
				adbID = sql.NullString{String: *p.AudiobookdbPersonID, Valid: true}
			}
			if _, err := insert.Exec(bookID, p.Name, role, adbID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add("author", authors); err != nil {
		return fmt.Errorf("insert authors: %w", err)
	}
	if err := add("narrator", narrators); err != nil {
		return fmt.Errorf("insert narrators: %w", err)
	}
	return nil
}

// DeleteBook handles DELETE /api/books/:id, deleting the book and cascading people.
func (h *Handler) DeleteBook(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	bookID := parseInt64Param(id)
	if bookID == -1 {
		writeError(w, http.StatusBadRequest, "invalid book ID")
		return
	}

	// Verify the book exists before deleting
	_, err := h.getBook(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("get book: %v", err))
		return
	}

	_, err = h.svc.DB.Exec("DELETE FROM books WHERE id = ?", bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete book: %v", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// getBook returns a single book by ID, or nil if not found.
func (h *Handler) getBook(id int64) (*models.Book, error) {
	rows, err := h.svc.DB.Query(
		"SELECT id, title, asin, audiobookdb_book_id, audiobookdb_release_id, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, status_message, cover_image_url, created_at, updated_at FROM books WHERE id = ?",
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books, err := scanBooks(rows)
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, nil
	}
	return &books[0], nil
}

// getPeopleByBookID returns all people associated with a book.
func (h *Handler) getPeopleByBookID(bookID int64) ([]models.Person, error) {
	rows, err := h.svc.DB.Query(
		"SELECT id, book_id, name, role, audiobookdb_person_id, created_at FROM people WHERE book_id = ?",
		bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var people []models.Person
	for rows.Next() {
		var p models.Person
		var audiobookdbPersonID sql.NullString
		err := rows.Scan(&p.ID, &p.BookID, &p.Name, &p.Role, &audiobookdbPersonID, &p.CreatedAt)
		if err != nil {
			return nil, err
		}
		p.AudiobookdbPersonID = audiobookdbPersonID
		people = append(people, p)
	}
	return people, rows.Err()
}

// scanBooks reads rows from a book query and returns a slice of books.
func scanBooks(rows *sql.Rows) ([]models.Book, error) {
	var books []models.Book
	for rows.Next() {
		var b models.Book
		var asin, audiobookdbBookID, audiobookdbReleaseID sql.NullString
		err := rows.Scan(
			&b.ID, &b.Title, &asin, &audiobookdbBookID, &audiobookdbReleaseID,
			&b.Description, &b.ReleaseDate, &b.Series, &b.Publisher, &b.Language,
			&b.RuntimeLengthMinutes, &b.FormatType, &b.SrcPath, &b.DestPath,
			&b.Status, &b.StatusMessage, &b.CoverImageURL, &b.CreatedAt, &b.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		b.ASIN = asin
		b.AudiobookdbBookID = audiobookdbBookID
		b.AudiobookdbReleaseID = audiobookdbReleaseID
		books = append(books, b)
	}
	return books, rows.Err()
}

// enrichBooks attaches authors and narrators to each book in the list.
func (h *Handler) enrichBooks(books []models.Book) ([]models.BookWithPeople, error) {
	if len(books) == 0 {
		return []models.BookWithPeople{}, nil
	}

	// Build IN clause with literal integer IDs (safe: IDs are int64)
	idParts := make([]string, len(books))
	for i, b := range books {
		idParts[i] = fmt.Sprintf("%d", b.ID)
	}
	query := "SELECT id, book_id, name, role, audiobookdb_person_id, created_at FROM people WHERE book_id IN (" + strings.Join(idParts, ",") + ")"

	stmt, err := h.svc.DB.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group people by book ID
	peopleByBook := make(map[int64][]models.Person)
	for rows.Next() {
		var p models.Person
		var audiobookdbPersonID sql.NullString
		err := rows.Scan(&p.ID, &p.BookID, &p.Name, &p.Role, &audiobookdbPersonID, &p.CreatedAt)
		if err != nil {
			return nil, err
		}
		p.AudiobookdbPersonID = audiobookdbPersonID
		peopleByBook[p.BookID] = append(peopleByBook[p.BookID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Build enriched books
	result := make([]models.BookWithPeople, 0, len(books))
	for _, b := range books {
		people := peopleByBook[b.ID]
		result = append(result, models.BookWithPeople{
			Book:      b,
			Authors:   filterPeople(people, "author"),
			Narrators: filterPeople(people, "narrator"),
		})
	}
	return result, nil
}

// filterPeople returns the subset of people matching the given role.
func filterPeople(people []models.Person, role string) []models.Person {
	result := make([]models.Person, 0)
	for _, p := range people {
		if p.Role == role {
			result = append(result, p)
		}
	}
	return result
}

// parseInt64Param converts a string to int64, returning -1 on error.
func parseInt64Param(s string) int64 {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return -1
	}
	return n
}