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
// It sits outside the /api prefix because the `<img>` tags that render it
// cannot attach the bearer header, so it rides the same no-key loopback/LAN
// fallback as the /api routes. The SPA does not mirror its token into a
// cookie (it only clears a leftover `bragibooks_token`), so remote
// API-key clients cannot load cover art via `<img>`: the book endpoints
// return only a protected cover URL, and an `<img>` cannot attach the
// Authorization header. Only the `<id>.jpg` files written by CreateBooks
// are served: no directory listings, no traversal, no other content types.
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

// validCoverName reports whether name is a legal covers-directory asset:
// the `<book id>.jpg` files written by CreateBooks, the snapshot captures
// (`<book id>-<unixnano>.jpg` and the legacy `<book id>-snapshot.jpg`,
// written by the metadata capture and staged into a run by buildEngineJob),
// OR a manual-override cover asset (`ovr-<book id>-<hex>.jpg`, the
// metadata editor's uploaded artwork). Requiring the exact shapes rejects
// path separators, parent references, and stray assets in the shared
// covers directory.
func validCoverName(name string) bool {
	if len(name) < 5 || name[len(name)-4:] != ".jpg" {
		return false
	}
	if strings.HasPrefix(name, "ovr-") {
		return overrideCoverName(name)
	}
	// The book id is digits; after a "-" the snapshot class carries either
	// the legacy "snapshot" marker or the unix-nano digits. Anything else
	// is not a covers-directory asset this handler may serve.
	id := name[:len(name)-4]
	dash := strings.IndexByte(id, '-')
	if dash < 0 {
		return allDigitsLen(id)
	}
	if !allDigitsLen(id[:dash]) {
		return false
	}
	suffix := id[dash+1:]
	return suffix == "snapshot" || allDigitsLen(suffix)
}

func allDigitsLen(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
