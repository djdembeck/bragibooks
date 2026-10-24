package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bragibooks/bragibooks/internal/api"
	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/db"
	"github.com/bragibooks/bragibooks/internal/server"
	"github.com/bragibooks/bragibooks/webfs"
)

func main() {
	// Config
	cfgMgr := config.NewConfigManager()
	cfg := cfgMgr.Config()

	// Database — ensure config directory exists
	dbPath := cfg.Database.Path
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Run migrations
	if err := db.RunMigrations(database); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Load settings from DB into config struct FIRST so the legacy
	// auto-migration can carry the live base URL / API key through a
	// settings merge (row 53) — a DB row or YAML value written before the
	// migration must survive it.
	loadSettingsFromDB(database, cfgMgr)

	// Auto-migrate from legacy Django DB if present and new DB is empty
	autoMigrateLegacy(database, cfgMgr)

	// Ensure default settings row exists
	if err := ensureDefaultSettings(database); err != nil {
		log.Fatalf("Failed to ensure default settings: %v", err)
	}

	// Re-load (idempotent) so the config reflects the merged settings row
	// the migration may have just written.
	loadSettingsFromDB(database, cfgMgr)
	// Refresh cfg after potential DB-loaded overrides
	cfg = cfgMgr.Config()

	// Services
	processor := audio.NewProcessor(
		cfg.M4bMerge.Binary,
		cfg.Directories.OutputDir,
		cfg.Directories.CompletedDir,
		cfg.Processing.NumCPUs,
		cfg.Processing.PathFormat,
		cfg.Processing.LogLevel,
	)

	// Verify m4b-merge version meets minimum requirement
	if err := processor.CheckVersion(); err != nil {
		log.Fatalf("m4b-merge version check failed: %v", err)
	}

	audiobookDBClient := audiobookdb.NewClient(cfg.APIKey.APIKey, cfg.APIKey.BaseURL)
	audiobookDBVal := &atomic.Value{}
	audiobookDBVal.Store(audiobookDBClient)

	processingSvc := api.NewProcessingService(database, processor, cfg.Processing.NumCPUs)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go processingSvc.RunWorkers(ctx, cfg.Processing.NumCPUs)

	handler := api.NewHandler(api.Services{
		DB:            database,
		Config:        cfgMgr,
		Processor:     processor,
		AudiobookDB:   audiobookDBVal,
		ProcessingSvc: processingSvc,
	})

	// Router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// The SPA is served same-origin from this server, so the API does not
	// need CORS at all; the old wildcard-credentials combo was invalid and
	// opened the admin API to any origin (finding: CORS). No CORS handler
	// is mounted — cross-origin requests simply get no CORS headers and
	// the browser blocks them.

	// Health check lives OUTSIDE the gated /api subtree so container
	// orchestrators and the CLI can probe liveness without credentials.
	r.Get("/api/health", server.HealthCheck)

	// Admin auth (finding: unauthenticated API): token mode when
	// BRAGIBOOKS_ADMIN_TOKEN is set, loopback-LAN-only local mode when not.
	// Applied to the /api subtree only; the embedded SPA (served same
	// origin below) stays reachable for the UI to load.
	handler.RegisterRoutes(r, api.RequireAdmin(os.Getenv("BRAGIBOOKS_ADMIN_TOKEN")))

	// SPA handler — serve embedded frontend for non-API routes.
	r.HandleFunc("/*", webfs.Handler().ServeHTTP)

	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	log.Printf("Bragi Books listening on %s", addr)

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
		// Read-side timeouts keep the process responsive and keep
		// half-open connections from pinning workers (liveness).
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout MUST stay 0: job-stream (SSE) and directory-stream
		// handlers stay open for the lifetime of a multi-hour job; a 5m
		// write timeout killed them (finding).
		WriteTimeout: 0,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Block until a fatal server error or SIGINT/SIGTERM, then drain.
	select {
	case err := <-errCh:
		log.Fatalf("HTTP server error: %v", err)
	case <-ctx.Done():
		log.Printf("shutdown signal received, draining in-flight requests")
	}

	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func autoMigrateLegacy(newDB *sql.DB, cfgMgr *config.ConfigManager) {
	// Refuse to migrate into a populated DB (same guard the manual
	// POST /api/migrate path uses — single source of truth).
	if api.ShouldSkipMigration(newDB) {
		return // new DB already populated, no migration needed
	}

	// Search for legacy Django DB in candidate locations
	candidates := []string{
		os.Getenv("LEGACY_DB_PATH"), // explicit override
	}
	if cp := cfgMgr.ConfigPath(); cp != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(cp), "db.sqlite3"))
	}
	candidates = append(candidates,
		filepath.Join(filepath.Dir(cfgMgr.Config().Database.Path), "db.sqlite3"),
		"config/db.sqlite3",
		"/config/db.sqlite3",
	)

	var legacyPath string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			legacyPath = c
			break
		}
	}
	if legacyPath == "" {
		return // no legacy DB found
	}

	log.Printf("Legacy Django DB found at %s, starting auto-migration...", legacyPath)
	// Carry the in-memory (YAML/env/DB) base URL into the settings merge
	// so it survives the INSERT OR REPLACE (row 53).
	result, err := api.RunLegacyMigration(legacyPath, newDB, cfgMgr.Config().APIKey.BaseURL)
	if err != nil {
		log.Printf("Warning: auto-migration failed: %v (you can manually migrate via POST /api/migrate)", err)
		return
	}
	log.Printf("Auto-migration complete: %d books, %d people migrated", result.Books, result.People)
}

