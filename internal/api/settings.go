package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/version"
)

// cryptoRand is the key-entropy source; a variable (not the package) so a
// test can fault it for the generateAPIKey error path.
var cryptoRand = rand.Reader

// GetSettings handles GET /api/settings.
// Returns the current settings without the API key.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := h.svc.Config.Config()

	// The API key digest is never exposed by any read path.
	settings := map[string]any{
		"m4b_merge_binary":         cfg.M4bMerge.Binary,
		"input_dir":                cfg.Directories.InputDir,
		"output_dir":               cfg.Directories.OutputDir,
		"completed_dir":            cfg.Directories.CompletedDir,
		"num_cpus":                 cfg.Processing.NumCPUs,
		"output_scheme":            cfg.Processing.PathFormat,
		"region":                   cfg.Processing.Region,
		"audiobookdb_base_url":     cfg.APIKey.BaseURL,
		"api_endpoint_enabled":     cfg.Server.APIEndpoint.Enabled,
		"indexing_enabled":         cfg.Directories.IndexingEnabled,
		"api_endpoint_env_managed": h.svc.Config.APIEndpointEnvManaged(),
		"bragibooks_version":       version.Version,
		"m4b_merge_version":        h.svc.Processor.ReadVersion(),
		// The nil (default all) vs explicit-list distinction must reach the
		// UI: null renders every checkbox checked (the default), [] renders
		// none checked (write nothing).
		"metadata_fields":   cfg.Processing.MetadataFields,
		"metadata_chapters": cfg.Processing.MetadataChapters,
		"metadata_fields_pinned": func() bool {
			f, _ := h.svc.Config.MetadataEnvManaged()
			return f
		}(),
		"metadata_chapters_pinned": func() bool {
			_, c := h.svc.Config.MetadataEnvManaged()
			return c
		}(),
		// Stored profiles the boot validation rejected (the server started
		// anyway so the Settings UI could repair them): the UI renders
		// these as a fixable list. Empty (not null) when the store is
		// clean, so the client can count on a list shape.
		"invalid_output_profiles": bootInvalidProfileList(),
	}

	writeJSON(w, http.StatusOK, settings)
}

// generateAPIKey mints a new external-API key: 32 random bytes, exposed to
// the client as "bk_<64 hex>", stored only as its SHA-256 digest.
func generateAPIKey() (raw string, digest string, err error) {
	var b [32]byte
	if _, err = cryptoRand.Read(b[:]); err != nil {
		return "", "", err
	}
	raw = "bk_" + hex.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// matchAPIKeyDigest reports whether presented is the key whose digest is
// stored (constant-time compare over the SHA-256 hex digest).
func matchAPIKeyDigest(presented, storedDigest string) bool {
	sum := sha256.Sum256([]byte(presented))
	presentedHex := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(presentedHex), []byte(storedDigest)) == 1
}

// envManagedGuard rejects enable/disable when the env var
// SERVER_API_ENDPOINT_ENABLED is set (deployment-managed state).
// Returns true (and has written the 403 response) when managed.
func (h *Handler) envManagedGuard(w http.ResponseWriter) bool {
	if h.svc.Config.APIEndpointEnvManaged() {
		writeError(w, http.StatusForbidden, "external API state is managed by the SERVER_API_ENDPOINT_ENABLED environment variable")
		return true
	}
	return false
}

