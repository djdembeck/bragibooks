package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bragibooks/bragibooks/internal/config"
)

// The loopback-LAN definition, stated once and used everywhere in this
// package:
//
//   - 127.0.0.0/8 (all of 127.*.*.*) and ::1
//   - 10.0.0.0/8 (class-A private, the common docker-compose LAN)
//   - 172.16.0.0/12 (class-B private range, 172.16.-172.31.*)
//   - 192.168.0.0/16 (class-C private)
//
// This is deliberately NOT “only loopback”: the compose deployment runs in
// a private LAN and the host must be able to reach it (finding: local
// mode must stop LAN exposure without breaking single-host use). The
// tradeoff is documented: any host on that LAN can reach the API — the
// web UI relies on exactly this, via the CSRF double-submit cookie.
//
// X-Forwarded-For is honored ONLY when the direct TCP peer (RemoteAddr)
// is in an explicitly configured trusted-proxy set (env
// BRAGIBOOKS_TRUSTED_PROXIES: comma-separated IPs and/or CIDRs, default
// empty). The chain is
// then walked from the rightmost hop to the client, skipping trusted-proxy
// entries, and the first untrusted entry is the end client. With no
// trusted proxies configured the header is ignored entirely and the direct
// peer decides, so a remote client behind any unconfigured proxy cannot
// forge a trusted first hop. (Finding: XFF trust.)
//
// State-changing (POST/PUT/PATCH/DELETE) requests additionally pass a
// double-submit-cookie CSRF check: the client echoes the server-set
// bragibooks_csrf cookie in an X-CSRF-Token header. Requests authenticated
// by an explicit "Bearer" Authorization header are exempt — browsers never
// attach that header to cross-origin requests, so they cannot be forged
// by a third-party page. Any other Authorization value does not exempt
// the request; its token then rides the auto-attached cookie and takes
// the CSRF guard.
//
// The access rule, given the current {Enabled, KeyDigest} state (read per
// request, so runtime toggles apply without a restart):
//
//  1. A present, valid key (Bearer header or bragibooks_token cookie)
//     allows from any address. A Bearer header is CSRF-exempt; the key is
//     compared as its SHA-256 digest against the stored digest.
//  2. No presented key (or none applicable) and the client is
//     loopback/LAN: allow via the double-submit-CSRF guard, in BOTH the
//     enabled and disabled states. This is what keeps the same-origin web
//     UI working on the LAN whether or not a key exists.
//  3. A present but invalid key: 403 "invalid API key" — always, so a
//     stale credential can never degrade to the LAN fallback.
//  4. Otherwise (non-LAN, no valid key):
//     - Enabled: 401 "API key required" with WWW-Authenticate: Bearer.
//     - Disabled: 403 "external API disabled — enable External API in
//       Settings to access the API remotely".

