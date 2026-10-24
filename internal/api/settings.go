package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
//
// Persistence order (finding: split-brain): the settings DB row is written
// FIRST (it is the durable settings store), then the YAML file. If the DB
// write fails, nothing was written. If the file write fails afterwards, the
// DB row is re-set from the previous config so the two stores can never
// diverge, and the error is returned. Only after both persist is the live
// config pointer swapped and the processor/clients refreshed.
//
// The YAML file is written PARTIALLY — only the keys explicitly updated by
// this request — so saving one setting does not freeze the others' env-var
// values (finding: partial YAML save).
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req UpdateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate before applying anything.
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
	// SSRF guard (finding: base URL policy) — the proxy target must be a
	// trusted origin; anything else can exfiltrate the API key.
	if req.AudiobookdbBaseURL != nil && *req.AudiobookdbBaseURL != "" {
		if err := validateAudiobookdbBaseURL(*req.AudiobookdbBaseURL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	prev := h.svc.Config.Config()
	snap := *prev
	changed := make(map[string]any)

	if req.AudiobookdbAPIKey != nil {
		snap.APIKey.APIKey = *req.AudiobookdbAPIKey
		changed["api_key"] = mergeChangedMap(changed, "api_key", "api_key", *req.AudiobookdbAPIKey)
	}
	if req.AudiobookdbBaseURL != nil {
		snap.APIKey.BaseURL = *req.AudiobookdbBaseURL
		changed["api_key"] = mergeChangedMap(changed, "api_key", "base_url", *req.AudiobookdbBaseURL)
	}
	if req.M4bMergeBinary != nil {
		snap.M4bMerge.Binary = *req.M4bMergeBinary
		changed["m4b_merge"] = mergeChangedMap(changed, "m4b_merge", "binary", *req.M4bMergeBinary)
	}
	if req.InputDir != nil {
		snap.Directories.InputDir = *req.InputDir
		changed["directories"] = mergeChangedMap(changed, "directories", "input_dir", *req.InputDir)
	}
	if req.OutputDir != nil {
		snap.Directories.OutputDir = *req.OutputDir
		changed["directories"] = mergeChangedMap(changed, "directories", "output_dir", *req.OutputDir)
	}
	if req.CompletedDir != nil {
		snap.Directories.CompletedDir = *req.CompletedDir
		changed["directories"] = mergeChangedMap(changed, "directories", "completed_dir", *req.CompletedDir)
	}
	if req.NumCPUs != nil {
		snap.Processing.NumCPUs = *req.NumCPUs
		changed["processing"] = mergeChangedMap(changed, "processing", "num_cpus", *req.NumCPUs)
	}
	if req.OutputScheme != nil {
		snap.Processing.PathFormat = *req.OutputScheme
		changed["processing"] = mergeChangedMap(changed, "processing", "path_format", *req.OutputScheme)
	}
	if req.Region != nil {
		snap.Processing.Region = *req.Region
		changed["processing"] = mergeChangedMap(changed, "processing", "region", *req.Region)
	}

	// 1. DB first.
	if err := h.saveSettingsToDB(&snap); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("persist settings to DB: %v", err))
		return
	}

	// 2. File second; on failure roll the DB row back to the previous state
	//    so DB and file agree again.
	if len(changed) > 0 {
		if err := h.svc.Config.SaveKeys(changed); err != nil {
			if rbErr := h.saveSettingsToDB(prev); rbErr != nil {
				log.Printf("Warning: settings file save failed and DB rollback also failed: %v", rbErr)
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
			return
		}
	}

	// 3. Both stores persisted: swap in the new immutable live config
	//    (directory ~ expanded so the live value is absolute) and refresh
	//    the live services (finding: live settings apply).
	live := snap
	live.Directories.InputDir = expandTilde(live.Directories.InputDir)
	live.Directories.OutputDir = expandTilde(live.Directories.OutputDir)
	live.Directories.CompletedDir = expandTilde(live.Directories.CompletedDir)
	h.svc.Config.Store(&live)
	for _, k := range changedDottedKeys(changed) {
		h.svc.Config.AddYAMLKeys(k)
	}
	h.svc.Processor.ApplyConfig(h.svc.Config.Config())
	h.svc.AudiobookDB.Store(audiobookdb.NewClient(live.APIKey.APIKey, live.APIKey.BaseURL))

	settings := map[string]any{
		"m4b_merge_binary":     live.M4bMerge.Binary,
		"input_dir":            live.Directories.InputDir,
		"output_dir":           live.Directories.OutputDir,
		"completed_dir":        live.Directories.CompletedDir,
		"num_cpus":             live.Processing.NumCPUs,
		"output_scheme":        live.Processing.PathFormat,
		"region":               live.Processing.Region,
		"audiobookdb_base_url": live.APIKey.BaseURL,
	}

	writeJSON(w, http.StatusOK, settings)
}

// mergeChangedMap sets sub inside the top-level section of a partial-save
// map, creating the section when absent, and returns it.
func mergeChangedMap(changed map[string]any, top string, sub string, value any) map[string]any {
	section, _ := changed[top].(map[string]any)
	if section == nil {
		section = make(map[string]any)
		changed[top] = section
	}
	section[sub] = value
	return section
}

// changedDottedKeys lists the dotted keys present in a partial-save map.
func changedDottedKeys(changed map[string]any) []string {
	var keys []string
	for top, section := range changed {
		if s, ok := section.(map[string]any); ok {
			for k := range s {
				keys = append(keys, top+"."+k)
			}
		} else {
			keys = append(keys, top)
		}
	}
	return keys
}

// validateAudiobookdbBaseURL enforces the trusted-origin policy for the
// audiobookdb proxy target (finding: SSRF). The policy — https only on
// audiobookdb.org and its subdomains, localhost/http dev targets only
// behind BRAGIBOOKS_ALLOW_LOCAL_ADB — lives in
// audiobookdb.TrustedBaseURL, shared with the client's request-time gate.
func validateAudiobookdbBaseURL(raw string) error {
	_, err := audiobookdb.TrustedBaseURL(raw)
	return err
}

// saveSettingsToDB persists the config to the settings table in the database.
// Uses INSERT OR REPLACE to handle both initial setup and updates.
// Directory paths are stored in expanded (absolute) form.
func (h *Handler) saveSettingsToDB(cfg *config.Config) error {
	_, err := h.svc.DB.Exec(`
		INSERT OR REPLACE INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`,
		cfg.APIKey.APIKey,
		cfg.APIKey.BaseURL,
		cfg.M4bMerge.Binary,
		expandTilde(cfg.Directories.InputDir),
		expandTilde(cfg.Directories.OutputDir),
		expandTilde(cfg.Directories.CompletedDir),
		cfg.Processing.NumCPUs,
		cfg.Processing.PathFormat,
		cfg.Processing.Region,
	)
	return err
}

// expandTilde expands a leading ~ in a path to the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
