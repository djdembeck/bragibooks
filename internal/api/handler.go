package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/server"
)

// Services holds all dependencies needed by API handlers.
type Services struct {
	DB            *sql.DB
	Config        *config.ConfigManager
	Processor     *audio.Processor
	AudiobookDB   *atomic.Value
	ProcessingSvc *ProcessingService
}

// Handler wraps services and registers routes.
type Handler struct {
	svc Services
}

// NewHandler creates a new Handler with the given services.
func NewHandler(svc Services) *Handler {
	return &Handler{svc: svc}
}

// getAudiobookDB returns the current audiobookdb.Client from the atomic value.
func (h *Handler) getAudiobookDB() *audiobookdb.Client {
	return h.svc.AudiobookDB.Load().(*audiobookdb.Client)
}

// RegisterRoutes mounts all API routes on the given chi router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/api", func(r chi.Router) {
		// Health
		r.Get("/health", server.HealthCheck)

		// Books CRUD
		r.Get("/books", h.ListBooks)
		r.Post("/books", h.CreateBooks)
		r.Get("/books/{id}", h.GetBook)
		r.Put("/books/{id}", h.UpdateBook)
		r.Delete("/books/{id}", h.DeleteBook)

		// Processing
		r.Post("/process", h.StartProcessing)
		r.Get("/jobs", h.ListJobs)
		r.Get("/jobs/{id}", h.GetJobStatus)
		r.Get("/jobs/{id}/stream", h.StreamJob)

		// audiobookdb search proxy
		r.Get("/search", h.SearchAudiobookdb)
		r.Get("/search/books/{id}", h.GetBookFromDB)
		r.Get("/search/releases/{id}", h.GetReleaseFromDB)

		// Settings
		r.Get("/settings", h.GetSettings)
		r.Put("/settings", h.UpdateSettings)

		// Directory browsing
		r.Get("/directories", h.ListDirectories)
		r.Get("/directories/stream", h.StreamDirectories)
		r.Get("/directories/tree", h.GetDirectoryTree)

		// Migration
		r.Post("/migrate", h.MigrateLegacyDB)
		r.Post("/migrate/people", h.RecoverPeople)
	})
}

// writeJSON encodes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// writeError writes a JSON error response with the given status and message.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// chiURLParam extracts a URL parameter by key from the chi.Context.
func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// parseIntQueryParam reads an integer query parameter from the request.
// Returns defaultVal if the key is missing or the value cannot be parsed.
func parseIntQueryParam(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return n
}

// parseBoolQueryParam reads a boolean query parameter from the request.
// Accepts "true", "1", "yes" (case-insensitive) as true.
func parseBoolQueryParam(r *http.Request, key string) bool {
	s := strings.ToLower(r.URL.Query().Get(key))
	return s == "true" || s == "1" || s == "yes"
}
