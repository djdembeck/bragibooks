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
	"time"
	"unicode"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// ListBooksResponse is the JSON shape returned by GET /api/books.
type ListBooksResponse struct {
	Books []models.BookWithPeople `json:"books"`
	Total int                     `json:"total"`
	Page  int                     `json:"page"`
	Limit int                     `json:"limit"`
}

// PersonUpdate is a single author or narrator supplied by the client.
type PersonUpdate struct {
	Name                string  `json:"name"`
	AudiobookdbPersonID *string `json:"audiobookdb_person_id,omitempty"`
}

// CreateBookEntry is a single source path to turn into a pending book.
// Output destinations are NOT per-book: saved output profiles (Settings)
// are selected once per queue request and snapshotted into the jobs.
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
	Title                *string `json:"title"`
	ASIN                 *string `json:"asin"`
	AudiobookdbBookID    *string `json:"audiobookdb_book_id"`
	AudiobookdbReleaseID *string `json:"audiobookdb_release_id"`
	Description          *string `json:"description"`
	ReleaseDate          *string `json:"release_date"`
	Series               *string `json:"series"`
	Publisher            *string `json:"publisher"`
	Language             *string `json:"language"`
	RuntimeLengthMinutes *int    `json:"runtime_length_minutes"`
	FormatType           *string `json:"format_type"`
	SrcPath              *string `json:"src_path"`
	DestPath             *string `json:"dest_path"`
	Status               *string `json:"status"`
	StatusMessage        *string `json:"status_message"`
	CoverImageURL        *string `json:"cover_image_url"`
	// No omitempty: an empty list ([] = "clear this role") must survive
	// JSON round-trips as distinct from an absent field (nil = "leave this
	// role untouched"), which is the F11 replace-vs-keep contract.
	Authors   []PersonUpdate `json:"authors"`
	Narrators []PersonUpdate `json:"narrators"`
}

// booksColumns is the SELECT column list for the books table, shared by the
// list and count helpers so the scan column set stays in one place.
const booksColumns = "id, title, asin, audiobookdb_book_id, audiobookdb_release_id, description, release_date, series, publisher, language, runtime_length_minutes, format_type, src_path, dest_path, status, status_message, cover_image_url, created_at, updated_at"

// validBookStatus reports whether s is an accepted book status filter value.
func validBookStatus(s string) bool {
	switch s {
	case "done", "processing", "error", "pending", "matched":
		return true
	}
	return false
}

