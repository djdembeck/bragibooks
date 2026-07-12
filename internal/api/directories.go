package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DirectoryEntry represents a single file or directory in the listing.
type DirectoryEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "dir"
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ListDirectoriesResponse is the JSON shape returned by GET /api/directories.
type ListDirectoriesResponse struct {
	Path    string           `json:"path"`
	Entries []DirectoryEntry `json:"entries"`
}

// ListDirectories handles GET /api/directories?path=/some/dir.
func (h *Handler) ListDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	entries, err := readDirectory(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ListDirectoriesResponse{
		Path:    path,
		Entries: entries,
	})
}

// StreamDirectories handles GET /api/directories/stream?path=/some/dir.
// Outputs NDJSON with one JSON object per line per directory entry.
func (h *Handler) StreamDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")

	entries, err := readDirectory(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	enc := json.NewEncoder(w)
	for _, entry := range entries {
		if err := enc.Encode(entry); err != nil {
			return
		}
	}
}

// readDirectory reads the given path and returns a list of entries.
func readDirectory(path string) ([]DirectoryEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrInvalid
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	result := make([]DirectoryEntry, 0, len(entries))
	for _, e := range entries {
		// Skip hidden files and directories
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		entry := DirectoryEntry{
			Name: e.Name(),
			Path: filepath.Join(path, e.Name()),
		}

		info, err := e.Info()
		if err == nil {
			entry.Size = info.Size()
		}

		if e.IsDir() {
			entry.Type = "dir"
		} else {
			entry.Type = "file"
		}

		result = append(result, entry)
	}

	// Sort: directories first, then files, alphabetically within each group
	// (simple sort for now)
	_ = sortDir(result)

	return result, nil
}

// sortDir returns a function that sorts directories before files.
// This is used for stable ordering within the directory listing.
func sortDir(entries []DirectoryEntry) []DirectoryEntry {
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			// Directories come before files
			if entries[i].Type == "file" && entries[j].Type == "dir" {
				entries[i], entries[j] = entries[j], entries[i]
			} else if entries[i].Type == entries[j].Type {
				// Alphabetical within same type
				if entries[i].Name > entries[j].Name {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	}
	return entries
}

// unused import guard for fs
var _ fs.FileInfo