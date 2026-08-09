package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
)

// GetSettings handles GET /api/settings.
// Returns the current settings without the API key.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := h.svc.Config.Config()

	settings := map[string]any{
		"m4b_merge_binary":     cfg.M4bMerge.Binary,
		"input_dir":            cfg.Directories.InputDir,
		"output_dir":           cfg.Directories.OutputDir,
		"completed_dir":        cfg.Directories.CompletedDir,
		"num_cpus":             cfg.Processing.NumCPUs,
		"output_scheme":        cfg.Processing.PathFormat,
		"region":               cfg.Processing.Region,
		"audiobookdb_base_url": cfg.APIKey.BaseURL,
	}

	writeJSON(w, http.StatusOK, settings)
}

// UpdateSettingsRequest is the JSON body accepted by PUT /api/settings.
type UpdateSettingsRequest struct {
	AudiobookdbAPIKey  *string `json:"audiobookdb_api_key"`
	AudiobookdbBaseURL *string `json:"audiobookdb_base_url"`
	M4bMergeBinary     *string `json:"m4b_merge_binary"`
	InputDir           *string `json:"input_dir"`
	OutputDir          *string `json:"output_dir"`
	CompletedDir       *string `json:"completed_dir"`
	NumCPUs            *int    `json:"num_cpus"`
	OutputScheme       *string `json:"output_scheme"`
	Region             *string `json:"region"`
}

// UpdateSettings handles PUT /api/settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req UpdateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate required fields before applying
	if req.InputDir != nil && strings.TrimSpace(*req.InputDir) == "" {
		writeError(w, http.StatusBadRequest, "input directory must not be empty")
		return
	}
	if req.OutputDir != nil && strings.TrimSpace(*req.OutputDir) == "" {
		writeError(w, http.StatusBadRequest, "output directory must not be empty")
		return
	}
	if req.NumCPUs != nil && *req.NumCPUs < 0 {
		writeError(w, http.StatusBadRequest, "CPU count must be zero or greater")
		return
	}
	if req.AudiobookdbBaseURL != nil && *req.AudiobookdbBaseURL != "" {
		if _, err := parseURL(*req.AudiobookdbBaseURL); err != nil {
			writeError(w, http.StatusBadRequest, "audiobookdb base URL must be a valid URL (e.g. https://audiobookdb.org/api)")
			return
		}
	}

	// Lock the config manager for atomic update
	h.svc.Config.Lock()
	defer h.svc.Config.Unlock()

	cfg := h.svc.Config.Config()
	snapshot := h.svc.Config.Snapshot()

	// Apply updates
	if req.AudiobookdbAPIKey != nil {
		cfg.APIKey.APIKey = *req.AudiobookdbAPIKey
	}
	if req.AudiobookdbBaseURL != nil {
		cfg.APIKey.BaseURL = *req.AudiobookdbBaseURL
	}
	if req.M4bMergeBinary != nil {
		cfg.M4bMerge.Binary = *req.M4bMergeBinary
	}
	if req.InputDir != nil {
		cfg.Directories.InputDir = *req.InputDir
	}
	if req.OutputDir != nil {
		cfg.Directories.OutputDir = *req.OutputDir
	}
	if req.CompletedDir != nil {
		cfg.Directories.CompletedDir = *req.CompletedDir
	}
	if req.NumCPUs != nil {
		cfg.Processing.NumCPUs = *req.NumCPUs
	}
	if req.OutputScheme != nil {
		cfg.Processing.PathFormat = *req.OutputScheme
	}
	if req.Region != nil {
		cfg.Processing.Region = *req.Region
	}

	// Save the updated config
	if err := h.svc.Config.Save(); err != nil {
		// Restore on failure
		h.svc.Config.Restore(snapshot)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	// Also persist settings in the database
	if err := h.saveSettingsToDB(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("persist settings to DB: %v", err))
		return
	}

	// Rebuild audiobookdb client with updated credentials
	newClient := audiobookdb.NewClient(cfg.APIKey.APIKey, cfg.APIKey.BaseURL)
	h.svc.AudiobookDB.Store(newClient)

	// Return updated settings (without API key)
	updatedCfg := h.svc.Config.Config()
	settings := map[string]any{
		"m4b_merge_binary":     updatedCfg.M4bMerge.Binary,
		"input_dir":            updatedCfg.Directories.InputDir,
		"output_dir":           updatedCfg.Directories.OutputDir,
		"completed_dir":        updatedCfg.Directories.CompletedDir,
		"num_cpus":             updatedCfg.Processing.NumCPUs,
		"output_scheme":        updatedCfg.Processing.PathFormat,
		"region":               updatedCfg.Processing.Region,
		"audiobookdb_base_url": updatedCfg.APIKey.BaseURL,
	}

	writeJSON(w, http.StatusOK, settings)
}

// saveSettingsToDB persists the config to the settings table in the database.
// Uses INSERT OR REPLACE to handle both initial setup and updates.
func (h *Handler) saveSettingsToDB(cfg *config.Config) error {
	// Expand ~ in directory paths
	expandedInputDir := expandTilde(cfg.Directories.InputDir)
	expandedOutputDir := expandTilde(cfg.Directories.OutputDir)
	expandedCompletedDir := expandTilde(cfg.Directories.CompletedDir)

	_, err := h.svc.DB.Exec(`
		INSERT OR REPLACE INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`,
		cfg.APIKey.APIKey,
		cfg.APIKey.BaseURL,
		cfg.M4bMerge.Binary,
		expandedInputDir,
		expandedOutputDir,
		expandedCompletedDir,
		cfg.Processing.NumCPUs,
		cfg.Processing.PathFormat,
		cfg.Processing.Region,
	)
	return err
}

// parseURL validates an absolute HTTP(S) URL string.
func parseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("URL must be an absolute HTTP(S) URL")
	}
	return parsed, nil
}

// expandTilde expands ~ in a path to the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}
