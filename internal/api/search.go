package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audiobookdb"
)

// SearchAudiobookdb handles GET /api/search.
// Proxies to audiobookdb POST /search with query parameters.
// Query params: ?query=...&types=books&skip=0&take=20
func (h *Handler) SearchAudiobookdb(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	typesStr := r.URL.Query().Get("types")
	if typesStr == "" {
		typesStr = "books"
	}
	types := strings.Split(typesStr, ",")
	for i, t := range types {
		types[i] = strings.TrimSpace(t)
	}

	skip := parseIntQueryParam(r, "skip", 0)
	take := parseIntQueryParam(r, "take", 20)

	resp, err := h.getAudiobookDB().Search(r.Context(), query, types, skip, take)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("audiobookdb search: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetBookFromDB handles GET /api/search/books/:id.
// Fetches a book from audiobookdb with optional include parameter.
func (h *Handler) GetBookFromDB(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "book ID is required")
		return
	}

	include := r.URL.Query().Get("include")
	if include == "" {
		include = "external,genres,people,releases,series,tags,images"
	}

	book, err := h.getAudiobookDB().GetBook(r.Context(), id, include)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("audiobookdb get book: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, book)
}

// GetReleaseFromDB handles GET /api/search/releases/:id.
// Fetches a release from audiobookdb with optional include parameter.
func (h *Handler) GetReleaseFromDB(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "release ID is required")
		return
	}

	include := r.URL.Query().Get("include")
	if include == "" {
		include = "book,chapterDetail,external,images,language,people,publisher"
	}

	release, err := h.getAudiobookDB().GetRelease(r.Context(), id, include)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("audiobookdb get release: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, release)
}

// GetBookByASIN handles GET /api/search/by-asin/:asin.
// Resolves a book directly from its Audible ASIN via the upstream
// external/audible endpoint. Returns 400 when the ASIN does not have the
// Audible shape, 404 when the ASIN is not in the catalog, and 502 on
// other upstream failures.
func (h *Handler) GetBookByASIN(w http.ResponseWriter, r *http.Request) {
	asin := chiURLParam(r, "asin")
	if asin == "" {
		writeError(w, http.StatusBadRequest, "asin parameter is required")
		return
	}
	// Validate the Audible ASIN shape before it is interpolated into the
	// upstream URL: a decoded %2F or ? in the parameter would alter the
	// request structure on a key-carrying request.
	if !audiobookdb.IsValidASIN(asin) {
		writeError(w, http.StatusBadRequest, "asin must be 10 uppercase alphanumeric characters")
		return
	}
	book, err := h.getAudiobookDB().GetBookByASIN(r.Context(), asin)
	if err != nil {
		switch {
		case errors.Is(err, audiobookdb.ErrInvalidASIN):
			writeError(w, http.StatusBadRequest, "asin must be 10 uppercase alphanumeric characters")
		case errors.Is(err, audiobookdb.ErrNotFound):
			writeError(w, http.StatusNotFound, "no catalog record for this ASIN")
		default:
			writeError(w, http.StatusBadGateway, fmt.Sprintf("audiobookdb ASIN lookup: %v", err))
		}
		return
	}
	writeJSON(w, http.StatusOK, book)
}
