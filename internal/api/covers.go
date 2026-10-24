package api

import (
	"net/http"
	"path/filepath"
)

// CoverHandler serves the extracted source cover art kept in dir.
//
// The art is referenced from <img src="/covers/…"> tags, which cannot
// attach the admin bearer token, so this route is mounted outside the
// authed /api subtree — like the SPA it renders inside, and like the
// health probe. Only the `<id>.jpg` files written by CreateBooks are
// served: no directory listings, no traversal, no other content types.
func CoverHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/covers/"):]
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
