package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/bragibooks/bragibooks/internal/config"
)

// ValidateHost protects every route from DNS rebinding without changing the
// LAN/key authorization boundary. Forwarded host headers are never authority;
// proxies must preserve Host or send an explicitly allowed backend Host.
func ValidateHost(cfg config.ServerConfig) (func(http.Handler) http.Handler, error) {
	allowed := map[string]bool{"localhost": true}
	if cfg.Host != "" && cfg.Host != "0.0.0.0" && cfg.Host != "::" {
		// Listener IPv6 is unbracketed, unlike request authorities.
		host := cfg.Host
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		} else {
			var err error
			host, err = authorityHost(host)
			if err != nil {
				return nil, fmt.Errorf("server.host: %w", err)
			}
		}
		allowed[host] = true
	}
	if cfg.AllowedHosts != "" {
		for _, entry := range strings.Split(cfg.AllowedHosts, ",") {
			host, err := authorityHost(strings.TrimSpace(entry))
			if err != nil {
				return nil, fmt.Errorf("server.allowed_hosts: %w", err)
			}
			allowed[host] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, err := authorityHost(r.Host)
			ip := net.ParseIP(host)
			// Private literal destinations retain Docker published-port LAN
			// access: the host's LAN IP is not a container interface address.
			// DNS names are never resolved to decide trust.
			local := ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
			if err != nil || (!allowed[host] && !local) {
				writeError(w, http.StatusForbidden, "untrusted request Host")
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// authorityHost accepts an ASCII DNS name or literal IP, optionally with a
// numeric TCP port. IPv6 must be bracketed; zones, URLs and wildcards are not
// accepted. Ports are validated but do not constrain the allowlist, since a
// published/proxy port may differ from the listener port.
func authorityHost(authority string) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("invalid host authority %q", authority) }
	if authority == "" || strings.ContainsAny(authority, " /\\@?#%\t\r\n") {
		return invalid()
	}
	host := authority
	if strings.HasPrefix(authority, "[") || strings.Contains(authority, ":") {
		if strings.HasPrefix(authority, "[") && strings.HasSuffix(authority, "]") {
			host = authority[1 : len(authority)-1]
		} else {
			var port string
			var err error
			host, port, err = net.SplitHostPort(authority)
			if err != nil || port == "" {
				return invalid()
			}
			for _, c := range port {
				if c < '0' || c > '9' {
					return invalid()
				}
			}
			p, err := strconv.Atoi(port)
			if err != nil || p < 1 || p > 65535 {
				return invalid()
			}
		}
		if strings.HasPrefix(authority, "[") && (net.ParseIP(host) == nil || !strings.Contains(host, ":")) {
			return invalid()
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) == 0 || len(host) > 253 {
		return invalid()
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalid()
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return invalid()
			}
		}
	}
	return host, nil
}
