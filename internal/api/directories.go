package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DirectoryEntry represents a single file or directory in the listing.
type DirectoryEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // "file" or "dir"
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"` // RFC 3339 timestamp
}

// DirectoryTreeEntry extends DirectoryEntry with recursive children.
type DirectoryTreeEntry struct {
	Name         string               `json:"name"`
	Path         string               `json:"path"`
	IsDirectory  bool                 `json:"is_directory"`
	Size         int64                `json:"size"`
	Children     []DirectoryTreeEntry `json:"children,omitempty"`
	CycleSkipped bool                 `json:"cycle_skipped,omitempty"`
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

// GetDirectoryTreeResponse is the JSON shape returned by GET /api/directories/tree.
type GetDirectoryTreeResponse struct {
	Path    string               `json:"path"`
	Entries []DirectoryTreeEntry `json:"entries"`
}

// GetDirectoryTree handles GET /api/directories/tree?path=/some/dir&max_depth=50.
func (h *Handler) GetDirectoryTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	maxDepth := 50
	if s := r.URL.Query().Get("max_depth"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxDepth = n
		}
	}

	entries := buildDirectoryTree(path, maxDepth, 0, nil)

	writeJSON(w, http.StatusOK, GetDirectoryTreeResponse{
		Path:    path,
		Entries: entries,
	})
}

// buildDirectoryTree recursively builds a tree of DirectoryTreeEntry nodes.
// visited tracks resolved (real) paths to prevent infinite loops from symlinks.
func buildDirectoryTree(dir string, maxDepth, currentDepth int, visited map[string]struct{}) []DirectoryTreeEntry {
	if currentDepth >= maxDepth {
		return nil
	}

	if visited == nil {
		visited = make(map[string]struct{})
	}

	// Resolve symlinks for cycle detection
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		if _, ok := visited[resolved]; ok {
			// Cycle detected — skip this directory
			return nil
		}
		visited[resolved] = struct{}{}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	result := make([]DirectoryTreeEntry, 0, len(entries))
	for _, e := range entries {
		// Skip hidden files and directories
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		fullPath := filepath.Join(dir, e.Name())
		isDir := e.IsDir()

		entry := DirectoryTreeEntry{
			Name:        e.Name(),
			Path:        fullPath,
			IsDirectory: isDir,
		}

		info, err := e.Info()
		if err == nil {
			entry.Size = info.Size()
		}

		if isDir {
			entry.Children = buildDirectoryTree(fullPath, maxDepth, currentDepth+1, visited)
		}

		result = append(result, entry)
	}

	// Sort: directories first, then files, alphabetically within each group
	sortTree(result)
	return result
}

// sortTree sorts directory tree entries: directories before files, alphabetical within.
func sortTree(entries []DirectoryTreeEntry) {
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].IsDirectory && !entries[j].IsDirectory {
				entries[i], entries[j] = entries[j], entries[i]
			} else if entries[i].IsDirectory == entries[j].IsDirectory {
				if entries[i].Name > entries[j].Name {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
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
			entry.ModTime = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
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

// sortDir sorts directories before files, alphabetically within each group.
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
