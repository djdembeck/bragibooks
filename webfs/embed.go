//go:build webui

package webfs

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:build/*
var buildFS embed.FS

// Handler returns an HTTP handler that serves the embedded SvelteKit SPA.
// Static assets are served directly; client-side routes fall through to index.html.
// API paths are rejected so they fall through to the API router.
func Handler() http.Handler {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject API paths — let the API router handle them
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		// Strip the leading slash to get the file path inside the embedded FS
		filePath := path.Clean(r.URL.Path)
		if filePath == "/" {
			filePath = ""
		}

		// Check if the exact file exists in the embedded FS
		info, err := sub.Open(strings.TrimPrefix(filePath, "/"))
		if err == nil {
			info.Close()
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}

		// Fall through to index.html for client-side routing
		http.ServeFileFS(w, r, sub, "index.html")
	})
}
