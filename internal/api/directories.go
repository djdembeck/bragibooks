package api

import (
	"encoding/json"
	"log"
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
// Index is set ONLY in the index-mode filesystem fallback (a guarded or
// truncated root): the entries are filesystem-sourced, the state is the
// CURRENT root's — so a fresh page visit sees the just-finished scan's
// freshness instead of treating it as stale.
type ListDirectoriesResponse struct {
	Path    string           `json:"path"`
	Entries []DirectoryEntry `json:"entries"`
	Index   *IndexState      `json:"index,omitempty"` // same-root index state in the index-mode fallback
}

// ListDirectories handles GET /api/directories?path=/some/dir.
// With ?index=1 (and indexing enabled and the path at/below the input
// root) the listing is served from the source index with per-entry
// evidence; a fallback (stale root, guarded/truncated table, index read
// error) keeps the filesystem read and, for the SAME root, carries the
// index status in `index` (state without rows) so the client can keep
// the freshness/warning the indexed shape shows; any other case keeps
// the existing filesystem read and the existing response shape.
func (h *Handler) ListDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}

	// Index mode: the flag is present, the feature is on, and the
	// requested path equals the canonical root or is below it.
	// fallbackIndex marks a request that fell back to the filesystem for
	// a path at/below the current root: the response below then carries
	// the same-root index status (state, not rows). The root is captured
	// ONCE here — the same value that decides index-mode eligibility —
	// and reused by the fallback metadata gate below: a config change
	// mid-request must not let a filesystem listing served for the old
	// root carry the NEW root's status. The gate itself compares against
	// the SYMLINK-RESOLVED directory the fallback read below (resolved
	// AFTER the read) — the form scan() stores as IndexState.Root — so
	// the status binds to the tree actually served, not the configured
	// root captured before the read.
	var fallbackIndex bool
	root := canonicalRoot(h.svc.Config.Config().Directories.InputDir)
	if r.URL.Query().Has("index") && h.svc.IndexSvc != nil && h.svc.IndexSvc.Enabled() {
		cleanPath := canonicalRoot(path)
		if cleanPath == root || strings.HasPrefix(cleanPath, indexChildPrefix(root)) {
			entries, ok, err := h.listIndexedChildren(cleanPath)
			switch {
			case err != nil:
				// An index read failure falls back to the filesystem read
				// below — the index is a cache, the tree is the truth.
				log.Printf("index: list %s: %v (falling back to filesystem)", cleanPath, err)
				fallbackIndex = true
			case ok:
				state := h.svc.IndexSvc.Status().State
				resp := IndexedDirectoriesResponse{
					Path:    cleanPath,
					Entries: entries,
					Index:   &state,
				}
				writeJSON(w, http.StatusOK, resp)
				return
			default:
				// Guarded/truncated/stale table: filesystem entries, with
				// the same-root index status below.
				fallbackIndex = true
			}
		}
	}

	entries, err := readDirectory(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := ListDirectoriesResponse{
		Path:    path,
		Entries: entries,
	}
	// Index-mode fallback: serve the same-READ-root index status alongside
	// the filesystem entries (state, not rows — the entries stay
	// filesystem-sourced). A fresh page visit then sees the just-finished
	// scan's freshness (no spurious rescan queued for a cap-truncated
	// root) and the incomplete-index warning; a state from ANOTHER root
	// (a re-pointed input) is not served for this path. The gate compares
	// against the resolved directory the read above ACTUALLY opened
	// (EvalSymlinks of the path readDirectory used) — the form scan()
	// stores as IndexState.Root, so the gate compares like with like.
	// A symlink below the guarded root resolves to the tree the walk
	// stores as file-symlink rows (never descended), and a
	// symlink-configured root can retarget between the eligibility check
	// and this read; either way the served tree is the RESOLVED target's,
	// and the status must bind to it. The binding is CONTAINMENT, not
	// equality: a fallback read of a DESCENDANT of the indexed root
	// serves the indexed tree's subtree, so it carries the status too —
	// while a sibling that merely shares the root's string prefix is not
	// contained. The gate compares against the resolved directory, not
	// the configured root captured at the top, nor a re-resolved
	// CurrentRoot: the read happened in this request for this path, so a
	// mid-request config change cannot bind the NEW root's status to a
	// listing of the old root's tree. Path resolution runs ONLY here —
	// plain (non-?index) requests and index requests that did not fall
	// back pay zero path-resolution I/O.
	if fallbackIndex {
		state := h.svc.IndexSvc.Status().State
		if state.Root != "" && indexedRootContains(state.Root, resolvedRoot(path)) {
			resp.Index = &state
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
