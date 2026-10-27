package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
)

// Services holds all dependencies needed by API handlers.
type Services struct {
	DB            *sql.DB
	Config        *config.ConfigManager
	Processor     *audio.Processor
	AudiobookDB   *atomic.Value
	ProcessingSvc *ProcessingService

	// settingsMu serializes every full settings-row read-modify-write
	// (the form save and the external-API enable/disable/regenerate
	// flows): both persist the whole row, and without this lock a
	// concurrent pair can interleave snapshot-read and row-write so a
	// later save clobbers what the earlier one persisted (e.g. a form
	// save landing after an enable clears the freshly stored key digest).
	settingsMu sync.Mutex

	// SettingsPersistDelay is a test hook called, under the settings
	// lock, inside the pre-commit window of the settings persistence
	// flows — between the flow's boundaries, either just before the
	// snapshot read (the external-API state flows: the write must be
	// built from the state as it exists at commit time) or after the
	// snapshot read (the form save). Production wiring leaves it nil;
	// tests use it to widen the window so an unsynchronized
	// interleaving becomes deterministic.
	SettingsPersistDelay func()

	// CoversDir is the on-disk directory for cover art extracted from
	// source media. Empty disables extraction; the UI keeps its placeholder.
	CoversDir string
	// FFmpeg is the ffmpeg binary used to read embedded art. Empty disables
	// extraction (ffmpeg is not installed).
	FFmpeg string
}

// Handler wraps services and registers routes.
type Handler struct {
	svc *Services
}

// NewHandler creates a new Handler with the given services.
func NewHandler(svc *Services) *Handler {
	return &Handler{svc: svc}
}

// getAudiobookDB returns the current audiobookdb.Client from the atomic value.
func (h *Handler) getAudiobookDB() *audiobookdb.Client {
	return h.svc.AudiobookDB.Load().(*audiobookdb.Client)
}

// RegisterRoutes mounts all API routes on the given chi router behind the
// auth middleware (token or loopback-LAN, see auth.go). The /api/health
// route is intentionally NOT mounted here: the caller serves it at the
// router root, outside the gated subtree, so liveness probes stay
// unauthenticated.
func (h *Handler) RegisterRoutes(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Route("/api", func(r chi.Router) {
		r.Use(auth)

		// Books CRUD
		r.Get("/books", h.ListBooks)
		r.Post("/books", h.CreateBooks)
		r.Get("/books/counts", h.GetBooksCounts)
		r.Get("/books/{id}", h.GetBook)
		r.Put("/books/{id}", h.UpdateBook)
		r.Delete("/books/{id}", h.DeleteBook)

		// Processing
		r.Post("/process", h.StartProcessing)
		r.Get("/jobs", h.ListJobs)
		r.Post("/jobs/{id}/cancel", h.CancelJob)
		r.Get("/jobs/{id}", h.GetJobStatus)
		r.Get("/jobs/{id}/stream", h.StreamJob)

		// audiobookdb search proxy
		r.Get("/search", h.SearchAudiobookdb)
		r.Get("/search/books/{id}", h.GetBookFromDB)
		r.Get("/search/releases/{id}", h.GetReleaseFromDB)

		// Output profiles
		r.Get("/output-profiles", h.ListOutputProfiles)
		r.Post("/output-profiles", h.CreateOutputProfile)
		r.Put("/output-profiles/{id}", h.UpdateOutputProfile)
		r.Delete("/output-profiles/{id}", h.DeleteOutputProfile)

		// Settings
		r.Get("/settings", h.GetSettings)
		r.Put("/settings", h.UpdateSettings)
		r.Post("/settings/api-endpoint/enable", h.EnableAPIEndpoint)
		r.Post("/settings/api-endpoint/disable", h.DisableAPIEndpoint)
		r.Post("/settings/api-endpoint/regenerate", h.RegenerateAPIEndpoint)

		// Directory browsing
		r.Get("/directories", h.ListDirectories)
		r.Get("/directories/stream", h.StreamDirectories)
		r.Get("/directories/tree", h.GetDirectoryTree)

		// Migration
		r.Post("/migrate", h.MigrateLegacyDB)
		r.Post("/migrate/people", h.RecoverPeople)
	})

	// Extracted source covers are library content — the same art the book
	// endpoints return — so they sit behind the same gate rather than being
	// enumerable by book ID. They are mounted outside /api only because the
	// `<img>` tags that render them cannot attach a bearer header: the SPA
	// mirrors its token into the same-origin `bragibooks_token` cookie
	// (web/src/lib/api.ts), which the browser sends with those requests, and
	// local mode admits loopback/LAN clients exactly as it does for the API.
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Handle("/covers/*", CoverHandler(h.svc.CoversDir))
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
