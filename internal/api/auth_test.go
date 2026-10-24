package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler is a passthrough handler that reports the request succeeded.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func doAuthReq(t *testing.T, mw func(http.Handler) http.Handler, method, target, remoteAddr, xff, auth, token string) *httptest.ResponseRecorder {
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
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	return rec
}

func TestRequireAdminLocalModeLoopback(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/settings", "127.0.0.1:4321", "", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("loopback request in local mode = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// With no trusted proxies configured, XFF is ignored entirely: a foreign
// peer cannot present a LAN client in the header.
func TestRequireAdminLocalModeIgnoresXFFWithoutTrustedProxy(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "109.100.0.1:5555", "10.0.3.4", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign peer with LAN XFF first hop = %d, want 403 (XFF must be ignored)", rec.Code)
	}
}

func TestRequireAdminLocalModeRejectsForeign(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "109.100.0.1:5555", "203.0.113.7", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign proxied request in local mode = %d, want 403", rec.Code)
	}
}

// A direct loopback-LAN peer is accepted; the XFF chain is moot (the
// direct peer already decides).
func TestRequireAdminLocalModeAcceptsDirectLANPeer(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "127.0.0.1:5555", "10.0.3.4", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("direct LAN peer = %d, want 200", rec.Code)
	}
}

// A trusted proxy (BRAGIBOOKS_TRUSTED_PROXIES) that itself sits in the
// loopback-LAN can forward: the chain is walked right to left and the
// first untrusted entry (10.0.3.4) is the end client.
func TestRequireAdminLocalModeAcceptsXFFFromTrustedProxy(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "172.16.0.5")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "172.16.0.5:3128", "10.0.3.4", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("trusted proxy with LAN XFF end-client = %d, want 200", rec.Code)
	}
}

// Multi-hop chain: two trusted proxies append their hops; walking from the
// rightmost untrusted entry identifies the end client.
func TestRequireAdminLocalModeTrustedProxyChain(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "172.16.0.5,192.168.7.9/32")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "172.16.0.5:3128", "192.168.7.9, 10.0.3.4", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("two trusted proxies, LAN end-client = %d, want 200", rec.Code)
	}
	// Same chain, foreign end client: rejected.
	rec = doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "172.16.0.5:3128", "192.168.7.9, 109.100.0.1", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("two trusted proxies, foreign end-client = %d, want 403", rec.Code)
	}
}

// A trusted proxy that is itself NOT in the loopback-LAN is still
// honored (trust is explicit, not address-derived): a private-LAN proxy
// in front of the service.
func TestRequireAdminLocalModeTrustedProxyOffLAN(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "203.0.113.10")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "203.0.113.10:3128", "10.0.3.4", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("off-LAN trusted proxy with LAN XFF end-client = %d, want 200", rec.Code)
	}
}

// A CIDR entry in the trusted set covers the whole range.
func TestRequireAdminLocalModeTrustedProxyCIDR(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "192.168.99.0/24")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "192.168.99.14:3128", "127.0.0.1", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("proxy from trusted CIDR, loopback end-client = %d, want 200", rec.Code)
	}
}

// A foreign client hitting a trusted proxy directly and planting a LAN
// hop to the LEFT of the hop the proxy appends for them is refused: the
// rightmost untrusted entry (the attacker's real address) is the client.
func TestRequireAdminLocalModeRejectsForgedLeftHop(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "172.16.0.5")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books", "172.16.0.5:3128", "10.0.3.4, 8.8.8.8", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("trusted proxy, forged LAN left hop = %d, want 403", rec.Code)
	}
}

func TestRequireAdminLocalModeRejectsDirectForeign(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "DELETE", "/api/books/1", "8.8.8.8:1234", "", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("direct foreign request in local mode = %d, want 403", rec.Code)
	}
}

func TestRequireAdminTokenModeAcceptsValid(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "POST", "/api/settings", "8.8.8.8:1234", "", "Bearer s3cret", "")
	if rec.Code != http.StatusOK {
		t.Errorf("valid token from foreign IP = %d, want 200 (token mode grants remote access)", rec.Code)
	}
}

