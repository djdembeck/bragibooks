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

	// Ensure default settings row exists
	if err := ensureDefaultSettings(database); err != nil {
		log.Fatalf("Failed to ensure default settings: %v", err)
	}

	// Services
	processor := audio.NewProcessor(
		cfg.M4bMerge.Binary,
		cfg.Directories.OutputDir,
		cfg.Directories.CompletedDir,
		cfg.Processing.NumCPUs,
		cfg.Processing.PathFormat,
		cfg.Processing.LogLevel,
	)

	audiobookDBClient := audiobookdb.NewClient(cfg.APIKey.APIKey)

	processingSvc := api.NewProcessingService(database, processor, cfg.Processing.NumCPUs)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go processingSvc.RunWorkers(ctx, cfg.Processing.NumCPUs)

	handler := api.NewHandler(api.Services{
		DB:            database,
		Config:        cfgMgr,
		Processor:     processor,
		AudiobookDB:   audiobookDBClient,
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