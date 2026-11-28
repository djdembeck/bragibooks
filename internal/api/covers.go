package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// coverPreviewTimeout bounds a single preview fetch: the preview is a
// decorative thumbnail, so it must not hold the request (or the pinned
// connection) for as long as a full cover download would.
const coverPreviewTimeout = 10 * time.Second

// maxCoverPreviewBodyBytes caps the preview POST body before decoding: the
// payload is one URL plus JSON framing, so 8 KiB is far beyond any real
// draft URL while bounding what an unbounded client can stream.
const maxCoverPreviewBodyBytes = 8 << 10

// CoverPreviewHandler serves a draft cover URL through the SAME validated
// fetch path the save/run paths use (public-HTTPS guard re-asserted on
// every redirect hop, dialing only the validated address) into a temp
// file, then serves THAT file. The editor's image must never load the
// raw URL directly: a saved external URL would then be resolved and
// followed by the browser independently of the server's destination and
// redirect checks, letting a destination reachable from the operator's
// network be contacted on page load.
//
// The URL travels in the POST BODY, not the request-target query: a
// draft URL can carry credentials in its path or a signed query, and a
// query string would expose both to request-target logging by reverse
// proxies before the server-side redaction in error messages can run.
// The POST method also puts the fetch under the CSRF double-submit
// guard (GET is exempt), so a cross-site page cannot invoke the server's
// fetch through the keyless LAN path. Mount it at /covers/preview,
// behind the same auth as the rest of /covers.
func CoverPreviewHandler(h *Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		// The body is one URL: cap it BEFORE decoding so an unbounded
		// stream cannot reserve memory for the JSON string before any
		// timeout applies.
		r.Body = http.MaxBytesReader(w, r.Body, maxCoverPreviewBodyBytes)
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			var mbErr *http.MaxBytesError
			if errors.As(err, &mbErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "cover URL body too large")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// Decode stops at the first JSON value: trailing data past the
		// `{"url": …}` object (still inside the 8 KiB bound) must be
		// refused, not silently dropped — drain the bounded reader and
		// reject anything left. A second Decode expecting io.EOF is the
		// canonical "exactly one value" check.
		var trailing json.RawMessage
		if err := dec.Decode(&trailing); err != io.EOF {
			var mbErr *http.MaxBytesError
			if errors.As(err, &mbErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "cover URL body too large")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		raw := strings.TrimSpace(req.URL)
		if raw == "" {
			writeError(w, http.StatusBadRequest, "url is required")
			return
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "invalid cover URL")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), coverPreviewTimeout)
		defer cancel()
		tmp, err := os.CreateTemp("", "bragi-cover-preview-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)
		if err := downloadCoverFile(ctx, raw, tmpPath); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("cover preview unavailable: %s", redactCoverURL(raw)))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		// The preview is a per-request fetch of a draft URL: no caching,
		// so an edited draft always re-renders from the current value.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, tmpPath)
	})
}