// persistAPIEndpointState persists the external-API state (DB first, then
// the YAML flag, rolling the DB back on a file failure) and — only after
// both stores agree — swaps the live state. Returns the previous snapshot
// for rollback and (for enable/reissue) the minted raw key.
//
// It runs the whole read-modify-write under h.svc.settingsMu, the same
// lock UpdateSettings uses for the form save: both persist the entire
// settings row, so unserialized they can interleave snapshot-read and
// row-write and clobber each other (a form save landing after an enable
// would clear the freshly stored key digest, and the returned key would
// stop working after a restart).
//
// requireEnabled (regenerate only) is evaluated against the SAME locked
// snapshot prev that the write is built from: a concurrent disable that
// commits before this flow takes the lock leaves the snapshot disabled,
// so the regenerate fails with 400 instead of persisting enabled=true
// with a fresh key and reopening remote access after a successful
// disable (finding: regenerate precondition race).
func (h *Handler) persistAPIEndpointState(w http.ResponseWriter, enabled bool, keyDigest string, requireEnabled bool) bool {
	h.svc.settingsMu.Lock()
	defer h.svc.settingsMu.Unlock()

	// The pre-commit window (the SettingsPersistDelay test hook; nil in
	// production) is widened BEFORE the authoritative snapshot is taken:
	// the write must be built from the state as it exists when it
	// commits, so a concurrent disable that lands in this window leaves
	// the snapshot disabled (finding: regenerate precondition race). The
	// lock is held across the whole window, so no other flow can commit
	// in it.
	if h.svc.SettingsPersistDelay != nil {
		h.svc.SettingsPersistDelay()
	}

	prev := h.svc.Config.Config()
	if requireEnabled && !prev.Server.APIEndpoint.Enabled {
		writeError(w, http.StatusBadRequest, "enable the external API first")
		return false
	}
	// Full-row write: run the same pin-freeze UpdateSettings applies, so a
	// re-asserted metadata env pin never rides into the durable row
	// (forward write AND the DB rollback below).
	snap := *prev
	if ferr := h.freezePinnedMetadata(&snap); ferr != nil {
		writeError(w, http.StatusInternalServerError, ferr.Error())
		return false
	}
	snap.Server.APIEndpoint = config.APIEndpointConfig{
		Enabled:   enabled,
		KeyDigest: keyDigest,
	}

	// 1. DB first — the settings row is the authoritative store (it is the
	//    only source a restart reads when no deployment pin exists).
	if err := h.saveSettingsToDB(&snap); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("persist external API state to DB: %v", err))
		return false
	}

	// 2. File second: the enabled flag only (the digest has no YAML path by
	//    tag). The write ALWAYS runs, including from the regenerate path,
	//    which is allowed while SERVER_API_ENDPOINT_ENABLED pins the flag:
	//    while the pin is present it shadows this file value (env wins at
	//    load, and the value written equals the pin — enable/disable are
	//    the only flows the env guard blocks), and once the pin is removed
	//    the file flag matches the pin that governed the write.
	if err := h.svc.Config.SaveKeys(map[string]any{"server": map[string]any{"api_endpoint": map[string]any{"enabled": enabled}}}); err != nil {
		rbSnap := *prev
		if ferr := h.freezePinnedMetadata(&rbSnap); ferr != nil {
			// Rollback lost: the pin is unreadable, so the row cannot be
			// restored without persisting the pin. Report both failures.
			log.Printf("Warning: external API state file save failed and DB rollback also failed (pin freeze: %v; file error: %v)", ferr, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("save external API state: %v", err))
			return false
		}
		if rbErr := h.saveSettingsToDB(&rbSnap); rbErr != nil {
			// Both stores now disagree and the rollback lost: report both,
			// keep the live process on the previous state (it and the DB
			// revert together on the next successful action), do NOT swap.
			log.Printf("Warning: external API state file save failed and DB rollback also failed: %v (file error: %v)", rbErr, err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("save external API state: %v", err))
			return false
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save external API state: %v", err))
		return false
	}

	// 3. Both stores persisted: swap the live state so the next request sees
	//    it without a restart.
	h.svc.Config.SetAPIEndpointState(enabled, keyDigest)
	h.svc.Config.AddYAMLKeys("server.api_endpoint.enabled")
	return true
}

// EnableAPIEndpoint enables the external API and mints a new key. The raw
// key is returned ONLY here — reveal-once by design.
func (h *Handler) EnableAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.envManagedGuard(w) {
		return
	}
	raw, digest, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate API key")
		return
	}
	if !h.persistAPIEndpointState(w, true, digest, false) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "api_key": raw})
}

// DisableAPIEndpoint disables the external API and wipes the stored key.
func (h *Handler) DisableAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.envManagedGuard(w) {
		return
	}
	if !h.persistAPIEndpointState(w, false, "", false) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// RegenerateAPIEndpoint mints a fresh key while the external API is