func TestRequireAdminTokenModeRequiresHeader(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "GET", "/api/books", "127.0.0.1:4321", "", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token = %d, want 401", rec.Code)
	}
}

func TestRequireAdminTokenModeRejectsWrongToken(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "GET", "/api/books", "127.0.0.1:4321", "", "Bearer nope", "")
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

// A valid cookie is auto-attached by the browser, so a state-changing
// request that authenticates via the cookie (no Authorization header)
// must pass the CSRF double-submit; a matching token passes, a missing
// header is refused (finding: CSRF).
func TestTokenModeCookieAuthRequiresCSRF(t *testing.T) {
	// GET (safe) is fine.
	rec := doAuthCookieReq(t, RequireAdmin("s3cret"), "GET", "/api/settings", "8.8.8.8:1234", "s3cret")
	if rec.Code != http.StatusOK {
		t.Errorf("cookie-auth GET = %d, want 200", rec.Code)
	}
	// POST without the X-CSRF-Token header: 403.
	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "s3cret"})
	rec = httptest.NewRecorder()
	RequireAdmin("s3cret")(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie-auth POST without CSRF header = %d, want 403", rec.Code)
	}
	// ... with a matching double-submit: 200.
	req = httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "s3cret"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	req.Header.Set(csrfHeaderName, "tok")
	rec = httptest.NewRecorder()
	RequireAdmin("s3cret")(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie-auth POST with CSRF token = %d, want 200", rec.Code)
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

// doAuthCSRFReq builds a loopback-LAN request with no auth headers (local
// mode), optionally carrying a bragibooks_csrf cookie and/or an
// X-CSRF-Token header.
func doAuthCSRFReq(t *testing.T, mw func(http.Handler) http.Handler, method, target, remoteAddr, csrfCookie, csrfHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	if csrfCookie != "" {
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfCookie})
	}
	if csrfHeader != "" {
		req.Header.Set(csrfHeaderName, csrfHeader)
	}
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	return rec
}

// csrfCookieFromHeader extracts the browser-side view of a response's
// Set-Cookie for the CSRF cookie.
func csrfCookieFromHeader(h http.Header) string {
	for _, c := range h.Values("Set-Cookie") {
		if strings.HasPrefix(c, csrfCookieName+"=") {
			val := strings.TrimPrefix(c, csrfCookieName+"=")
			if i := strings.Index(val, ";"); i >= 0 {
				return val[:i]
			}
			return val
		}
	}
	return ""
}

// A safe method from a loopback client passes and receives the CSRF
// cookie when absent (the client captures it for later double-submit
// use).
func TestCSRFGetIssuesCookieAndPasses(t *testing.T) {
	rec := doAuthCSRFReq(t, RequireAdmin(""), "GET", "/api/books", "127.0.0.1:1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET in local mode = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if tok := csrfCookieFromHeader(rec.Header()); tok == "" {
		t.Errorf("GET did not set the %s cookie", csrfCookieName)
	}
}

// A state-changing method without a bearer Authorization header requires
// the double-submit: cookie present + matching X-CSRF-Token header.
func TestCSRFPostRequiresMatchingToken(t *testing.T) {
	// No cookie at all: 403.
	rec := doAuthCSRFReq(t, RequireAdmin(""), "POST", "/api/settings", "127.0.0.1:1", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without cookie = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	// Cookie but no header: 403.
	rec = doAuthCSRFReq(t, RequireAdmin(""), "POST", "/api/settings", "127.0.0.1:1", "tok123", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with cookie, no header = %d, want 403", rec.Code)
	}
	// Cookie and mismatched header: 403.
	rec = doAuthCSRFReq(t, RequireAdmin(""), "POST", "/api/settings", "127.0.0.1:1", "tok123", "tok456")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with mismatched token = %d, want 403", rec.Code)
	}
	// Cookie and matching header: 200.
	rec = doAuthCSRFReq(t, RequireAdmin(""), "POST", "/api/settings", "127.0.0.1:1", "tok123", "tok123")
	if rec.Code != http.StatusOK {
		t.Errorf("POST with matching token = %d, want 200", rec.Code)
	}
}

