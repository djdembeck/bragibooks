package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

var errProfileNotFound = errors.New("output profile not found")

// isProfileUniqueViolation reports whether a profile insert/update failed on
// the table's UNIQUE constraint via the modernc typed error (result code
// SQLITE_CONSTRAINT_UNIQUE), the authority even under concurrent creates.
// Unrelated constraints and non-sqlite errors map to 500, never 409.
func isProfileUniqueViolation(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// OutputProfilesResponse is the JSON shape returned by GET /output-profiles.
type OutputProfilesResponse struct {
	Profiles []models.OutputProfile `json:"profiles"`
}

// OutputProfileRequest is the JSON body accepted by POST/PUT
// /output-profiles. The whole object is authoritative on write.
type OutputProfileRequest struct {
	Name         string `json:"name"`
	OutputDir    string `json:"output_dir"`
	OutputScheme string `json:"output_scheme"`
}

// validateProfile applies the shared profile-write rules: trimmed required
// name; optional ABSOLUTE output_dir (a destination root; no existence
// requirement, no relative-format rules); optional output_scheme under the
// shared relative-path validator; and at least one of the two fields set.
// Returns the cleaned values plus a 400 message ("" when valid).
func validateProfile(name, outputDir, outputScheme string) (string, string, string, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", "", "name is required"
	}
	outputDir = strings.TrimSpace(outputDir)
	if outputDir != "" && !strings.HasPrefix(outputDir, "/") {
		return "", "", "", "output directory must be an absolute path"
	}
	outputScheme = strings.TrimSpace(outputScheme)
	if outputScheme != "" {
		if err := audio.ValidateRelativePathFormat(outputScheme); err != nil {
			return "", "", "", "output scheme: " + err.Error()
		}
	}
	if outputDir == "" && outputScheme == "" {
		return "", "", "", "set an output directory, a path format, or both"
	}
	return name, outputDir, outputScheme, ""
}

// getOutputProfile loads a profile by ID. A non-positive ID is a client
// error (400); a well-formed ID with no row is 404.
func (h *Handler) getOutputProfile(id int64) (*models.OutputProfile, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: %d", errProfileNotFound, id)
	}
	row := h.svc.DB.QueryRow(
		"SELECT id, name, output_dir, output_scheme, created_at, updated_at FROM output_profiles WHERE id = ?",
		id,
	)
	var p models.OutputProfile
	if err := row.Scan(&p.ID, &p.Name, &p.OutputDir, &p.OutputScheme, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, errProfileNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListOutputProfiles handles GET /api/output-profiles, sorted by name
// case-insensitively.
func (h *Handler) ListOutputProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.DB.Query(
		"SELECT id, name, output_dir, output_scheme, created_at, updated_at FROM output_profiles",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query output profiles: "+err.Error())
		return
	}
	defer rows.Close()

	profiles := make([]models.OutputProfile, 0)
	for rows.Next() {
		var p models.OutputProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.OutputDir, &p.OutputScheme, &p.CreatedAt, &p.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "read output profile: "+err.Error())
			return
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "read output profiles: "+err.Error())
		return
	}
	sort.Slice(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	writeJSON(w, http.StatusOK, OutputProfilesResponse{Profiles: profiles})
}

// CreateOutputProfile handles POST /api/output-profiles, saving the full
// object and returning the stored profile with its ID.
func (h *Handler) CreateOutputProfile(w http.ResponseWriter, r *http.Request) {
	var req OutputProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, dir, scheme, msg := validateProfile(req.Name, req.OutputDir, req.OutputScheme)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// validateProfile is deliberately pure; the write-access probe runs here
	// because it touches the filesystem. A destination the server cannot
	// create subdirectories in would otherwise be accepted and only fail at
	// job time. An empty dir inherits the global setting and is not probed.
	if err := audio.CheckDirWritable(dir); err != nil {
		writeError(w, http.StatusBadRequest, "output directory "+err.Error())
		return
	}

	res, err := h.svc.DB.Exec(
		"INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES (?, ?, ?)",
		name, dir, scheme,
	)
	if err != nil {
		if isProfileUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a profile with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "create output profile: "+err.Error())
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create output profile: "+err.Error())
		return
	}
	// Re-read so the response carries the DB-stamped created_at/updated_at
	// instead of zero values.
	profile, err := h.getOutputProfile(id)
	if err != nil {
		// The row was just inserted by this request; a read failure is a
		// server fault, not a client error.
		writeError(w, http.StatusInternalServerError, "read created profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

// UpdateOutputProfile handles PUT /api/output-profiles/:id, replacing the
// full object and refreshing updated_at. Unknown well-formed IDs are 404.
func (h *Handler) UpdateOutputProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileIDParam(chiURLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid profile id")
		return
	}

	var req OutputProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, dir, scheme, msg := validateProfile(req.Name, req.OutputDir, req.OutputScheme)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// Load the stored profile first: an unknown ID is a 404 even when the
	// body is valid, and the stored output_dir lets the write probe skip an
	// unchanged value so a mount that went away cannot block an unrelated
	// edit (renaming the profile) — the user must still be able to fix it.
	existing, err := h.getOutputProfile(id)
	if err != nil {
		if errors.Is(err, errProfileNotFound) {
			writeError(w, http.StatusNotFound, "output profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "load output profile: "+err.Error())
		return
	}
	if dir != existing.OutputDir {
		if err := audio.CheckDirWritable(dir); err != nil {
			writeError(w, http.StatusBadRequest, "output directory "+err.Error())
			return
		}
	}

	res, err := h.svc.DB.Exec(
		`UPDATE output_profiles SET name = ?, output_dir = ?, output_scheme = ?, updated_at = datetime('now') WHERE id = ?`,
		name, dir, scheme, id,
	)
	if err != nil {
		if isProfileUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a profile with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "update output profile: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "output profile not found")
		return
	}
	// Re-read so the response carries the refreshed updated_at and the
	// DB-stamped created_at.
	profile, err := h.getOutputProfile(id)
	if err != nil {
		// RowsAffected > 0 just above; a read failure is a server fault.
		writeError(w, http.StatusInternalServerError, "read updated profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// DeleteOutputProfile handles DELETE /api/output-profiles/:id. Jobs already
// queued remain valid: their args store a resolved snapshot, not a profile
// reference.
func (h *Handler) DeleteOutputProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileIDParam(chiURLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid profile id")
		return
	}
	res, err := h.svc.DB.Exec("DELETE FROM output_profiles WHERE id = ?", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete output profile: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "output profile not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseProfileIDParam parses a positive-integer profile ID; false on any
// malformed value, which callers map to 400.
func parseProfileIDParam(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
