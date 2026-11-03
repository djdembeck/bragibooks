package api

import (
	"fmt"
	"net/http"
	"strings"
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