// Same for PUT and DELETE.
func TestCSRFPutDeleteRequireMatchingToken(t *testing.T) {
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		rec := doAuthCSRFReq(t, RequireAdmin(""), m, "/api/books/1", "127.0.0.1:1", "t", "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without token = %d, want 403", m, rec.Code)
		}
		rec = doAuthCSRFReq(t, RequireAdmin(""), m, "/api/books/1", "127.0.0.1:1", "t", "t")
		if rec.Code != http.StatusOK {
			t.Errorf("%s with token = %d, want 200", m, rec.Code)
		}
	}
}

// An explicit Authorization header exempts the request (the browser never
// attaches it cross-origin): no CSRF header needed even from a loopback
// client.
func TestCSRFBearerExempt(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin(""), "POST", "/api/settings", "127.0.0.1:1", "", "Bearer whateversigh", "")
	if rec.Code != http.StatusOK {
		t.Errorf("POST with Authorization in local mode = %d, want 200", rec.Code)
	}
}

// A non-Bearer Authorization header (e.g. "Basic …" from a cached
// forwarding proxy) must NOT exempt a request that authenticates through
// the auto-attached cookie: the token rides the cookie, so the CSRF
// guard applies (finding: CSRF).
func TestCSRFNonBearerAuthHeaderNotExempt(t *testing.T) {
	// Token mode, cookie-authenticated POST, stray Basic header: 403.
	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "s3cret"})
	req.Header.Set("Authorization", "Basic c2VjbWV0OnBhc3M=")
	rec := httptest.NewRecorder()
	RequireAdmin("s3cret")(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie-auth POST with non-Bearer Authorization = %d, want 403", rec.Code)
	}
	// Same request with a matching double-submit: 200.
	req = httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "s3cret"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	req.Header.Set("Authorization", "Basic c2VjbWV0OnBhc3M=")
	req.Header.Set(csrfHeaderName, "tok")
	rec = httptest.NewRecorder()
	RequireAdmin("s3cret")(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie-auth POST with non-Bearer Authorization + CSRF = %d, want 200", rec.Code)
	}
}

// A valid Bearer header exempting while a same-token cookie is also
// present (the browser sends both after a login): no CSRF needed.
func TestCSRFBearerHeaderExemptWithCookiePresent(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "s3cret"})
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	RequireAdmin("s3cret")(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Bearer POST with cookie present = %d, want 200", rec.Code)
	}
}

// A "Bearer" header with an empty token is not an exemption either: the
// request falls back to the (missing here) cookie and is refused.
func TestCSRFEmptyBearerNotExempt(t *testing.T) {
	rec := doAuthReq(t, RequireAdmin("s3cret"), "POST", "/api/settings", "8.8.8.8:1234", "", "Bearer  ", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST with empty Bearer = %d, want 401 (no token presented)", rec.Code)
	}
}

// End to end (double-submit round trip): a GET issues the cookie; a
// subsequent POST that echoes it succeeds, and one that does not is
// rejected — the exact shape of the SvelteKit client flow.
func TestCSRFDoubleSubmitRoundTrip(t *testing.T) {
	mw := RequireAdmin("")
	rec := doAuthCSRFReq(t, mw, "GET", "/api/books", "127.0.0.1:1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", rec.Code)
	}
	token := csrfCookieFromHeader(rec.Header())
	if token == "" {
		t.Fatal("GET did not set the CSRF cookie")
	}
	rec = doAuthCSRFReq(t, mw, "POST", "/api/settings", "127.0.0.1:1", token, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with cookie only = %d, want 403", rec.Code)
	}
	rec = doAuthCSRFReq(t, mw, "POST", "/api/settings", "127.0.0.1:1", token, token)
	if rec.Code != http.StatusOK {
		t.Errorf("POST echoing cookie = %d, want 200", rec.Code)
	}
}

