package api

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// TestSanitizeTransportErrorFixedDiagnostic guards the cover-log
// redaction: a *url.Error embeds the full request URL (path + query) in
// its message, and its nested message can name any hop's URL — including
// a malformed/relative Location whose signed query is not an http(s) URL
// and survives substring-scanning redaction. The fix replaces the fragile
// substring recovery with a fixed diagnostic that cannot leak the raw
// URL/path/query; the caller already reports the redacted host.
func TestSanitizeTransportErrorFixedDiagnostic(t *testing.T) {
	const wantFixed = "cover request failed (URL details redacted)"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "relative location with signed query",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`Get "https://covers.example/seed.jpg": redirect: relative Location "cdn.example/cover.jpg?sig=abc123" is not a valid redirect target`),
			},
			want: wantFixed,
		},
		{
			name: "unquoted value with semicolon parameter",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`Get "https://covers.example/seed.jpg": redirect to Location: /cover;v=1?sig=abc failed`),
			},
			want: wantFixed,
		},
		{
			name: "url.Parse form quoted relative",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`parse "cdn.example/cover.jpg?sig=abc123": first path segment in relative URL cannot be absolute`),
			},
			want: wantFixed,
		},
		{
			name: "quoted value with escaped quote",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`parse "/cover\"broken%zz?sig=SECRET": invalid URL escape "%zz"`),
			},
			want: wantFixed,
		},
		{
			name: "absolute location",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New(`redirect to Location: https://cdn.example/cover.jpg?sig=abc123 failed`),
			},
			want: wantFixed,
		},
		{
			name: "plain message with URL wrapper",
			err: &url.Error{
				Op:  "GET",
				URL: "https://covers.example/seed.jpg",
				Err: errors.New("connection reset by peer"),
			},
			want: wantFixed,
		},
		{
			name: "non-url error passes through",
			err:  errors.New("connection reset by peer"),
			want: "connection reset by peer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeTransportError(tc.err)
			if got != tc.want {
				t.Errorf("sanitize = %q; want %q", got, tc.want)
			}
			// The fixed diagnostic must never leak URL fragments.
			if strings.Contains(got, "sig=") || strings.Contains(got, "cdn.example") || strings.Contains(got, "covers.example") {
				t.Errorf("sanitize = %q; must not contain URL fragments", got)
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

// TestCoverPreviewHandlerContract pins the POST /covers/preview body
// contract: a valid JSON body with a URL that passes the public-HTTPS guard
// REACHES the validated fetch path (the body is decoded, exactly-one-value
// checked, and the URL validated — the request then goes out to fetch),
// while a body over the 8 KiB cap is 413 BEFORE any fetch, and trailing
// data after the JSON value / a non-JSON body are 400 (the URL is one
// value; anything extra is refused, not silently dropped).
//
// The fetch is driven with the package's stub seams (lookupCoverHost +
// coverDialContext, the same pair the other cover tests use): the host
// resolves to a public address and the pinned dial is made to refuse, so
// the success case lands at 502 "cover preview unavailable" — the marker
// that the body passed every gate and the fetch was actually attempted.
// The 413/400 cases never reach the dial. No public network is contacted.
func TestCoverPreviewHandlerContract(t *testing.T) {
	oldLookup := lookupCoverHost
	lookupCoverHost = func(_ context.Context, host string) ([]string, error) {
		if host == "covers.example" {
			return []string{"93.184.216.34"}, nil
		}
		return nil, fmt.Errorf("no address for %s", host)
	}
	t.Cleanup(func() { lookupCoverHost = oldLookup })
	oldDial := coverDialContext
	coverDialContext = func(_ context.Context, _ string, _ string) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { coverDialContext = oldDial })

	h := &Handler{svc: &Services{DB: newTestDB(t)}}
	pv := CoverPreviewHandler(h)
	validURL := "https://covers.example/1.jpg"

	cases := []struct {
		name string
		body string
		want int
	}{
		// Valid body: decodes, exactly-one-value, passes the guard, and
		// reaches the fetch — the stubbed dial refuses, so 502. This is
		// the "fetchable URL" path: a 413/400 would mean the body was
		// refused before the fetch, not that the fetch ran.
		{"valid fetchable url", `{"url":"` + validURL + `"}`, http.StatusBadGateway},
		{"body over 8 KiB cap", `{"url":"` + validURL + `","pad":"` + strings.Repeat("x", maxCoverPreviewBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"trailing data after json value", `{"url":"` + validURL + `"}` + `{"extra":1}`, http.StatusBadRequest},
		{"non-json body", "not json", http.StatusBadRequest},
		{"missing url field", `{}`, http.StatusBadRequest},
		{"blank url", `{"url":"  "}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/covers/preview", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			pv.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d — body: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusBadGateway {
				if !strings.Contains(w.Body.String(), "cover preview unavailable") {
					t.Errorf("502 does not name the fetch failure: %s", w.Body.String())
				}
			}
		})
	}
	// A non-POST method is refused before the body is read.
	req := httptest.NewRequest(http.MethodGet, "/covers/preview", nil)
	w := httptest.NewRecorder()
	pv.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d; want 405", w.Code)
	}
}
