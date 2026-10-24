package api

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
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
// tradeoff is documented: any host on that LAN can reach the API in local
// mode, which is why the env token (BRAGIBOOKS_ADMIN_TOKEN) is the
// recommended configuration for anything not on a trusted quiet LAN.
//
// X-Forwarded-For is trusted ONLY when the direct TCP peer
// (RemoteAddr) is itself in one of the above networks (the service sits
// behind its own proxy on the LAN); from any other peer the header is
// ignored entirely, so a remote client cannot forge a trusted first hop.
// Token auth (header or cookie) grants remote access in token mode and is
// subject to the same loopback-LAN rule only in local mode.

// RequireAdmin returns an http.Handler middleware enforcing admin access
// on the /api route group (excluding /api/health and static serving, which
// are mounted outside it by the caller).
//
// Token mode (adminToken != ""): every request must carry the admin token
// via "Authorization: Bearer <token>" (preferred) or the
// adminCookieName cookie (constant-time compare; the cookie exists so
// clients that cannot set headers — e.g. EventSource — can authenticate).
// Requests that omit the token get 401; a wrong token gets 403. Token
// auth works from any address.
//
// Local mode (adminToken == ""): requests must come from a loopback-LAN
// address (see above); X-Forwarded-For is honored only from a
// loopback-LAN direct peer. Everything else is 403. One warning is
// logged at construction time so the operator knows the service is
// running without a secret.
func RequireAdmin(adminToken string) func(http.Handler) http.Handler {
	if adminToken == "" {
		log.Printf("Warning: no BRAGIBOOKS_ADMIN_TOKEN configured — admin API is in local mode: only loopback/10./192.168./172.16-31 clients are accepted. Set the env var to enable token auth for remote access.")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if ok {
				if adminToken == "" {
					// Token mode is not active; a token-bearing request in
					// local mode is checked against the loopback-LAN rule
					// below, not compared against anything.
					if !isLoopbackLAN(r) {
						writeError(w, http.StatusForbidden, "admin API access from non-local address without a configured token")
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, http.StatusForbidden, "invalid admin token")
				return
			}

			// No bearer token: local mode requires a loopback-LAN client.
			if adminToken != "" {
				// Token mode: token is required.
				w.Header().Set("WWW-Authenticate", `Bearer realm="bragibooks"`)
				writeError(w, http.StatusUnauthorized, "admin token required")
				return
			}
			if !isLoopbackLAN(r) {
				writeError(w, http.StatusForbidden, "admin API is only reachable from loopback/LAN addresses (set BRAGIBOOKS_ADMIN_TOKEN for remote access)")
				return
			}
			next.ServeHTTP(w, r)
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

// isLoopbackLAN reports whether the request's client is in the
// loopback-LAN networks defined at the top of this file. The direct TCP
// peer (RemoteAddr) must be in those networks; only then, when the request
// arrives via a proxy, is the first X-Forwarded-For hop consulted (it is
// the end client as seen by the nearest proxy). From a non-local direct
// peer the XFF header is ignored and the request is rejected.
func isLoopbackLAN(r *http.Request) bool {
	peer := clientAddress(r)
	if !isLoopbackLANIP(peer) {
		return false
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			if ip := net.ParseIP(first); ip != nil {
				return isLoopbackLANIP(ip)
			}
		}
	}
	return true
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