// enabled, invalidating the previous one. Allowed in env-managed mode
// (only the enabled flag is deployment-managed, not the key).
//
// The "enabled" precondition is enforced INSIDE settingsMu, against the
// same locked snapshot the row write is built from (a fast pre-check
// here skips the key mint when it is already known to be disabled; the
// locked check is the authoritative one — a disable that commits first
// makes the regenerate fail with 400, never re-enable, finding:
// regenerate precondition race).
func (h *Handler) RegenerateAPIEndpoint(w http.ResponseWriter, r *http.Request) {
	if !h.svc.Config.Config().Server.APIEndpoint.Enabled {
		writeError(w, http.StatusBadRequest, "enable the external API first")
		return
	}
	raw, digest, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate API key")
		return
	}
	if !h.persistAPIEndpointState(w, true, digest, true) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "api_key": raw})
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
	IndexingEnabled    *bool   `json:"indexing_enabled"`
	// MetadataFields is the per-field embedded-metadata write selection
	// (#518): a present array (possibly empty = write nothing) is the
	// explicit selection; an ABSENT field leaves the saved selection
	// untouched (standard partial-update semantics — the default
	// all-supported-available behavior is the stored zero value, not a
	// request-time reset). The values must name supported fields
	// (validated below).
	MetadataFields *[]string `json:"metadata_fields"`
	// MetadataChapters is the chapter write policy: "write" (default;
	// embed the release's chapters + sidecar) or "preserve" (no chapter
	// mutation; single natively-copyable input only).
	MetadataChapters *string `json:"metadata_chapters"`
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
	if req.OutputScheme != nil {
		// The same relative-path rules the boot check and enqueue apply:
		// a value the engine cannot resolve inside the output root must
		// fail HERE (400), not make the next startup fatal. When this
		// request also swaps the engine binary, the token vocabulary is
		// validated against the CANDIDATE's registry in the swap branch
		// below, so the pre-check covers only the engine-independent
		// structure (a candidate-specific token must not 400 before the
		// candidate is probed).
		swapping := req.M4bMergeBinary != nil && *req.M4bMergeBinary != h.svc.Config.Config().M4bMerge.Binary
		var verr error
		if swapping {
			verr = audio.ValidateRelativePathStructure(*req.OutputScheme)
		} else {
			verr = audio.ValidateRelativePathFormat(*req.OutputScheme)
		}
		if verr != nil {
			writeError(w, http.StatusBadRequest, "output scheme: "+verr.Error())
			return
		}
	}
	// SSRF guard (finding: base URL policy) — the proxy target must be a
	// trusted origin; anything else can exfiltrate the API key.
	if req.AudiobookdbBaseURL != nil && *req.AudiobookdbBaseURL != "" {
		if err := validateAudiobookdbBaseURL(*req.AudiobookdbBaseURL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// A deployment pin (PROCESSING_METADATA_FIELDS /
	// PROCESSING_METADATA_CHAPTERS) outranks the settings stores: it stays
	// authoritative for jobs while present (applyEnvPriority re-asserts it),
	// so a save of the pinned value must not be persisted as if it had
	// taken effect. The pinned key is dropped from this save BEFORE
	// validation, so a stale client that posts a malformed value for the
	// pinned key cannot 400 unrelated settings in the same PUT; GET
	// reports the pin so the UI renders those controls read-only. Same
	// env-managed contract as the API endpoint.
	fieldsPinned, chaptersPinned := h.svc.Config.MetadataEnvManaged()
	if fieldsPinned {
		req.MetadataFields = nil
	}
	if chaptersPinned {
		req.MetadataChapters = nil
	}
	// Per-field write selection (#518): a PRESENT (unpinned) value is
	// validated — an unknown field name is a 400 naming it, since the UI
	// derives its checkbox list from the verified engine capabilities and
	// a stale client that sends a dropped field must fail loudly. An
	// ABSENT value leaves the saved selection untouched (partial-update
	// semantics). The chapter policy accepts only the two Bragi-derived
	// values (write/preserve).
	if req.MetadataFields != nil {
		if _, err := audio.NormalizeSelectableFields(*req.MetadataFields); err != nil {
			writeError(w, http.StatusBadRequest, "metadata fields: "+err.Error())
			return
		}
	}
	if req.MetadataChapters != nil {
		c := strings.TrimSpace(*req.MetadataChapters)
		if c != audio.ChaptersPolicyValueWrite && c != audio.ChaptersPolicyValuePreserve {
			writeError(w, http.StatusBadRequest, "chapter write policy must be \"write\" or \"preserve\"")
			return
		}
	}

	// Directories the server creates subdirectories in are probed before
	// persisting, but only when this request CHANGES the value: the stored
	// value is not re-probed, so a mount that went away cannot block
	// unrelated saves (including the save that clears or fixes it), while a
	// newly submitted path the server cannot write is rejected here instead
	// of failing 0s into a job. Blank completed_dir clears the setting.
	// probedOutputDir/probedCompletedDir are the trimmed, tilde-expanded
	// values the server actually validated; persisting them (rather than
	// the raw request values) keeps the stored path equal to the probed one.
	var probedOutputDir, probedCompletedDir string
	if req.OutputDir != nil {
		v, err := probeChangedDir("output directory", *req.OutputDir, h.svc.Config.Config().Directories.OutputDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		probedOutputDir = v
	}
	if req.CompletedDir != nil {
		v, err := probeChangedDir("completed directory", *req.CompletedDir, h.svc.Config.Config().Directories.CompletedDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		probedCompletedDir = v
	}

	// Serialize with the external-API state flows (same full-row
	// read-modify-write; see persistAPIEndpointState). The lock covers
	// snapshot-read through row-write, so a concurrent save cannot
	// interleave its row write with this one.
	h.svc.settingsMu.Lock()
	defer h.svc.settingsMu.Unlock()

	prev := h.svc.Config.Config()
	snap := *prev
	// Every FULL-ROW settings write — the form save, the external-API
	// state flow, and every DB rollback below — must run the same
	// pin-freeze before it touches the stores, or the re-asserted env
	// pin rides into the durable row and stops looking like a pin once
	// it is removed.
	if ferr := h.freezePinnedMetadata(&snap); ferr != nil {
		writeError(w, http.StatusInternalServerError, ferr.Error())
		return
	}
	changed := make(map[string]any)
	if h.svc.SettingsPersistDelay != nil {
		h.svc.SettingsPersistDelay()
	}

	if req.AudiobookdbAPIKey != nil {
		snap.APIKey.APIKey = *req.AudiobookdbAPIKey
		changed["api_key"] = mergeChangedMap(changed, "api_key", "api_key", *req.AudiobookdbAPIKey)
	}
	if req.AudiobookdbBaseURL != nil {
		snap.APIKey.BaseURL = *req.AudiobookdbBaseURL
		changed["api_key"] = mergeChangedMap(changed, "api_key", "base_url", *req.AudiobookdbBaseURL)
	}
	var newBinaryCapsChecked *audio.Capabilities
	if req.M4bMergeBinary != nil {
		if *req.M4bMergeBinary != prev.M4bMerge.Binary {
			// A re-pointed engine binary is gated BEFORE the save is
			// accepted: a binary the boot gate would refuse (missing,
			// too old, no protocol capabilities) must not become the live
			// engine and start failing jobs. The gate probes the exact
			// path requested, so a relative path resolves against the
			// server's cwd the same way the launch will. The verified
			// capabilities are retained: step 3 publishes them together
			// with the binary swap (no second probe after the swap).
			caps, err := h.svc.Processor.CheckCapabilitiesFor(r.Context(), *req.M4bMergeBinary)
			if err != nil {
				writeError(w, http.StatusBadRequest, "engine check failed for "+*req.M4bMergeBinary+": "+err.Error())
				return
			}
			newBinaryCapsChecked = caps
			// The swap is not complete until EVERY effective scheme is
			// revalidated against the CANDIDATE's token set: the global
			// output scheme and each saved output-profile scheme were
			// validated against the CURRENT engine, and a scheme the old
			// engine accepted that the new one rejects must refuse the
			// save — not become a run-time failure for every job that
			// resolves through it. Validation runs BEFORE req.OutputScheme
			// is merged into snap below, so the effective global scheme
			// is the request's own value when it supplies one (it already
			// passed the candidate-independent structural rules above),
			// falling back to snap's persisted value.
			candReg := make(map[string]struct{}, len(caps.PathTokens))
			for _, t := range caps.PathTokens {
				candReg[t.Name] = struct{}{}
			}
			effectiveScheme := snap.Processing.PathFormat
			if req.OutputScheme != nil {
				effectiveScheme = *req.OutputScheme
			}
			if effectiveScheme != "" {
				if verr := audio.ValidatePathFormatForRegistry(effectiveScheme, candReg); verr != nil {
					writeError(w, http.StatusBadRequest, "output scheme: "+verr.Error())
					return
				}
			}
			prows, perr := h.svc.DB.Query("SELECT name, output_scheme FROM output_profiles WHERE output_scheme != ''")
			if perr != nil {
				writeError(w, http.StatusInternalServerError, "list output profiles: "+perr.Error())
				return
			}
			for prows.Next() {
				var name, scheme string
				if sErr := prows.Scan(&name, &scheme); sErr != nil {
					prows.Close()
					writeError(w, http.StatusInternalServerError, "list output profiles: "+sErr.Error())
					return
				}
				if verr := audio.ValidatePathFormatForRegistry(scheme, candReg); verr != nil {
					prows.Close()
					writeError(w, http.StatusBadRequest, fmt.Sprintf("output profile %q scheme is not valid for the new engine: %v", name, verr))
					return
				}
			}
			if cerr := prows.Close(); cerr != nil {
				writeError(w, http.StatusInternalServerError, "list output profiles: "+cerr.Error())
				return
			}
			if rowsErr := prows.Err(); rowsErr != nil {
				writeError(w, http.StatusInternalServerError, "list output profiles: "+rowsErr.Error())
				return
			}
		}
		snap.M4bMerge.Binary = *req.M4bMergeBinary
		changed["m4b_merge"] = mergeChangedMap(changed, "m4b_merge", "binary", *req.M4bMergeBinary)
	}
	if req.InputDir != nil {
		snap.Directories.InputDir = *req.InputDir
		changed["directories"] = mergeChangedMap(changed, "directories", "input_dir", *req.InputDir)
	}
	if req.OutputDir != nil {
		// Persist the probed (trimmed) value, not the raw request value.
		snap.Directories.OutputDir = probedOutputDir
		changed["directories"] = mergeChangedMap(changed, "directories", "output_dir", probedOutputDir)
	}
	if req.CompletedDir != nil {
		// Persist the probed (trimmed) value, not the raw request value.
		snap.Directories.CompletedDir = probedCompletedDir
		changed["directories"] = mergeChangedMap(changed, "directories", "completed_dir", probedCompletedDir)
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
	if req.IndexingEnabled != nil {
		snap.Directories.IndexingEnabled = *req.IndexingEnabled
		changed["directories"] = mergeChangedMap(changed, "directories", "indexing_enabled", *req.IndexingEnabled)
	}
	if req.MetadataFields != nil {
		// Store the normalized (trimmed, de-duplicated) list; nil would
		// mean "reset to default" but a present array always yields a
		// non-nil normalized value (possibly empty = write nothing).
		norm, _ := audio.NormalizeSelectableFields(*req.MetadataFields)
		snap.Processing.MetadataFields = &norm
		changed["processing"] = mergeChangedMap(changed, "processing", "metadata_fields", norm)
	}
	if req.MetadataChapters != nil {
		snap.Processing.MetadataChapters = strings.TrimSpace(*req.MetadataChapters)
		changed["processing"] = mergeChangedMap(changed, "processing", "metadata_chapters", snap.Processing.MetadataChapters)
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
			// Full-row write: the rollback runs the same pin-freeze as
			// the forward write, so the restored row cannot carry the
			// re-asserted pin.
			rbSnap := *prev
			if ferr := h.freezePinnedMetadata(&rbSnap); ferr != nil {
				log.Printf("Warning: settings file save failed and DB rollback also failed (pin freeze: %v)", ferr)
			} else if rbErr := h.saveSettingsToDB(&rbSnap); rbErr != nil {
				log.Printf("Warning: settings file save failed and DB rollback also failed: %v", rbErr)
			}
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
			return
		}
	}

	// 3. Both stores persisted: swap in the new immutable live config
	//    (directory ~ expanded so the live value is absolute) and refresh
	//    the live services (finding: live settings apply).
	//
	// newBinaryCaps is the verified capabilities of a re-pointed engine
	// binary, captured by the pre-save gate above. It is published TOGETHER
	// with the binary swap below: re-probing after ApplyConfig would leave a
	// window where jobs run the new binary against the previous binary's
	// capabilities and token registry, and a transient probe failure there
	// would only be logged while the stale state kept serving. The pre-save
	// result is the same probe, already succeeded, so nothing is lost.
	var newBinaryCaps *audio.Capabilities
	if req.M4bMergeBinary != nil && *req.M4bMergeBinary != prev.M4bMerge.Binary {
		newBinaryCaps = newBinaryCapsChecked
	}
	live := snap
	live.Directories.InputDir = expandTilde(live.Directories.InputDir)
	live.Directories.OutputDir = expandTilde(live.Directories.OutputDir)
	live.Directories.CompletedDir = expandTilde(live.Directories.CompletedDir)
	// A settings save stores a freshly merged config; without this the
	// active PROCESSING_METADATA_* pins would vanish from the live state
	// for the rest of the process's lifetime (jobs would then run the
	// saved value, not the deployment pin).
	config.ReassertMetadataPins(&live)
	h.svc.Config.Store(&live)
	for _, k := range changedDottedKeys(changed) {
		h.svc.Config.AddYAMLKeys(k)
	}
	// A swapped engine binary means a new protocol surface: the
	// capabilities stored for the previous binary are stale, and the
	// Settings picker + every save-time format validation render from
	// them. Publish the pre-save-verified document and the token registry
	// BEFORE the binary swap below: a reader then pairs either the complete
	// old state (old binary + old capabilities) or the new capabilities
	// with the old binary — never the new binary with the previous
	// binary's capabilities. The latter pairing would record a stale
	// engine identity at queue time that the new binary's run cannot match;
	// the former fails closed and recovers by re-queue: a job captured
	// under the old identity that runs on the new binary trips the
	// identity check, errors with a re-preview/re-queue instruction, and
	// the re-queued job captures the new identity.
	if newBinaryCaps != nil {
		h.svc.Processor.StoreCapabilities(newBinaryCaps)
		tokenNames := make([]string, 0, len(newBinaryCaps.PathTokens))
		for _, t := range newBinaryCaps.PathTokens {
			tokenNames = append(tokenNames, t.Name)
		}
		audio.SetPathFormatTokens(tokenNames)
	}
	// The mutable settings AND the write selection refresh under ONE
	// processor lock: the save changes both, and a queue snapshot taken
	// between two separate applies would freeze a mixed generation into
	// the job args. Queued jobs keep their frozen policy; new jobs pick
	// up the complete saved state.
	h.svc.Processor.ApplyConfigAndSelection(h.svc.Config.Config())
	h.svc.AudiobookDB.Store(audiobookdb.NewClient(live.APIKey.APIKey, live.APIKey.BaseURL))

	// Scan trigger: turning the index on, or re-pointing the input root
	// while it is on, schedules a (re)scan so the index tracks the new
	// root. When DIRECTORIES_INDEXING_ENABLED forces the flag on at boot,
	// the "turning on" transition never happens here (the flag was
	// already on) — the boot scan covers that case. Non-blocking; the
	// caller polls GET /api/index.
	if h.svc.IndexSvc != nil {
		enabledOn := !prev.Directories.IndexingEnabled && snap.Directories.IndexingEnabled
		rootMoved := snap.Directories.IndexingEnabled &&
			canonicalRoot(prev.Directories.InputDir) != canonicalRoot(snap.Directories.InputDir)
		if enabledOn || rootMoved {
			h.svc.IndexSvc.RequestScan()
		}
	}

	// Re-snapshot the m4b-merge version and pair its result with the live
	// binary in the response: a second read here could observe the value
	// stored by an overlapping update.
	m4bMergeVersion := h.svc.Processor.UpdateVersion()

	settings := map[string]any{
		"m4b_merge_binary": live.M4bMerge.Binary,
		// Included so the client's draft-vs-saved tracking (the index
		// status row appears only once the preference is saved) reads the
		// saved truth from this response, matching GET's shape.
		"indexing_enabled":         live.Directories.IndexingEnabled,
		"input_dir":                live.Directories.InputDir,
		"output_dir":               live.Directories.OutputDir,
		"completed_dir":            live.Directories.CompletedDir,
		"num_cpus":                 live.Processing.NumCPUs,
		"output_scheme":            live.Processing.PathFormat,
		"region":                   live.Processing.Region,
		"audiobookdb_base_url":     live.APIKey.BaseURL,
		"bragibooks_version":       version.Version,
		"m4b_merge_version":        m4bMergeVersion,
		"metadata_fields":          live.Processing.MetadataFields,
		"metadata_chapters":        live.Processing.MetadataChapters,
		"metadata_fields_pinned":   fieldsPinned,
		"metadata_chapters_pinned": chaptersPinned,
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

// freezePinnedMetadata applies the durable-value freeze to a settings copy
// before it is written to ANY full-row store (the form save, the
// external-API state flow, and every DB rollback): after an earlier save,
// the live config carries the re-asserted env values, and letting them ride
// along would leak the pin into the durable stores (env must outrank, never
// overwrite, the settings stores — a pinned operator who later edits an
// unrelated setting would otherwise persist the pin as if it were a user
// selection). The durable file is read, not the live config, because the
// live config is exactly the contaminated copy.
//
// The freeze, per pinned key, writes back the DURABLE value so the env pin
// never rides into a freshly written row. The durable value for a key
// follows the documented startup precedence — the file is primary, the
// settings row is the fallback the file falls back to:
//
//   - file carries a VALID value for the key (present + OK) -> the file is
//     authoritative; freeze its value.
//   - file is SILENT on the key (absent: not present, and absent counts as
//     OK so it is not treated as unreadable) -> the row is where the
//     effective value comes from; freeze the row, so a DB-only selection
//     survives an unrelated save. A MISSING row is not corruption: with
//     nothing stored, the documented default (nil fields / "write"
//     chapters) IS the durable value, and the ordinary first save
//     proceeds.
//   - file carries a MALFORMED value (present, not OK) -> the typed loader
//     discarded it, so the row (the documented DB fallback) is the truth;
//     freeze the row.
//
// Presence is tracked PER KEY: a file that sets only metadata_chapters is
// silent on metadata_fields and vice versa.
//
// Abort conditions (per key, for the pinned keys):
//   - the row read fails (a real query error) -> the fallback is unknown,
//     and writing would replace the durable selection with the default or
//     persist the pin into a fresh row;
//   - the file MANAGES the key (present, MALFORMED value: the typed loader
//     discarded it, so the file said something it will not honor) and no
//     settings row exists -> there is no durable row to fall back to, and
//     a first save would persist the pin (or default-overwrite the file's
//     intent).
//
// A MISSING row with the file SILENT on the key is NOT an abort: with
// nothing stored and no file intent, the documented default IS the durable
// value and the ordinary first save proceeds. A corrupt COLUMN of an
// existing row is not an abort either: the full-row save writes the live
// (default) value for that column, which REPAIRS the row to the documented
// default, while the sibling's valid stored value is preserved.
func (h *Handler) freezePinnedMetadata(snap *config.Config) error {
	f, c := h.svc.Config.MetadataEnvManaged()
	if !f && !c {
		return nil
	}
	storedFields, storedChapters, fieldsOK, chaptersOK := h.svc.Config.StoredMetadataSelection()
	fileFieldsPresent, fileChaptersPresent := h.svc.Config.StoredMetadataKeyPresent()
	fieldsCorrupt := f && !fieldsOK && fileFieldsPresent
	chaptersCorrupt := c && !chaptersOK && fileChaptersPresent
	needRow := fieldsCorrupt || chaptersCorrupt ||
		(f && !fileFieldsPresent) || (c && !fileChaptersPresent)
	var rowFields *[]string
	rowChapters := "" // '' = the documented default (write)
	if needRow {
		// The reader returns the VALID sibling column and drops only the
		// corrupt one (a corrupt column falls back to the documented
		// default above), so a corrupt fields column keeps a good
		// chapters value and vice versa. queryOK is false ONLY on a real
		// query failure; a missing row and a corrupt column are both
		// readable states (rowExists is false only when there is no row).
		var fieldsValid, chaptersValid, queryOK bool
		rowFields, rowChapters, fieldsValid, chaptersValid, queryOK = h.storedMetadataRow()
		rowExists := queryOK && (fieldsValid || chaptersValid)
		if !queryOK {
			// The row is the fallback for a pinned key and it cannot be
			// read: writing would replace the durable selection with the
			// default (or persist the pin into a fresh row). Abort
			// instead of guessing.
			return fmt.Errorf("cannot save while a metadata write pin is set: the stored selection is unreadable (settings row query failed); resolve the failure, then retry")
		}
		if !rowExists && (fieldsCorrupt || chaptersCorrupt) {
			// The file manages a pinned key with a value the loader will
			// not honor, and there is no durable row to fall back to: a
			// first save would persist the pin (or default-overwrite the
			// file's intent). Abort before any store write.
			return fmt.Errorf("cannot save while a metadata write pin is set: the stored selection is unreadable (malformed config value and no stored row); resolve the config, then retry")
		}
	}
	if f {
		switch {
		case !fieldsOK || !fileFieldsPresent:
			snap.Processing.MetadataFields = rowFields
		default:
			snap.Processing.MetadataFields = storedFields
		}
	}
	if c {
		switch {
		case !chaptersOK || !fileChaptersPresent:
			snap.Processing.MetadataChapters = rowChapters
		default:
			snap.Processing.MetadataChapters = storedChapters
		}
	}
	return nil
}

// storedMetadataRow reads the settings row's metadata selection columns
// with PER-COLUMN validity, decoding them the way saveSettingsToDB writes
// them (fields: JSON array text, ” = unset default; chapters: policy
// string, ” = default write). fieldsValid/chaptersValid report that each
// column holds a valid value — an EMPTY column IS valid (the documented
// default for that key) — so a corrupt value in one column does not
// discard a valid value in the other (fields must be a JSON ARRAY — `null`
// unmarshals to a nil slice without error and is corrupt; chapters must be
// a present non-empty value).
// queryOK is false only on a real query failure (it is true for a missing
// row and for a read row); a missing row is a valid empty state — with
// nothing stored the documented default is the durable value, while a
// query failure is corruption and the caller must abort. With a read row,
// a nil fields pointer is the UNSET default; a present empty list is the
// none-selected save.
func (h *Handler) storedMetadataRow() (fields *[]string, chapters string, fieldsValid, chaptersValid bool, queryOK bool) {
	var fieldsCol, chaptersCol string
	err := h.svc.DB.QueryRow(
		`SELECT COALESCE(metadata_fields, ''), COALESCE(metadata_chapters, '') FROM settings WHERE id = 1`,
	).Scan(&fieldsCol, &chaptersCol)
	if err != nil {
		if err != sql.ErrNoRows {
			// A real query failure: not trustworthy, and distinct from a
			// missing row (queryOK true) so the caller can treat it as
			// corruption, not "nothing stored".
			return nil, "", false, false, false
		}
		return nil, "", false, false, true // no row: a valid empty state
	}
	// An EMPTY column is the documented default for that key (fields: no
	// selection saved; chapters: write) — a VALID value, not corruption.
	// Only a PRESENT value that fails to parse is invalid.
	switch {
	case fieldsCol == "":
		fieldsValid = true
	default:
		var f []string
		if jerr := json.Unmarshal([]byte(fieldsCol), &f); jerr == nil && f != nil {
			fields = &f
			fieldsValid = true
		}
	}
	switch {
	case chaptersCol == "":
		chaptersValid = true
	default:
		chapters = chaptersCol
		chaptersValid = true
	}
	return fields, chapters, fieldsValid, chaptersValid, true
}

// saveSettingsToDB persists the config to the settings table in the database.
// Uses INSERT OR REPLACE to handle both initial setup and updates.
// Directory paths are stored in expanded (absolute) form.
func (h *Handler) saveSettingsToDB(cfg *config.Config) error {
	apiEnabled := 0
	if cfg.Server.APIEndpoint.Enabled {
		apiEnabled = 1
	}
	indexingEnabled := 0
	if cfg.Directories.IndexingEnabled {
		indexingEnabled = 1
	}
	// The whole row is replaced, so every column NOT written by this
	// config must be carried through: the legacy-token one-shot marker
	// (schema v6) — dropping it to 0 would let the startup migration
	// re-seed the digest after a user later disabled the API (finding:
	// legacy token cutover) — and indexing_enabled, whose DEFAULT 0
	// would silently disable the index on the next settings save
	// (finding: settings writers reset indexing_enabled).
	// The write selection (#518) round-trips as a JSON array; the EMPTY
	// string means "not set" (default all-supported-available) — the same
	// nil-vs-explicit distinction the Go config uses, so the column's
	// NOT NULL DEFAULT '' matches the zero config value.
	metadataFieldsCol := ""
	if cfg.Processing.MetadataFields != nil {
		if b, err := json.Marshal(*cfg.Processing.MetadataFields); err == nil {
			metadataFieldsCol = string(b)
		}
	}
	metadataChaptersCol := cfg.Processing.MetadataChapters
	if metadataChaptersCol == "" {
		metadataChaptersCol = "write"
	}
	var legacyMigrated int
	if err := h.svc.DB.QueryRow(
		`SELECT COALESCE(legacy_token_migrated, 0) FROM settings WHERE id = 1`,
	).Scan(&legacyMigrated); err != nil && err != sql.ErrNoRows {
		return err
	}
	_, err := h.svc.DB.Exec(`
		INSERT OR REPLACE INTO settings (id, audiobookdb_api_key, audiobookdb_base_url, m4b_merge_binary, input_dir, output_dir, completed_dir, num_cpus, output_scheme, region, api_enabled, api_key_digest, legacy_token_migrated, indexing_enabled, metadata_fields, metadata_chapters, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
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
		apiEnabled,
		cfg.Server.APIEndpoint.KeyDigest,
		legacyMigrated,
		indexingEnabled,
		metadataFieldsCol,
		metadataChaptersCol,
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

// probeChangedDir checks a submitted directory setting the server must write
// to, but only when it differs from the live (tilde-expanded) value. label
// names the settings field in the returned 400 message. An empty submitted
// value always passes: clearing an optional directory never needs access.
// It returns the probed (trimmed, tilde-expanded) value so the caller
// persists exactly what was validated — the raw request value may carry
// surrounding whitespace the probe stripped.
func probeChangedDir(label, submitted, live string) (string, error) {
	v := expandTilde(strings.TrimSpace(submitted))
	if v == "" || v == live {
		return v, nil
	}
	if err := audio.CheckDirWritable(v); err != nil {
		return "", fmt.Errorf("%s %w", label, err)
	}
	return v, nil
}
