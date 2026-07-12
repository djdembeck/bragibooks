//go:build webui

package webfs

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:build/*
var Dist embed.FS

func FileServer() http.Handler {
	sub, err := fs.Sub(Dist, "build")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

var staticFileExtensions = []string{
	".js", ".css", ".map",
	".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico",
	".woff", ".woff2", ".ttf", ".eot",
}

func Handler() http.Handler {
	server := FileServer()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/api/") || path == "/api" {
			http.NotFound(w, r)
			return
		}

		if isStaticAsset(path) {
			server.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		r.URL.Path = "/"
		server.ServeHTTP(w, r)
	})
}

func isStaticAsset(path string) bool {
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	for _, ext := range staticFileExtensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}