// A foreign peer in local mode is still rejected before the CSRF check
// (auth rule, not CSRF, is the gate from off-LAN).
func TestCSRFDoesNotRelaxPeerRule(t *testing.T) {
	rec := doAuthCSRFReq(t, RequireAdmin(""), "POST", "/api/settings", "8.8.8.8:1", "tok", "tok")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST from foreign peer with valid token = %d, want 403", rec.Code)
	}
}

// Multi-hop XFF in proxy-append order: the direct peer is the rightmost
// trusted proxy; walking the chain right to left, every trusted entry is
// the hop its appender appended, and the first untrusted entry (the LAN
// client) is the end client. A forged off-LAN entry planted to the LEFT of
// the chain must have no effect — only the walked-to entry decides.
func TestRequireAdminLocalModeSkipsTrustedProxyHops(t *testing.T) {
	// Both proxies injected as separate env entries so the exact two-hop
	// paths cannot be masked by a range entry; 10.9.0.0/24 matches neither.
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "203.0.113.10,10.0.0.2")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books",
		"203.0.113.10:3128", "8.8.8.8, 10.0.0.2, 192.168.1.50", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("LAN client across two trusted hops = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// Same chain, off-LAN end client (8.8.8.8 is the walked-to first
	// untrusted entry): rejected, even though 192.168.1.50 sits further
	// right in the string.
	rec = doAuthReq(t, RequireAdmin(""), "GET", "/api/books",
		"203.0.113.10:3128", "8.8.8.8, 10.0.0.2, 203.0.113.7", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign client across two trusted hops = %d, want 403", rec.Code)
	}

	// No spaces in the header.conf form: same right-to-left walk must
	// hold.
	rec = doAuthReq(t, RequireAdmin(""), "GET", "/api/books",
		"203.0.113.10:3128", "8.8.8.8,10.0.0.2,192.168.1.50", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("unspaced header, LAN end client = %d, want 200", rec.Code)
	}
}

// A trusted proxy that forwards an XFF entry that is not an IP literal
// (proxies sometimes log the client hostname) must be rejected: no
// verifiable end client exists, so the request cannot be accepted.
func TestRequireAdminLocalModeTrustedProxyRejectsInvalidRightmostXFF(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "10.0.0.2")
	rec := doAuthReq(t, RequireAdmin(""), "GET", "/api/books",
		"10.0.0.2:3128", "203.0.113.7, not-an-ip", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("trusted proxy, non-IP rightmost XFF = %d, want 403", rec.Code)
	}
}

// Valid admin cookie + "Authorization: Bearer " (empty token): the token
// is carried by the cookie (auth passes), the empty Bearer grants no
// exemption, and with no CSRF token the POST must be refused by the guard.
func TestCSRFEmptyBearerWithCookieNotExempt(t *testing.T) {
	const token = "s3cret"
	req := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: token})
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	RequireAdmin(token)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie-auth POST with empty Bearer, no CSRF = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	// Same request with the double-submit present: accepted.
	req = httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: token})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	req.Header.Set("Authorization", "Bearer ")
	req.Header.Set(csrfHeaderName, "tok")
	rec = httptest.NewRecorder()
	RequireAdmin(token)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie-auth POST with empty Bearer + CSRF = %d, want 200", rec.Code)
	}
}

// Safe (non state-changing) methods never require the double-submit token:
// with cookie auth (no Authorization header) and no CSRF cookie, all of
// GET/HEAD/OPTIONS/TRACE pass the CSRF guard.
func TestCSRFSafeMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/books", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "anything"})
			rec := httptest.NewRecorder()
			RequireAdmin("anything")(okHandler()).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s with cookie auth, no CSRF token = %d, want 200 (%s)", method, rec.Code, rec.Body.String())
			}
		})
	}
}
