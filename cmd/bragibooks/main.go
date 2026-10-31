package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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

	// One-time migration of the legacy BRAGIBOOKS_ADMIN_TOKEN into the
	// settings-based external API key, so an upgrade with the env token
	// does not silently lock out remote clients (finding: legacy token
	// cutover). Runs before the startup state line so that line reports
	// the post-migration state. When it applies, the live state is
	// swapped from the persisted digest (mirrors the settings endpoints'
	// SetAPIEndpointState step).
	migrated, migratedDigest, migErr := migrateLegacyAdminToken(database)
	if migErr != nil {
		// A persistence failure on ANY path that observed the token is
		// fatal: the one-shot marker did not reach the row, so the token
		// could re-seed the external API key on a later boot. A broken
		// DB layer failing at startup is the correct, visible state —
		// no background retry is attempted.
		log.Fatalf("Legacy token migration aborted startup: %v", migErr)
	}
	if migrated {
		cfgMgr.SetAPIEndpointState(true, migratedDigest)
		cfg = cfgMgr.Config()
	}

	// One startup line about the external API state (the auth middleware
	// itself logs nothing).
	log.Print(externalAPIStatusLine(cfgMgr.AdminAuthState()))

	// Cover art extracted from source media lives beside the database, so
	// the container's persistent /config volume keeps it across restarts.
	coversDir := filepath.Join(filepath.Dir(dbPath), "covers")
	if err := os.MkdirAll(coversDir, 0755); err != nil {
		log.Printf("Warning: could not create covers directory %s: %v (source covers disabled)", coversDir, err)
		coversDir = ""
	}
	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")
	if ffmpegErr != nil {
		log.Printf("Warning: ffmpeg not found on PATH (%v) — source cover art will not be extracted", ffmpegErr)
		ffmpegPath = ""
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

	// Verify m4b-merge version meets minimum requirement and store the
	// reported version for display in the settings page.
	mergeVersion, err := processor.CheckVersion()
	if err != nil {
		log.Fatalf("m4b-merge version check failed: %v", err)
	}
	processor.StoreVersion(mergeVersion)

	audiobookDBClient := audiobookdb.NewClient(cfg.APIKey.APIKey, cfg.APIKey.BaseURL)
	audiobookDBVal := &atomic.Value{}
	audiobookDBVal.Store(audiobookDBClient)

	processingSvc := api.NewProcessingService(database, processor, cfg.Processing.NumCPUs)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go processingSvc.RunWorkers(ctx, cfg.Processing.NumCPUs)

	handler := api.NewHandler(&api.Services{
		DB:            database,
		Config:        cfgMgr,
		Processor:     processor,
		AudiobookDB:   audiobookDBVal,
		ProcessingSvc: processingSvc,
		CoversDir:     coversDir,
		FFmpeg:        ffmpegPath,
	})

	// Router
	r := chi.NewRouter()
	// Validate authority before auth, health, covers, or SPA handling.
	hostGuard, err := api.ValidateHost(cfg.Server)
	if err != nil {
		log.Fatalf("Invalid Host configuration: %v", err)
	}
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// Mounted last so Host denials still get a request ID and an access-log
	// line recording which authority was refused, while remaining ahead of
	// every route registration (auth, health, covers, SPA all stay gated).
	r.Use(hostGuard)
	// The SPA is served same-origin from this server, so the API does not
	// need CORS at all; the old wildcard-credentials combo was invalid and
	// opened the admin API to any origin (finding: CORS). No CORS handler
	// is mounted — cross-origin requests simply get no CORS headers and
	// the browser blocks them.

	// Health check lives OUTSIDE the gated /api subtree so container
	// orchestrators and the CLI can probe liveness without credentials.
	r.Get("/api/health", server.HealthCheck)

	// Admin auth (finding: unauthenticated API): loopback-LAN access is
	// always available (the same-origin UI relies on it via CSRF); when
	// the external API is enabled, a valid API key additionally allows
	// any address. State is read per request from the config manager so
	// Settings-page toggles apply without a restart. Applied to the /api
	// subtree and to the extracted source covers; the embedded SPA (served
	// same origin below) stays reachable for the UI to load. Covers are
	// mounted inside RegisterRoutes so they cannot drift outside this gate.
	handler.RegisterRoutes(r, api.RequireAdminWithState(cfgMgr.AdminAuthState))

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

// externalAPIStatusLine renders the one startup line about the external
// API state (the auth middleware itself logs nothing): which state
// (disabled / enabled with key / enabled without key) sees which message.
func externalAPIStatusLine(state config.AdminAuthState) string {
	switch {
	case !state.Enabled:
		return "External API disabled — loopback/LAN access only."
	case state.KeyDigest != "":
		return "External API enabled — remote access authorized with API key; LAN access unchanged."
	default:
		return "External API enabled via env but no key exists — open Settings → External API → Reissue key to mint one for remote clients."
	}
}

// digestPresent renders a digest's presence for a warning line without
// leaking the value itself.
func digestPresent(digest string) string {
	if digest == "" {
		return "empty"
	}
	return "present"
}

// migrateLegacyAdminToken performs the one-time migration of the legacy
// BRAGIBOOKS_ADMIN_TOKEN env var into the settings-based external API
// key (finding: legacy token cutover).
//
// It applies exactly once, ever, only when ALL of:
//  1. BRAGIBOOKS_ADMIN_TOKEN is set;
//  2. the deployment pin SERVER_API_ENDPOINT_ENABLED is absent — the pin
//     is only valid for post-cutover deployments, and if it is present
//     the deployment chose to manage the flag itself, so the legacy var
//     is ignored;
//  3. the STORED state (read from the DB row, not the construction-time
//     config snapshot — the legacy Django auto-migration and
//     ensureDefaultSettings may have just written the row) is fresh:
//     api_enabled=0 with an empty api_key_digest;
//  4. the one-shot marker legacy_token_migrated in the settings row
//     (schema v6) is false.
//
// Condition 4 alone is what keeps the migration from resurrecting a
// token the user later disabled: a disable clears the digest back to
// ”, which by itself looks exactly like the pre-upgrade state — the
// marker is never cleared, so the re-seed never re-runs. A rotation
// after an explicit disable+enable is covered by condition 3 (the state
// was no longer fresh when the marker flipped).
//
// Seeding is the only thing that is one-shot: the marker is consumed on
// FIRST observation of the variable on every path that reaches it — the
// fresh-state seed, the non-fresh warn-and-ignore, and the pin-suppressed
// run alike — so a later disable can never leave the row reading "fresh
// and unconsumed", and a pin removed later cannot re-seed the token.
//
// When it applies, the state seed and the marker flip are ONE row-write
// (the row's existing values are carried through), persisted to the DB
// only — the YAML flag file is deliberately NOT written. main() swaps
// the live config state from the returned digest so the rest of
// startup and the auth middleware see the migrated key immediately.
//
// Returns (true, digest) when it migrated, (false, "") otherwise. A
// non-nil error means the process MUST NOT continue: the legacy token was
// observed but its one-shot marker (or the migration itself) could not be
// persisted — main() fatals on it, because a row that lost the mark would
// read as unconsumed and the token could re-seed the external API key on
// a later boot (finding: legacy token cutover, P1).
func migrateLegacyAdminToken(database *sql.DB) (bool, string, error) {
	legacyToken := strings.TrimSpace(os.Getenv("BRAGIBOOKS_ADMIN_TOKEN"))
	if legacyToken == "" {
		return false, "", nil
	}
	if os.Getenv("SERVER_API_ENDPOINT_ENABLED") != "" {
		// The pin manages the flag, so the legacy var never seeds — but
		// its first observation still consumes the one-shot marker, the
		// same way the non-fresh warn-and-ignore below does, so a pin
		// removed later cannot re-seed the token (finding: legacy token
		// cutover, P1 no-resurrection-after-disable). consumeLegacyToken-
		// MarkerOnObservation is a no-op once the marker is set, so boots
		// with the pin present do not rewrite the row.
		var present int
		if err := database.QueryRow("SELECT 1 FROM settings WHERE id = 1").Scan(&present); err == nil {
			if err := consumeLegacyTokenMarkerOnObservation(database, "the deployment pin SERVER_API_ENDPOINT_ENABLED manages the external API flag"); err != nil {
				// Fail closed: the observed token's mark did not persist,
				// so the row would read as un-consumed — a pin removed
				// later (or any later disable) could re-seed the token.
				return false, "", fmt.Errorf("the legacy token was observed under the deployment pin, but its one-shot observation mark could not be persisted: %v — startup aborted to prevent the token re-seeding the external API key after a later disable or pin removal; fix the database and restart", err)
			}
		}
		return false, "", nil
	}

	// Read the whole row so nothing the Django auto-migration carried in
	// is discarded by the seed's INSERT OR REPLACE (same carry-through
	// rule as migrateSettings). No row yet → the seed creates it.
	var m4bBinary, inputDir, outputDir, completedDir, outputScheme, region, apiKey, baseURL string
	var numCPUs int
	var apiEnabled int
	var digest string
	var markerVal int
	err := database.QueryRow(`
		SELECT m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, audiobookdb_api_key, audiobookdb_base_url,
		       api_enabled, api_key_digest, COALESCE(legacy_token_migrated, 0)
		FROM settings WHERE id = 1
	`).Scan(&m4bBinary, &inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme, &region, &apiKey, &baseURL, &apiEnabled, &digest, &markerVal)
	if err == sql.ErrNoRows {
		// A new database with no settings row yet: nothing to migrate.
		return false, "", nil
	}
	if err != nil {
		// The token is set, so the mark must eventually persist; an
		// unreadable row leaves that unproven — fail closed.
		return false, "", fmt.Errorf("the legacy token was observed, but the settings row could not be read to check its migration state: %v — startup aborted so the token cannot re-seed the external API key on a later boot; fix the database and restart", err)
	}
	if markerVal != 0 {
		log.Printf("Warning: BRAGIBOOKS_ADMIN_TOKEN is set but the legacy token was already migrated into the external API key (legacy_token_migrated) and is now ignored — manage remote access in Settings → External API.")
		return false, "", nil
	}
	if apiEnabled != 0 || digest != "" {
		// The token is ignored, but its observation still consumes the
		// one-shot marker in this same row-read's write: leaving it unset
		// would let a later user disable (api_enabled=0, digest='') read
		// as "fresh" and re-seed the token on the next boot — resurrection
		// after an explicit disable (finding: legacy token cutover, P1).
		if err := consumeLegacyTokenMarkerOnObservation(database, "the external API already has state in the database"); err != nil {
			// Fail closed: without the mark persisted a later user
			// disable (api_enabled=0, digest='') would read as fresh and
			// re-seed the token on the next boot.
			return false, "", fmt.Errorf("the legacy token was observed while the external API already has state in the database (api_enabled=%d, api_key_digest %s), but its one-shot observation mark could not be persisted: %v — startup aborted to prevent the token re-seeding the external API key after a later disable; fix the database and restart", apiEnabled, digestPresent(digest), err)
		}
		log.Printf("Warning: BRAGIBOOKS_ADMIN_TOKEN is set but the external API already has state in the database (api_enabled=%d, api_key_digest %s) — the legacy token is ignored and its first-observation mark is set; manage remote access in Settings → External API.", apiEnabled, digestPresent(digest))
		return false, "", nil
	}

	sum := sha256.Sum256([]byte(legacyToken))
	migratedDigest := hex.EncodeToString(sum[:])
	if _, err := database.Exec(`
		INSERT OR REPLACE INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, api_enabled, api_key_digest, legacy_token_migrated, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, 1, datetime('now'))
	`,
		apiKey,
		baseURL,
		m4bBinary,
		inputDir,
		outputDir,
		completedDir,
		numCPUs,
		outputScheme,
		region,
		migratedDigest,
	); err != nil {
		// Fail closed: the seed write is what makes the cutover a fact
		// (row + marker persisted). Retrying on the next boot would
		// leave this boot running with the token un-migrated — the key
		// the remote clients got would not exist.
		return false, "", fmt.Errorf("the legacy token migration could not persist the migrated key and marker: %v — startup aborted so the token cannot re-seed the external API key on a later boot with a partially persisted state; fix the database and restart", err)
	}

	log.Printf("Migrated legacy admin token: BRAGIBOOKS_ADMIN_TOKEN was one time migrated into the external API key (remote access is now key-based) and is now IGNORED — manage remote access in Settings → External API.")
	return true, migratedDigest, nil
}

// consumeLegacyTokenMarkerOnObservation marks legacy_token_migrated on the
// id=1 settings row. The whole row is replaced, so every other column is
// carried through unchanged, byte for byte (same carry-through rule as
// migrateSettings) — api_enabled and api_key_digest are deliberately left
// exactly as they are: consuming the marker never touches the external API
// state, it only stops the legacy token from ever seeding it again. Callers
// have already confirmed the row exists and the marker is unset (finding:
// legacy token cutover, P1 no-resurrection-after-disable).
//
// It is a no-op once the marker is already set (it returns before writing),
// so startups with the deployment pin present — which consume the marker on
// every boot — do not rewrite the row and do not reset updated_at.
func consumeLegacyTokenMarkerOnObservation(database *sql.DB, reason string) error {
	var m4bBinary, inputDir, outputDir, completedDir, outputScheme, region, apiKey, baseURL, created string
	var numCPUs, apiEnabled int
	var digest string
	var marker int
	if err := database.QueryRow(`
		SELECT m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, audiobookdb_api_key, audiobookdb_base_url,
		       api_enabled, api_key_digest, COALESCE(legacy_token_migrated, 0), created_at
		FROM settings WHERE id = 1
	`).Scan(&m4bBinary, &inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme, &region, &apiKey, &baseURL, &apiEnabled, &digest, &marker, &created); err != nil {
		return err
	}
	if marker != 0 {
		return nil // already consumed: no write, no timestamp churn
	}
	_, err := database.Exec(`
		INSERT OR REPLACE INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, api_enabled, api_key_digest, legacy_token_migrated, created_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, datetime('now'))
	`,
		apiKey,
		baseURL,
		m4bBinary,
		inputDir,
		outputDir,
		completedDir,
		numCPUs,
		outputScheme,
		region,
		apiEnabled,
		digest,
		created,
	)
	if err != nil {
		return err
	}
	log.Printf("Legacy token observation mark set: BRAGIBOOKS_ADMIN_TOKEN is ignored because %s — it can no longer seed the external API key.", reason)
	return nil
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

	var m4bBinary, inputDir, outputDir, completedDir, outputScheme, region, apiKey, baseURL, endpointKeyDigest string
	var numCPUs int
	var endpointEnabled int

	err := database.QueryRow(`
		SELECT m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, audiobookdb_api_key, audiobookdb_base_url, api_enabled, api_key_digest
		FROM settings WHERE id = 1
	`).Scan(&m4bBinary, &inputDir, &outputDir, &completedDir, &numCPUs, &outputScheme, &region, &apiKey, &baseURL, &endpointEnabled, &endpointKeyDigest)
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
	// The enabled flag follows the usual YAML/env gate (a deployment pin
	// wins over a store save); the digest has no deployment source and
	// always comes from the DB row — otherwise the key would die after a
	// restart.
	if !yamlKeys["server.api_endpoint.enabled"] && os.Getenv("SERVER_API_ENDPOINT_ENABLED") == "" {
		cfg.Server.APIEndpoint.Enabled = endpointEnabled != 0
	}
	cfg.Server.APIEndpoint.KeyDigest = endpointKeyDigest
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
