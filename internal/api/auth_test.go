package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a passthrough handler that reports the request succeeded.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func doAuthReq(t *testing.T, mw func(http.Handler) http.Handler, method, target, remoteAddr, xff, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	return rec
}

func TestRequireAdminLocalModeLoopback(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/settings", "127.0.0.1:4321", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("loopback request in local mode = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// A proxied LAN client is accepted only when the proxy (direct peer) is
// itself on the loopback-LAN; XFF from a foreign peer is never trusted.
func TestRequireAdminLocalModeRejectsXFFFromForeignPeer(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "109.100.0.1:5555", "10.0.3.4", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign peer with LAN XFF first hop = %d, want 403 (XFF must be ignored)", rec.Code)
	}
}

func TestRequireAdminLocalModeRejectsForeign(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "109.100.0.1:5555", "203.0.113.7", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign proxied request in local mode = %d, want 403", rec.Code)
	}
}

// The service behind its own proxy on the LAN: direct peer is loopback,
// so the first XFF hop (the real LAN client) is trusted.
func TestRequireAdminLocalModeAcceptsXFFFromLocalPeer(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "127.0.0.1:5555", "10.0.3.4", "")
	if rec.Code != http.StatusOK {
		t.Errorf("local peer with LAN XFF first hop = %d, want 200", rec.Code)
	}
}

func TestRequireAdminLocalModeRejectsDirectForeign(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "DELETE", "/api/books/1", "8.8.8.8:1234", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("direct foreign request in local mode = %d, want 403", rec.Code)
	}
}

func TestRequireAdminTokenModeAcceptsValid(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "POST", "/api/settings", "8.8.8.8:1234", "", "Bearer s3cret")
	if rec.Code != http.StatusOK {
		t.Errorf("valid token from foreign IP = %d, want 200 (token mode grants remote access)", rec.Code)
	}
}

func TestRequireAdminTokenModeRequiresHeader(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "GET", "/api/books", "127.0.0.1:4321", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token = %d, want 401", rec.Code)
	}
}

func TestRequireAdminTokenModeRejectsWrongToken(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "GET", "/api/books", "127.0.0.1:4321", "", "Bearer nope")
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token = %d, want 403", rec.Code)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		header string
		cookie string
		token  string
		ok     bool
	}{
		{header: "Bearer abc123", token: "abc123", ok: true},
		{header: "bearer abc123", token: "abc123", ok: true}, // case-insensitive scheme
		{header: "Bearer   abc123", token: "abc123", ok: true},
		{header: "Token abc123", token: "", ok: false},
		{header: "Basic abc123", token: "", ok: false},
		{header: "", cookie: "tok-cookie", token: "tok-cookie", ok: true},
		// Bearer header is preferred over the cookie.
		{header: "Bearer hdr-token", cookie: "cookie-token", token: "hdr-token", ok: true},
		{header: "", cookie: "", token: "", ok: false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/x", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: adminCookieName, Value: c.cookie})
		}
		got, ok := bearerToken(req)
		if ok != c.ok || got != c.token {
			t.Errorf("bearerToken(hdr=%q, cookie=%q) = (%q, %v), want (%q, %v)", c.header, c.cookie, got, ok, c.token, c.ok)
		}
	}
}

// Cookie auth (EventSource/SSE cannot set headers): the token is accepted
// via the cookie alone, from any address in token mode.
func TestRequireAdminTokenModeAcceptsCookie(t *testing.T) {
	rec := doAuthCookieReq(t, RequireAdmin("s3cret"), "GET", "/api/jobs/1/stream", "8.8.8.8:1234", "s3cret")
	if rec.Code != http.StatusOK {
		t.Errorf("valid token via cookie from foreign IP = %d, want 200", rec.Code)
	}
}

func TestRequireAdminTokenModeRejectsWrongCookie(t *testing.T) {
	rec := doAuthCookieReq(t, RequireAdmin("s3cret"), "GET", "/api/settings", "8.8.8.8:1234", "nope")
	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong token via cookie = %d, want 403", rec.Code)
	}
}

func TestRequireAdminLocalModeCookieDoesNotBypassPeerRule(t *testing.T) {
	// In local mode the cookie grants nothing extra: a remote peer is 403.
	rec := doAuthCookieReq(t, RequireAdmin(""), "GET", "/api/settings", "8.8.8.8:1234", "anything")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie from foreign peer in local mode = %d, want 403", rec.Code)
	}
}

// doAuthCookieReq is doAuthReq plus a token cookie on the request.
func doAuthCookieReq(t *testing.T, mw func(http.Handler) http.Handler, method, target, remoteAddr, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	return rec
}
