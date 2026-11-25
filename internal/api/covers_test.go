package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/config"

	"github.com/go-chi/chi/v5"
)

// TestSanitizeTransportErrorRedactsRelativeLocations guards the cover-log
// redaction against MALFORMED/relative redirect locations: Go's redirect
// parse error embeds the raw Location string, and a relative value with a
// signed query (no scheme prefix) must not survive into the log.
func TestSanitizeTransportErrorRedactsRelativeLocations(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantSub []string // substrings that MUST appear
		noSub   []string // substrings that MUST NOT appear
	}{
		{
			// The exact shape of the finding: a relative location with a
			// signed query, as url.Parse reports it inside a *url.Error.
			name: "relative location with signed query",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`Get "https://covers.example/seed.jpg": redirect: relative Location "cdn.example/cover.jpg?sig=abc123" is not a valid redirect target`),
			},
			wantSub: []string{"<redacted location>", "covers.example"},
			noSub:   []string{"sig=abc123", "cdn.example/cover.jpg"},
		},
		{
			// An unquoted redirect value with a semicolon parameter AND a
			// signed query: ';' is a legal URI value character, so the
			// WHOLE value must be redacted — stopping at the ';' would
			// leave the signed query in the log.
			name: "unquoted value with semicolon parameter",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`Get "https://covers.example/seed.jpg": redirect to Location: /cover;v=1?sig=abc failed`),
			},
			wantSub: []string{"<redacted location>", "covers.example"},
			noSub:   []string{"sig=abc", "/cover;v=1"},
		},
		{
			// Go's actual parse failure for a bare relative path: the raw
			// value is quoted, and it carries a signed query.
			name: "url.Parse form quoted relative",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`parse "cdn.example/cover.jpg?sig=abc123": first path segment in relative URL cannot be absolute`),
			},
			wantSub: []string{},
			noSub:   []string{"sig=abc123", "cdn.example/cover.jpg"},
		},
		{
			// A malformed Location with an embedded quote: Go's url.Error
			// quotes the raw value with the embedded quote ESCAPED, so a
			// plain quote scan stops at \" and leaves the signed query
			// behind. The WHOLE escaped token must be redacted.
			name: "quoted value with escaped quote",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`parse "/cover\"broken%zz?sig=SECRET": invalid URL escape "%zz"`),
			},
			wantSub: []string{"<redacted location>"},
			noSub:   []string{"sig=SECRET", "broken%zz"},
		},
		{
			// An absolute Location is reduced to its host-only form by the
			// URL pass; the Location pass must not eat the host.
			name: "absolute location keeps host",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`redirect to Location: https://cdn.example/cover.jpg?sig=abc123 failed`),
			},
			wantSub: []string{"cdn.example"},
			noSub:   []string{"sig=abc123", "cover.jpg?"},
		},
		{
			// A message with no URL and no Location passes through.
			name: "plain message untouched",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New("connection reset by peer"),
			},
			wantSub: []string{"connection reset by peer"},
			noSub:   []string{"<redacted>"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeTransportError(tc.err)
			for _, s := range tc.wantSub {
				if !strings.Contains(got, s) {
					t.Errorf("sanitize = %q; want it to contain %q", got, s)
				}
			}
			for _, s := range tc.noSub {
				if strings.Contains(got, s) {
					t.Errorf("sanitize = %q; must not contain %q", got, s)
				}
			}
		})
	}
}

func TestCoverHandlerServesExtractedCoversOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "7.jpg"), []byte("\xff\xd8\xff\xe0jpeg"), 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	handler := CoverHandler(dir)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"existing id cover", "/covers/7.jpg", http.StatusOK},
		{"unknown id", "/covers/8.jpg", http.StatusNotFound},
		{"wrong extension", "/covers/secret.txt", http.StatusNotFound},
		{"name traversal", "/covers/../7.jpg", http.StatusNotFound},
		{"empty name", "/covers/", http.StatusNotFound},
		{"non-numeric id", "/covers/abc.jpg", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("%s status = %d; want %d — body: %s", tc.path, w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
					t.Errorf("content-type = %q; want image/jpeg", got)
				}
			}
		})
	}
}

func TestCoverHandlerDisabledWithoutDirectory(t *testing.T) {
	handler := CoverHandler("")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/covers/7.jpg", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}

// The covers route is mounted inside RegisterRoutes behind the same gate as
// /api: an anonymous remote client must not be able to enumerate library
// art by book ID. The SPA authenticates these <img> requests with the
// same-origin bragibooks_token cookie, which the server accepts.
func TestRegisterRoutesCoversRequireAdmin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "7.jpg"), []byte("\xff\xd8\xff\xe0jpeg"), 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	const token = "s3cret"
	h := &Handler{svc: &Services{
		DB:        newTestDB(t),
		CoversDir: dir,
		Config:    config.NewConfigManager(),
	}}
	h.svc.Config.SetAPIEndpointState(true, keyDigestOf(token))

	r := chi.NewRouter()
	h.RegisterRoutes(r, RequireAdminWithState(h.svc.Config.AdminAuthState))

	req := func(remoteAddr, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/covers/7.jpg", nil)
		req.RemoteAddr = remoteAddr
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := req("203.0.113.9:5555", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous remote cover = %d; want 401", w.Code)
	}
	if w := req("203.0.113.9:5555", "wrong"); w.Code != http.StatusForbidden {
		t.Errorf("wrong-token cover = %d; want 403", w.Code)
	}
	if w := req("203.0.113.9:5555", token); w.Code != http.StatusOK {
		t.Errorf("token-cookie cover = %d; want 200 — an <img> in the SPA sends it", w.Code)
	}
}