// escapeLikeTerm escapes LIKE wildcards in a user-supplied substring so it is
// matched literally rather than as a pattern.
func escapeLikeTerm(term string) string {
	term = strings.ReplaceAll(term, `\`, `\\`)
	term = strings.ReplaceAll(term, `%`, `\%`)
	term = strings.ReplaceAll(term, `_`, `\_`)
	return term
}

// buildBooksFilter assembles the WHERE fragment (without leading WHERE) and
// bound args for the books list/count/ids queries from the optional status
// and text filters. Returns an empty fragment when no filter is present.
func buildBooksFilter(status, q string) (string, []any) {
	var conds []string
	var args []any
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	if q != "" {
		// SQLite LIKE is case-insensitive for ASCII, matching the contract.
		like := "%" + escapeLikeTerm(q) + "%"
		conds = append(conds, "(title LIKE ? ESCAPE '\\' OR series LIKE ? ESCAPE '\\')")
		args = append(args, like, like)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListBooks handles GET /api/books with optional status, text (q) and
// pagination filters. all_ids=1 switches the response to a capped ID list
// bulk selection.
func (h *Handler) ListBooks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	q := r.URL.Query().Get("q")
	page := parseIntQueryParam(r, "page", 0)
	limit := parseIntQueryParam(r, "limit", 20)

	if limit < 1 {
		limit = 20
	}
	// Allow up to 500 per page (deep pages of large libraries); only clamp
	// values above that, so legitimate pagination of big collections works.
	if limit > 500 {
		limit = 500
	}
	if page < 0 {
		page = 0
	}

	if status != "" && !validBookStatus(status) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status filter: %s", status))
		return
	}

	// all_ids mode: return the matching IDs (capped, with total/truncated)
	// for bulk select.
	if parseBoolQueryParam(r, "all_ids") {
		h.listBookIDs(w, status, q)
		return
	}

	offset := page * limit
	where, filterArgs := buildBooksFilter(status, q)

	queryArgs := append(append([]any{}, filterArgs...), limit, offset)
	rows, err := h.svc.DB.Query(
		"SELECT "+booksColumns+" FROM books"+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		queryArgs...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query books: %v", err))
		return
	}
	defer rows.Close()
	books, err := scanBooks(rows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan books: %v", err))
		return
	}

	var totalCount int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books"+where, filterArgs...).Scan(&totalCount); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("count books: %v", err))
		return
	}

	booksWithPeople, err := h.enrichBooks(books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("enrich books: %v", err))
		return
	}

	// The source-ASIN projection is a pending-queue aid only; other
	// statuses and all_ids mode omit it entirely.
	if status == "pending" {
		h.attachSourceProjections(booksWithPeople)
	}

	writeJSON(w, http.StatusOK, ListBooksResponse{
		Books: booksWithPeople,
		Total: totalCount,
		Page:  page,
		Limit: limit,
	})
}

// maxBookIDs caps the all_ids response so a bulk-select payload stays small.
const maxBookIDs = 1000

// listBookIDs writes {"ids":[...],"total":N,"truncated":bool} for the given
// filters, capped at maxBookIDs. total is the FULL match count (uncapped)
// and truncated is true when the cap cut the list — the client must not
// treat a capped response as the whole set (finding: F21).
func (h *Handler) listBookIDs(w http.ResponseWriter, status, q string) {
	where, filterArgs := buildBooksFilter(status, q)
	var total int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books"+where, filterArgs...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("count books: %v", err))
		return
	}

	queryArgs := append(append([]any{}, filterArgs...), maxBookIDs)
	rows, err := h.svc.DB.Query(
		"SELECT id FROM books"+where+" ORDER BY created_at DESC LIMIT ?",
		queryArgs...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query books: %v", err))
		return
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan book ids: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ids":       ids,
		"total":     total,
		"truncated": len(ids) < total,
	})
}

// BooksCountsResponse is the JSON shape returned by GET /api/books/counts.
type BooksCountsResponse struct {
	Counts map[string]int `json:"counts"`
}

// GetBooksCounts handles GET /api/books/counts, returning per-status book
// counts in a single grouped query (the dashboard previously made five
// separate limit=1 calls).
func (h *Handler) GetBooksCounts(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{
		"pending":    0,
		"matched":    0,
		"processing": 0,
		"done":       0,
		"error":      0,
	}
	rows, err := h.svc.DB.Query("SELECT status, COUNT(*) FROM books GROUP BY status")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("query book counts: %v", err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			continue
		}
		if _, ok := counts[s]; ok {
			counts[s] = n
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan book counts: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, BooksCountsResponse{Counts: counts})
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

	// People fields are valid update fields even when they are the ONLY
	// thing in the request: they have no SQL setter, so counting only the
	// scalar setters would 400 a request that updates just authors or
	// narrators (finding: F20).
	if len(setters) == 0 && req.Authors == nil && req.Narrators == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	if len(setters) > 0 {
		args = append(args, bookID)
		settersStr := strings.Join(setters, ", ")
		query := fmt.Sprintf("UPDATE books SET %s WHERE id = ?", settersStr)

		_, err := h.svc.DB.Exec(query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("update book: %v", err))
			return
		}
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
	if book == nil {
		// The UPDATE above matched no rows (unknown id): report 404, not a
		// 500 with a successful-looking response.
		writeError(w, http.StatusNotFound, "book not found")
		return
	}

	// Only genuine metadata edits are history-worthy: re-matching a book
	// resets {status: 'pending'} and a lifecycle touch (status, dest paths,
	// format) would otherwise flood the record timeline with indistinguishable
	// 'metadata update' rows. Only Authors/Narrators + catalog fields count as
	// metadata; provenance IDs, src/dest paths, format, status and
	// status_message are excluded pipeline bookkeeping.
	if req.Title != nil || req.ASIN != nil || req.Description != nil ||
		req.ReleaseDate != nil || req.Series != nil || req.Publisher != nil ||
		req.Language != nil || req.RuntimeLengthMinutes != nil || req.CoverImageURL != nil ||
		req.Authors != nil || req.Narrators != nil {
		h.svc.ProcessingSvc.RecordBookAction(bookID, "metadata_update", book.Title)
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
	// The metadata pass is best-effort: cap its wall clock per request so
	// a large batch on slow storage cannot hold intake open for hours.
	// Books after the deadline get a degraded row (the row contract holds).
	metadataDeadline := time.Now().Add(maxSourceMetadataBatchBudget)
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
		// Intake is the only moment the source is known to exist untouched:
		// read its own art now so Match can show the original cover beside
		// catalog candidates instead of an empty placeholder.
		h.attachSourceCover(r.Context(), id, entry.SrcPath)
		// The deadline is the wall-clock cap, not just a start gate: an
		// extraction begun just before it must not run its full per-book
		// budget past it, so the context is bound to it and the probe pass
		// is canceled when it expires.
		extractCtx, cancel := context.WithDeadline(r.Context(), metadataDeadline)
		if time.Now().Before(metadataDeadline) {
			h.attachSourceMetadata(extractCtx, id, entry.SrcPath)
		} else {
			log.Printf("source metadata for book %d: batch budget exhausted, extraction skipped", id)
			h.persistSourceMetadata(id, audio.DegradedSourceMetadata("batch wall-clock budget exhausted; extraction not run"))
		}
		cancel()
		h.svc.ProcessingSvc.RecordBookAction(id, "import", entry.SrcPath)
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

// sourceCoverTimeout bounds the ffmpeg run per ingested book: cover art is
// cosmetic and must not hold the intake request open on a slow mount.
const sourceCoverTimeout = 30 * time.Second

// maxSourceMetadataBatchBudget bounds the total wall clock of source
// metadata extraction per intake request: the per-book probe pass is up to
// 60s each, so a large batch on slow storage must not hold intake open.
// Tests may shrink it.
var maxSourceMetadataBatchBudget = 10 * time.Minute

// attachSourceCover extracts the art the source already carries into the
// covers directory and records its served URL on the book. Best-effort:
// sources without art keep the empty placeholder, and an extraction failure
// never fails intake.
func (h *Handler) attachSourceCover(ctx context.Context, bookID int64, srcPath string) {
	if h.svc.CoversDir == "" || h.svc.FFmpeg == "" {
		return
	}
	name := strconv.FormatInt(bookID, 10) + ".jpg"
	dest := filepath.Join(h.svc.CoversDir, name)
	extractCtx, cancel := context.WithTimeout(ctx, sourceCoverTimeout)
	defer cancel()
	if err := audio.ExtractCover(extractCtx, h.svc.FFmpeg, srcPath, dest); err != nil {
		if !errors.Is(err, audio.ErrNoCover) {
			log.Printf("extract source cover for book %d: %v", bookID, err)
		}
		os.Remove(dest)
		return
	}
	if _, err := h.svc.DB.Exec(
		"UPDATE books SET cover_image_url = ?, updated_at = datetime('now') WHERE id = ?",
		"/covers/"+name, bookID,
	); err != nil {
		log.Printf("record source cover for book %d: %v", bookID, err)
		os.Remove(dest)
	}
}

// attachSourceMetadata reads the source's own tags, IDs, and durations and
// persists an independent per-book snapshot. Every outcome writes a row
// (including ffprobe-missing hosts, which persist a degraded row with
// explicit error states); an INSERT failure never fails intake.
func (h *Handler) attachSourceMetadata(ctx context.Context, bookID int64, srcPath string) {
	var snap *audio.SourceMetadata
	var err error
	if h.svc.FFprobe == "" {
		snap = audio.DegradedSourceMetadata("ffprobe binary unavailable; extraction not run")
	} else {
		snap, err = audio.ExtractSourceMetadata(ctx, h.svc.FFprobe, srcPath)
		if err != nil || snap == nil {
			log.Printf("extract source metadata for book %d: %v", bookID, err)
			snap = audio.DegradedSourceMetadata(fmt.Sprintf("extraction failed: %v", err))
		}
	}
	h.persistSourceMetadata(bookID, snap)
}

// persistSourceMetadata writes one book's snapshot row; failures are
// log-only so the row never fails intake.
func (h *Handler) persistSourceMetadata(bookID int64, snap *audio.SourceMetadata) {
	payload, marshalErr := json.Marshal(snap)
	if marshalErr != nil {
		log.Printf("marshal source metadata for book %d: %v", bookID, marshalErr)
		return
	}
	if _, insertErr := h.svc.DB.Exec(
		"INSERT INTO book_source_metadata (book_id, payload) VALUES (?, ?)",
		bookID, payload,
	); insertErr != nil {
		log.Printf("persist source metadata for book %d: %v", bookID, insertErr)
	}
}

// audioExtensions are the containers a single-file source may use. Only these
// suffixes are stripped when deriving a title: a period anywhere else in a
// name (author initials like "Ursula K. Le Guin", "Vol. 2", "Dr.") is part of
// the title, not an extension.
var audioExtensions = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".m4b":  true,
	".mp4":  true,
	".aac":  true,
	".ogg":  true,
	".oga":  true,
	".opus": true,
	".flac": true,
	".wav":  true,
	".wma":  true,
	".mka":  true,
}

func deriveTitle(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); audioExtensions[strings.ToLower(ext)] {
		base = strings.TrimSuffix(base, ext)
	}
	name := strings.ReplaceAll(base, "_", " ")
	name = strings.ReplaceAll(name, ".", " ")
	name = strings.ReplaceAll(name, "-", " ")
	// Collapse the whitespace left behind by those separators.
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Untitled"
	}
	return titleCase(name)
}

// titleCase uppercases the first letter of each whitespace-separated word and
// leaves the rest of the word untouched. strings.Title is deprecated and also
// capitalizes after apostrophes ("Kane's" -> "Kane'S"), which mangles
// contractions in source filenames.
func titleCase(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	startOfWord := true
	for _, r := range name {
		if startOfWord {
			r = unicode.ToUpper(r)
		}
		b.WriteRune(r)
		startOfWord = unicode.IsSpace(r)
	}
	return b.String()
}

// replacePeople replaces the people for the roles PRESENT in the request.
// A nil list means "not provided" and leaves that role's existing rows
// untouched: the old delete-both-roles-then-insert shape wiped narrators
// whenever a metadata update supplied only authors (finding: F11).
// Delete + insert run in one transaction so a partial write cannot leave
// the book with a half-replaced role.
func replacePeople(db *sql.DB, bookID int64, authors, narrators []PersonUpdate) error {
	replaceRoles := make([]string, 0, 2)
	if authors != nil {
		replaceRoles = append(replaceRoles, "author")
	}
	if narrators != nil {
		replaceRoles = append(replaceRoles, "narrator")
	}
	if len(replaceRoles) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin people transaction: %w", err)
	}
	defer tx.Rollback() // safe to call after Commit

	// Delete only the roles being replaced; the other role's rows survive.
	placeholders := strings.Repeat("?,", len(replaceRoles))
	placeholders = strings.TrimSuffix(placeholders, ",")
	delArgs := make([]any, 0, len(replaceRoles)+1)
	delArgs = append(delArgs, bookID)
	for _, role := range replaceRoles {
		delArgs = append(delArgs, role)
	}
	if _, err := tx.Exec(
		"DELETE FROM people WHERE book_id = ? AND role IN ("+placeholders+")",
		delArgs...,
	); err != nil {
		return fmt.Errorf("delete people: %w", err)
	}

	insert, err := tx.Prepare(
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
	if authors != nil {
		if err := add("author", authors); err != nil {
			return fmt.Errorf("insert authors: %w", err)
		}
	}
	if narrators != nil {
		if err := add("narrator", narrators); err != nil {
			return fmt.Errorf("insert narrators: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit people transaction: %w", err)
	}
	return nil
}

// DeleteBook handles DELETE /api/books/:id, deleting the book and cascading people.
// A book with a queued or running processing job is refused with 409: the
// worker holds the book's row only loosely (processing_jobs.book_id is ON
// DELETE SET NULL), so deleting it would leave the job running against a
// record that no longer exists.
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

	// Count rather than list: the refusal only needs to state that the book
	// is busy, and a table-read failure must fail closed (never delete a
	// book whose job state could not be established).
	activeJobs, err := h.activeJobCount(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("check active jobs: %v", err))
		return
	}
	if activeJobs > 0 {
		writeError(w, http.StatusConflict, busyBookMessage(bookID, activeJobs))
		return
	}

	// The delete repeats the job check inside the statement: a job queued
	// between the count above and this Exec must not be orphaned either.
	res, err := h.svc.DB.Exec(
		"DELETE FROM books WHERE id = ? AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE book_id = ? AND status IN (?, ?))",
		bookID, bookID, jobStatusQueued, jobStatusRunning,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete book: %v", err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either the job appeared in the race window or the book vanished.
		// Re-count so the caller gets the actionable refusal rather than a
		// silent 204. The re-check is authoritative: a failed count fails
		// closed, because reporting success for a row that may still exist
		// would make the caller prune a book it did not delete.
		remaining, err := h.activeJobCount(bookID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("check active jobs after delete: %v", err))
			return
		}
		if remaining > 0 {
			writeError(w, http.StatusConflict, busyBookMessage(bookID, remaining))
			return
		}
		// No active job blocks it, so a surviving row means the book still
		// exists (a job went terminal between the guard and the DELETE).
		// Never answer 204 for a book that was not deleted.
		var exists int
		if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM books WHERE id = ?", bookID).Scan(&exists); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("confirm book deletion: %v", err))
			return
		}
		if exists > 0 {
			// The row survived while no job blocked it: a job reached the
			// queue between the re-count and the statement. Report the
			// conflict rather than deleting without knowing who owns it.
			writeError(w, http.StatusConflict, fmt.Sprintf(
				"book %d was not deleted; a processing job was queued concurrently — retry once it is visible", bookID))
			return
		}
	}

	// The extracted cover is keyed by book ID; leaving it behind would leak
	// an orphan and let a later book reuse the slot's art.
	if h.svc.CoversDir != "" {
		os.Remove(filepath.Join(h.svc.CoversDir, strconv.FormatInt(bookID, 10)+".jpg"))
	}

	w.WriteHeader(http.StatusNoContent)
}

// activeJobCount returns the number of queued or running processing jobs
// for a book.
func (h *Handler) activeJobCount(bookID int64) (int, error) {
	var activeJobs int
	err := h.svc.DB.QueryRow(
		"SELECT COUNT(*) FROM processing_jobs WHERE book_id = ? AND status IN (?, ?)",
		bookID, jobStatusQueued, jobStatusRunning,
	).Scan(&activeJobs)
	return activeJobs, err
}

// busyBookMessage is the delete refusal's operator-facing text: it names
// the remedy, since the client cannot cancel the job from the delete flow.
func busyBookMessage(bookID int64, activeJobs int) string {
	return fmt.Sprintf(
		"book %d has %d queued or running processing job(s); cancel them before deleting",
		bookID, activeJobs)
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

	// One placeholder per book, bound with the IDs: the same idiom the
	// source-ASIN projection uses.
	placeholders := make([]string, len(books))
	args := make([]any, len(books))
	for i, b := range books {
		placeholders[i] = "?"
		args[i] = b.ID
	}
	query := "SELECT id, book_id, name, role, audiobookdb_person_id, created_at FROM people WHERE book_id IN (" +
		strings.Join(placeholders, ",") + ")"

	rows, err := h.svc.DB.Query(query, args...)
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

// parseInt64Param converts a string to int64, returning -1 on error. The
// ENTIRE segment must be a base-10 integer: fmt.Sscanf would accept an
// integer prefix ("17oops" -> 17) and delete the wrong book, so a strict
// parse rejects any trailing non-numeric content.
func parseInt64Param(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