func loadSettingsFromDB(database *sql.DB, cfgMgr *config.ConfigManager) {
	cfg := cfgMgr.Config()
	yamlKeys := cfgMgr.YAMLKeys()

	var m4bBinary, inputDir, outputDir, completedDir, outputScheme, region, apiKey, baseURL string
	var numCPUs int

	err := database.QueryRow(`
		SELECT m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, audiobookdb_api_key, audiobookdb_base_url
		FROM settings WHERE id = 1
	`).Scan(&m4bBinary, &inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme, &region, &apiKey, &baseURL)
	if err != nil {
		log.Printf("Warning: could not load settings from DB: %v", err)
		return
	}

	// For each field: use DB value unless YAML explicitly set it or env var is set
	// (YAML takes precedence, then env vars, then DB, then defaults)
	if !yamlKeys["directories.input_dir"] && os.Getenv("DIRECTORIES_INPUT_DIR") == "" {
		cfg.Directories.InputDir = inputDir
	}
	if !yamlKeys["directories.output_dir"] && os.Getenv("DIRECTORIES_OUTPUT_DIR") == "" {
		cfg.Directories.OutputDir = outputDir
	}
	if !yamlKeys["directories.completed_dir"] && os.Getenv("DIRECTORIES_COMPLETED_DIR") == "" {
		cfg.Directories.CompletedDir = completedDir
	}
	if !yamlKeys["processing.num_cpus"] && os.Getenv("PROCESSING_NUM_CPUS") == "" {
		cfg.Processing.NumCPUs = numCPUs
	}
	if !yamlKeys["processing.path_format"] && os.Getenv("PROCESSING_PATH_FORMAT") == "" {
		cfg.Processing.PathFormat = outputScheme
	}
	// REGION is backward compat from the Python version
	if !yamlKeys["processing.region"] && os.Getenv("PROCESSING_REGION") == "" && os.Getenv("REGION") == "" {
		cfg.Processing.Region = region
	}
	if !yamlKeys["m4b_merge.binary"] && os.Getenv("M4B_MERGE_BINARY") == "" {
		cfg.M4bMerge.Binary = m4bBinary
	}
	if !yamlKeys["api_key.api_key"] && os.Getenv("API_KEY_API_KEY") == "" {
		cfg.APIKey.APIKey = apiKey
	}
	if !yamlKeys["api_key.base_url"] && os.Getenv("API_KEY_BASE_URL") == "" {
		cfg.APIKey.BaseURL = baseURL
	}
}

func ensureDefaultSettings(database *sql.DB) error {
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := database.Exec(`INSERT INTO settings (id, num_cpus, output_scheme, region) VALUES (1, 1, '{author}/{title}', 'us')`)
		return err
	}
	return nil
}
