package main

import (
	"context"
	"database/sql"
	"fmt"
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

	// Absolute path so a container log reader can immediately tell
	// persistent volume (/config/...) from writable layer (/app/...).
	if absPath, absErr := filepath.Abs(dbPath); absErr == nil {
		log.Printf("Database: %s", absPath)
	} else {
		log.Printf("Database: %s", dbPath)
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

	// Diagnostics: make a non-persistent database.path (new-DB
	// mtime == boot time) visible in the boot log.
	logDBFileDiag("New DB", cfgMgr.Config().Database.Path)
	logDBFileDiag("Legacy DB", legacyPath)

	if receipt, exists, rerr := api.ReadMigrationReceipt(legacyPath); rerr != nil {
		log.Printf("Warning: could not read migration receipt for %s: %v (proceeding with migration)", legacyPath, rerr)
	} else if exists && api.LegacyMatchesReceipt(legacyPath, receipt) {
		// -1: target unknown (same as current DB, absent, or not checked);
		// 0: empty; >0: data present.
		targetBooks := -1
		if t := receipt.TargetNewDB; t != "" && t != cfgMgr.Config().Database.Path {
			if _, serr := os.Stat(t); serr == nil {
				if n, ok := countBooksReadonly(t); ok {
					targetBooks = n
				} else {
					log.Printf(
						"WARNING: legacy DB %s was already auto-migrated to %s on %s (%d books),\n"+
							"\tbut that database exists and could not be read — verify it is intact before re-migrating.\n"+
							"\tOnce restored, set database.path back to %s, delete %s and restart to keep your data.",
						legacyPath, t, receipt.MigratedAt, receipt.Books,
						t, legacyPath+api.MigrationReceiptSuffix,
					)
					return
				}
			}
		}
		if targetBooks > 0 {
			log.Printf(
				"WARNING: legacy DB %s was already auto-migrated to %s on %s (%d books), and that database is still present with %d books,\n"+
					"\tbut current database.path (%s) does not point there.\n"+
					"\tSet database.path back to %s (or copy %s), then delete %s and restart.",
				legacyPath, receipt.TargetNewDB, receipt.MigratedAt, receipt.Books, targetBooks,
				cfgMgr.Config().Database.Path, receipt.TargetNewDB, receipt.TargetNewDB, legacyPath+api.MigrationReceiptSuffix,
			)
		} else {
			log.Printf(
				"WARNING: legacy DB %s was already auto-migrated on %s (%d books, %d people into %s),\n"+
					"\tbut %s is empty — the merged data is gone, only the legacy file itself is intact.\n"+
					"\tdatabase.path (%s) is probably not persistent. Fix the setting, delete %s and restart (or POST /api/migrate) to migrate fresh.",
				legacyPath, receipt.MigratedAt, receipt.Books, receipt.People, receipt.TargetNewDB,
				receipt.TargetNewDB, cfgMgr.Config().Database.Path, legacyPath+api.MigrationReceiptSuffix,
			)
		}
		return
	} else if exists {
		log.Printf("Receipt found for %s but legacy DB changed since; proceeding with migration", legacyPath)
	}

	log.Printf("Legacy Django DB found at %s, starting auto-migration...", legacyPath)
	// Carry the in-memory (YAML/env/DB) base URL into the settings merge
	// so it survives the INSERT OR REPLACE (row 53).
	result, err := api.RunLegacyMigration(legacyPath, newDB, cfgMgr.Config().APIKey.BaseURL)
	if err != nil {
		log.Printf("Warning: auto-migration failed: %v (you can manually migrate via POST /api/migrate)", err)
		return
	}
	if err := api.WriteMigrationReceipt(legacyPath, cfgMgr.Config().Database.Path, result.Books, result.People); err != nil {
		log.Printf("Warning: failed to write migration receipt: %v", err)
	}
	log.Printf("Auto-migration complete: %d books, %d people migrated", result.Books, result.People)
}

// countBooksReadonly returns the number of books in the SQLite DB at path
// (read-only open). ok is false when the file is missing/unreadable, so
// callers can distinguish "empty" from "cannot tell".
func countBooksReadonly(path string) (count int, ok bool) {
	d, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		return 0, false
	}
	defer d.Close()
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM books").Scan(&n); err != nil {
		return 0, false
	}
	return n, true
}

// logDBFileDiag logs size and mtime of a SQLite DB file (plus its -wal
// sidecar, which holds un-checkpointed data — the .db main file alone
// understates size for a WAL-mode DB; the stat can race a checkpoint,
// approximating size — that is acceptable for diagnostics). Emits
// "not found" on stat error so a missing file is itself diagnostic.
func logDBFileDiag(label, path string) {
	if absPath, absErr := filepath.Abs(path); absErr == nil {
		path = absPath
	}
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("%s: %s (not found: %v)", label, path, err)
		return
	}
	size := info.Size()
	if wal, werr := os.Stat(path + "-wal"); werr == nil {
		size += wal.Size()
	}
	log.Printf("%s: %s (size: %d bytes, mtime: %s)", label, path, size, info.ModTime().Format(time.RFC3339))
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
