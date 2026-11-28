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
	"time"

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

	// pendingCovers tracks override cover assets ("ovr-<bookID>-*.jpg") that
	// an upload has written but that the SAVED override row does not reference
	// yet: the editor uploads first and persists the filename in a later
	// draft save. Prune sweeps keep non-expired entries so a save or reset
	// arriving between the upload and the draft save cannot delete the asset
	// the draft points at (the job would then degrade to no cover). Entries
	// expire after pendingCoverTTL. Guarded by pendingCoversMu.
	pendingCoversMu sync.Mutex
	pendingCovers   map[string]time.Time
	// FFmpeg is the ffmpeg binary used to read embedded art. Empty disables
	// extraction (ffmpeg is not installed).
	FFmpeg string

	// FFprobe is the ffprobe binary used to read embedded tags and durations.
	// Empty disables source metadata extraction (ffprobe is not installed).
	FFprobe string

	// IndexSvc is the opt-in input-directory index (pre-extracted source
	// evidence served by the Import page). Always wired; its Enabled() gate
	// reads the live config flag, so a disabled deployment never touches it.
	IndexSvc *IndexService
}

// Handler wraps services and registers routes.
type Handler struct {
	svc *Services
	// previewSlots bounds concurrent engine PREVIEW subprocesses. The preview
	// endpoint is outside the processing worker limit, and each request can
	// launch an engine process immediately: without a cap a caller with API
	// access (including the LAN path) can exhaust process/memory resources
	// with concurrent previews, especially while the engine is slow or stuck.
	previewSlots chan struct{}
}

// enginePreviewConcurrency bounds the in-flight engine preview subprocesses.
const enginePreviewConcurrency = 4

// enginePreviewDeadline bounds a single preview's server-side execution
// independent of the client's connection: r.Context() only fires when the
// client gives up, so a client that keeps its connection open would pin a
// slow/stuck engine process for as long as it wished. A var (not a const)
// so a test can shorten it to exercise the deadline path in milliseconds
// (EnginePreview's WithTimeout takes the MINIMUM of this and the request
// context, so a request-side timeout would mask the server-side one).
var enginePreviewDeadline = 30 * time.Second

// NewHandler creates a new Handler with the given services.
func NewHandler(svc *Services) *Handler {
	return &Handler{
		svc:          svc,
		previewSlots: make(chan struct{}, enginePreviewConcurrency),
	}
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
		r.Get("/books/{id}/reprocess", h.GetBookReprocess)
		r.Get("/books/{id}/match-evidence", h.GetMatchEvidence)

		// Manual metadata editor (per-book override layer, B3): state,
		// save, reset-to-catalog, and the operator's own artwork upload.
		r.Get("/books/{id}/metadata", h.GetBookMetadata)
		r.Put("/books/{id}/metadata", h.UpdateBookMetadata)
		r.Post("/books/{id}/metadata/reset", h.ResetBookMetadata)
		r.Post("/books/{id}/metadata/cover", h.UploadBookMetadataCover)

		// Processing
		r.Post("/process", h.StartProcessing)
		r.Post("/process/reprocess", h.StartReprocess)
		r.Get("/jobs", h.ListJobs)
		r.Post("/jobs/{id}/cancel", h.CancelJob)
		r.Get("/jobs/{id}", h.GetJobStatus)
		r.Get("/jobs/{id}/stream", h.StreamJob)

		// audiobookdb search proxy
		r.Get("/search", h.SearchAudiobookdb)
		r.Get("/search/books/{id}", h.GetBookFromDB)
		r.Get("/search/releases/{id}", h.GetReleaseFromDB)
		r.Get("/search/by-asin/{asin}", h.GetBookByASIN)

		// Engine protocol: verified capabilities (Settings picker and path
		// examples) and the non-mutating job preview (Match preview).
		r.Get("/engine/capabilities", h.EngineCapabilities)
		r.Post("/engine/preview", h.EnginePreview)

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

		// Input-directory index
		r.Get("/index", h.GetIndexStatus)
		r.Post("/index/refresh", h.RefreshIndex)

		// Migration
		r.Post("/migrate", h.MigrateLegacyDB)
		r.Post("/migrate/people", h.RecoverPeople)
	})

	// Extracted source covers are library content — the same art the book
	// endpoints return — so they sit behind the same gate rather than being
	// enumerable by book ID. They are mounted outside /api because the
	// `<img>` tags that render them cannot attach a bearer header, so they
	// are admitted via the same no-key loopback/LAN fallback the /api
	// routes use (GET is CSRF-exempt). Consequence: a remote API-key client
	// cannot fetch covers — `<img>` requests carry no Authorization header,
	// and the SPA does NOT mirror its token into a cookie (it only clears
	// a leftover `bragibooks_token`, see web/src/lib/api.ts); remote API-key
	// clients cannot load cover art via <img>: the book endpoints return only
	// a protected cover URL, and images cannot attach the Authorization
	// header.
	r.Group(func(r chi.Router) {
		r.Use(auth)
		// The preview endpoint is registered BEFORE the /covers/* wildcard:
		// a wildcard matches /covers/preview too, and chi routes the
		// specific pattern first only when it is declared first.
		r.Handle("/covers/preview", CoverPreviewHandler(h))
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
