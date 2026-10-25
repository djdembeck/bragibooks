package api

import (
	"net/http"
	"path/filepath"
	"strings"
)

// CoverHandler serves the extracted source cover art kept in dir.
//
// The art is schematic library content, so the caller mounts this handler
// behind the same auth middleware as /api (see RegisterRoutes) — an
// anonymous client must not be able to enumerate /covers/1.jpg, /covers/2.jpg.
// It sits outside the /api prefix only because the `<img>` tags that render
// it cannot attach the bearer header; the SPA mirrors its token into the
// same-origin `bragibooks_token` cookie, which the browser sends with those
// image requests. Only the `<id>.jpg` files written by CreateBooks are
// served: no directory listings, no traversal, no other content types.
func CoverHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/covers/")
		if dir == "" || !validCoverName(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, name))
	})
}

// validCoverName reports whether name is one of the `<book id>.jpg` files
// written by CreateBooks. Requiring digits only rejects path separators,
// parent references, and stray assets in the covers directory.
func validCoverName(name string) bool {
	if len(name) < 5 || name[len(name)-4:] != ".jpg" {
		return false
	}
	for _, c := range name[:len(name)-4] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