// RequireAdminWithState returns an http.Handler middleware enforcing
// admin access on the /api route group (excluding /api/health and static
// serving, which are mounted outside it by the caller).
//
// lazy supplies the current auth state (config.AdminAuthState) per
// request, so enable/disable/reissue from the settings endpoints takes
// effect on the next request without a restart.
func RequireAdminWithState(lazy func() config.AdminAuthState) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := lazy()
			token, hasToken := bearerToken(r)
			// Requests authenticated by an explicit "Bearer" Authorization
			// header are CSRF-exempt: browsers never attach that header to
			// a cross-origin request, so a third-party page cannot forge
			// them. A non-Bearer Authorization value changes nothing —
			// the token then rides the auto-attached adminCookieName
			// cookie, which a third-party page can present — and takes the
			// normal CSRF guard (finding: CSRF).
			exempt := bearerHeaderPresent(r)

			if hasToken {
				// A presented credential is always treated as key auth: it
				// must match the stored digest. Deliberately stricter than
				// an old-code silent fallthrough — a presented-but-stale
				// key is never accepted, and the web UI sends no stored
				// key, so it is unaffected.
				if st.KeyDigest == "" || !matchAPIKeyDigest(token, st.KeyDigest) {
					writeError(w, http.StatusForbidden, "invalid API key")
					return
				}
				if exempt {
					next.ServeHTTP(w, r)
					return
				}
				csrfGuard(w, r, next)
				return
			}

			// No presented key: the loopback-LAN path is reachable in both
			// the enabled and disabled states (the UI-keeps-working
			// guarantee).
			if isLoopbackLAN(r) {
				csrfGuard(w, r, next)
				return
			}
			if !st.Enabled {
				writeError(w, http.StatusForbidden, "external API disabled — enable External API in Settings to access the API remotely")
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="bragibooks"`)
			writeError(w, http.StatusUnauthorized, "API key required")
		})
	}
}

// adminCookieName carries the admin token for clients that cannot set
// request headers (e.g. EventSource SSE streams). The Bearer header is
// preferred when both are present.
const adminCookieName = "bragibooks_token"

// bearerToken extracts the admin token from the "Authorization: Bearer
// <token>" header (preferred) or the adminCookieName cookie.
func bearerToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			if token := strings.TrimSpace(h[len(prefix):]); token != "" {
				return token, true
			}
		}
	}
	if c, err := r.Cookie(adminCookieName); err == nil {
		if token := strings.TrimSpace(c.Value); token != "" {
			// The SPA (and hand-crafted clients) may percent-encode the
			// token; decode before comparing. PathUnescape (not
			// QueryUnescape) keeps a literal '+' intact. On a decoding
			// error the raw value is compared as-is, which matches
			// verbatim cookies.
			if decoded, derr := url.PathUnescape(token); derr == nil {
				token = decoded
			}
			return token, true
		}
	}
	return "", false
}

// isLoopbackLAN reports whether the request's end client is in the
// loopback-LAN networks defined at the top of this file. The direct TCP
// peer (RemoteAddr) must be in those networks, or the peer must be an
// explicitly trusted proxy (env BRAGIBOOKS_TRUSTED_PROXIES). When the peer
// is a trusted proxy, the X-Forwarded-For chain is walked from the
// rightmost entry to the client: entries of trusted proxies are skipped
// and the first untrusted entry is the end client. With no trusted
// proxies configured the header is ignored entirely and the direct peer
// decides (finding: XFF trust).
func isLoopbackLAN(r *http.Request) bool {
	peer := clientAddress(r)
	proxies := parseTrustedProxies()
	if len(proxies) > 0 && isTrustedProxyIP(peer, proxies) {
		// The peer is an explicitly trusted proxy, so the forwarded chain
		// is authoritative: the proxy has told us the true end client, and
		// a LAN end client is accepted while a foreign one is rejected
		// (we do NOT fall back to accepting on the peer's own LAN status).
		return isLoopbackLANIP(endClientFromChain(peer, r.Header.Get("X-Forwarded-For"), proxies))
	}
	// Not arriving from a trusted proxy: the XFF header is not trusted and
	// the direct peer decides (finding: XFF trust).
	return isLoopbackLANIP(peer)
}

// endClientFromChain resolves the end client from an X-Forwarded-For chain
// received from a trusted proxy. Proxies append their client's hop to the
// right, so the chain is walked from the rightmost entry to the left:
// trusted-proxy entries (each one the hop its appender appended) are
// skipped, and the first untrusted, parseable entry is the end client.
// Walking this direction matters — a leftward scan would let a remote
// attacker plant a LAN entry to the left of the hop the trusted proxy
// appended for them. When every entry is a trusted proxy, the peer itself
// is the best available answer; when a rightward-reached entry is not an
// IP literal (proxies sometimes log the client hostname), no address can
// be verified and nil is returned (rejection).
func endClientFromChain(peer net.IP, xff string, proxies []trustedProxy) net.IP {
	entries := strings.Split(xff, ",")
	for i := len(entries) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(entries[i])
		if entry == "" {
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil
		}
		if !isTrustedProxyIP(ip, proxies) {
			return ip
		}
	}
	return peer
}

// trustedProxy is one element of the configured trusted-proxy set: a
// single IP (netmask 0 → exact match) or a CIDR range.
type trustedProxy struct {
	ip   net.IP
	mask net.IPMask
}

// parseTrustedProxies reads the BRAGIBOOKS_TRUSTED_PROXIES env var: a
// comma-separated list of IP literals and/or CIDR ranges. Unparseable
// entries are ignored with a log line; the default (unset/empty) is an
// empty set, in which case XFF is never honored.
func parseTrustedProxies() []trustedProxy {
	raw := strings.TrimSpace(os.Getenv("BRAGIBOOKS_TRUSTED_PROXIES"))
	if raw == "" {
		return nil
	}
	var out []trustedProxy
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(part); err == nil {
			out = append(out, trustedProxy{ip: ipnet.IP, mask: ipnet.Mask})
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			out = append(out, trustedProxy{ip: ip})
			continue
		}
		log.Printf("Warning: ignoring unparseable BRAGIBOOKS_TRUSTED_PROXIES entry %q (want an IP or CIDR)", part)
	}
	return out
}

// isTrustedProxyIP reports whether ip matches any configured trusted
// proxy. A bare IP entry is an exact match; a CIDR entry is a range
// match (IPv4-in-v6-mapped forms compare equal under masking).
func isTrustedProxyIP(ip net.IP, proxies []trustedProxy) bool {
	for _, p := range proxies {
		if p.mask != nil {
			if ip.Mask(p.mask).Equal(p.ip.Mask(p.mask)) {
				return true
			}
		} else if p.ip.Equal(ip) {
			return true
		}
	}
	return false
}

// bearerHeaderPresent reports whether the client explicitly presented a
// "Bearer <token>" Authorization header with a non-empty token. Browsers
// never attach that header cross-origin (it is a forbidden header name),
// so its presence is existential proof the request was crafted by
// same-origin code — the CSRF exemption (finding: CSRF). Any other value
// (e.g. "Basic …" from a cached forwarding proxy) grants none: the token
// then rides the auto-attached cookie and the request must pass the CSRF
// guard, as does a "Bearer" header whose token is empty.
func bearerHeaderPresent(r *http.Request) bool {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	return len(h) >= len(prefix) &&
		strings.EqualFold(h[:len(prefix)], prefix) &&
		strings.TrimSpace(h[len(prefix):]) != ""
}

// csrfCookieName carries the per-session CSRF double-submit token.
const csrfCookieName = "bragibooks_csrf"

// X-CSRF-Token header carrying the echoed csrfCookieName value.
const csrfHeaderName = "X-CSRF-Token"

// csrfGuard enforces the double-submit-cookie CSRF check. The caller has
// already applied the authorization rule and the Authorization-header
// exemption; here, state-changing requests may proceed only when the
// client echoes the server-set csrf cookie in the X-CSRF-Token header.
// Safe methods receive the cookie when absent and pass through unchanged.
func csrfGuard(w http.ResponseWriter, r *http.Request, next http.Handler) {
	ensureCSRFCookie(r, w)
	if methodAllowedWithoutToken(r.Method) {
		next.ServeHTTP(w, r)
		return
	}
	token := ""
	if c, err := r.Cookie(csrfCookieName); err == nil {
		token = c.Value
	}
	if token == "" || !constantTimeTokenEqual(token, r.Header.Get(csrfHeaderName)) {
		writeError(w, http.StatusForbidden, "CSRF token mismatch")
		return
	}
	next.ServeHTTP(w, r)
}

// methodAllowedWithoutToken reports whether a method is safe under the
// CSRF rule: no state change is expected, so no token is required.
func methodAllowedWithoutToken(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// ensureCSRFCookie issues the random double-submit CSRF cookie when the
// request has no such cookie yet. The value is a 128-bit random hex
// string; no signature is needed because the check is equality between
// the cookie and the header within one origin. The cookie is deliberately
// NOT HttpOnly: it is a bearer token the same-origin UI must echo in the
// X-CSRF-Token header, and browsers hide HttpOnly cookies from
// document.cookie. SameSite=Lax is what keeps it out of cross-origin
// requests.
func ensureCSRFCookie(r *http.Request, w http.ResponseWriter) {
	if _, err := r.Cookie(csrfCookieName); err == nil {
		return
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is effectively unreachable on supported
		// platforms; reject the request rather than proceed with no
		// cookie at all.
		writeError(w, http.StatusInternalServerError, "unable to initialize CSRF token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    hex.EncodeToString(buf),
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})
}

// constantTimeTokenEqual compares two CSRF token values in constant time
// (and only when lengths match) to avoid side channels.
func constantTimeTokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// isLoopbackLANIP reports whether ip falls in the loopback-LAN networks
// defined at the top of this file.
func isLoopbackLANIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// IPv6 in v4-mapped form (e.g. ::ffff:10.0.0.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return true
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil { // unreachable: literals
			continue
		}
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// clientAddress resolves the direct TCP peer of the request. XFF is never
// consulted here; see isLoopbackLAN for the XFF trust rule.
func clientAddress(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
