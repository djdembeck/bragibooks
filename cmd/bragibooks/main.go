package main

import (
	"context"
	"database/sql"
	"sync/atomic"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/bragibooks/bragibooks/internal/api"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/db"
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

	// Auto-migrate from legacy Django DB if present and new DB is empty
	autoMigrateLegacy(database, cfg.Database.Path)

	// Ensure default settings row exists
	if err := ensureDefaultSettings(database); err != nil {
		log.Fatalf("Failed to ensure default settings: %v", err)
	}

	// Load settings from DB into config struct (DB values fill in gaps not covered by YAML/env)
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
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	handler.RegisterRoutes(r)

	// SPA handler — serve embedded frontend for non-API routes
	// In dev mode (without webui tag), this returns a 404 fallback
	r.HandleFunc("/*", webfs.Handler().ServeHTTP)

	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	log.Printf("Bragi Books listening on %s", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func autoMigrateLegacy(newDB *sql.DB, configDir string) {
	// Default legacy DB path: config/db.sqlite3
	legacyPath := filepath.Join(filepath.Dir(configDir), "db.sqlite3")
	// Also check env var override
	if p := os.Getenv("LEGACY_DB_PATH"); p != "" {
		legacyPath = p
	}

	if _, err := os.Stat(legacyPath); err != nil {
		return // no legacy DB found, skip
	}

	// Check if new DB already has books (skip if non-empty)
	var bookCount int
	if err := newDB.QueryRow("SELECT COUNT(*) FROM books").Scan(&bookCount); err != nil {
		log.Printf("Warning: could not check book count for auto-migration: %v", err)
		return
	}
	if bookCount > 0 {
		log.Printf("Legacy DB found at %s but new DB already has %d books, skipping auto-migration", legacyPath, bookCount)
		return
	}

	log.Printf("Legacy Django DB found at %s, starting auto-migration...", legacyPath)
	result, err := api.RunLegacyMigration(legacyPath, newDB)
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