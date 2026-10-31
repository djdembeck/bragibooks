package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/config"
)

func TestHostAuthority(t *testing.T) {
	guard, err := ValidateHost(config.ServerConfig{Host: "bound.example", AllowedHosts: "Books.Example.,[2001:db8::5],203.0.113.8"})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "LOCALHOST.:8080", "127.0.0.2:80", "192.168.1.20:8888", "10.1.2.3", "172.31.255.2", "[::1]", "[fd12::1]:8888", "[fe80::1]", "[::ffff:192.168.1.20]:80", "BOOKS.EXAMPLE.:443", "bound.example", "[2001:db8:0:0::5]:443", "203.0.113.8"} {
		t.Run(host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/books", nil)
			r.Host = host
			w := httptest.NewRecorder()
			guard(okHandler()).ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", host, w.Code, w.Body)
			}
		})
	}
	for _, host := range []string{"evil.example", "localhost.evil.example", "127.attacker.example", "books.example.evil", "sub.books.example", "203.0.113.9", "[2001:db8::6]", "0.0.0.0", "[::]", "", "localhost:", "localhost:0", "localhost:65536", "localhost:+80", "localhost:abc", "localhost:80:90", "::1", "[localhost]", "[127.0.0.1]", "[::1", "[::1]:", "[fe80::1%eth0]", "user@localhost", "http://localhost", "localhost/path", "localhost?x", "localhost#x", "localhost ", "localhost..", "*.example", "-host", "host_foo", "books.example,localhost"} {
		t.Run("deny/"+host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/books", nil)
			r.Host = host
			r.Header.Set("X-Forwarded-Host", "localhost")
			r.Header.Set("Forwarded", "host=localhost")
			w := httptest.NewRecorder()
			guard(okHandler()).ServeHTTP(w, r)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "untrusted request Host") {
				t.Fatalf("%q: %d %s", host, w.Code, w.Body)
			}
		})
	}
}

func TestHostWithAuthAndCSRF(t *testing.T) {
	t.Setenv("BRAGIBOOKS_TRUSTED_PROXIES", "127.0.0.1")
	guard, err := ValidateHost(config.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, host, peer, key, csrf, xff string
		status                           int
		message                          string
	}{
		{"LAN csrf", "192.168.20.4", "192.168.20.10:10", "", "tok", "", 200, ""},
		{"LAN missing csrf", "localhost", "127.0.0.1:10", "", "", "", 403, "CSRF token mismatch"},
		{"external key", "localhost", "8.8.8.8:10", "secret", "", "", 200, ""},
		{"wrong key", "localhost", "127.0.0.1:10", "wrong", "tok", "", 403, "invalid API key"},
		{"external no key", "localhost", "8.8.8.8:10", "", "tok", "", 401, "API key required"},
		{"hostile valid key csrf", "evil.example", "8.8.8.8:10", "secret", "tok", "", 403, "untrusted request Host"},
		{"hostile LAN csrf", "evil.example", "192.168.20.10:10", "", "tok", "", 403, "untrusted request Host"},
		{"hostile trusted proxy", "evil.example", "127.0.0.1:10", "", "tok", "192.168.1.10", 403, "untrusted request Host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/books", nil)
			r.Host = tc.host
			r.RemoteAddr = tc.peer
			if tc.key != "" {
				r.Header.Set("Authorization", "Bearer "+tc.key)
			}
			r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
			r.Header.Set(csrfHeaderName, tc.csrf)
			r.Header.Set("X-Forwarded-For", tc.xff)
			r.Header.Set("X-Forwarded-Host", "localhost")
			w := httptest.NewRecorder()
			guard(sw(config.AdminAuthState{Enabled: true, KeyDigest: keyDigestOf("secret")})(okHandler())).ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}

func TestInvalidHostConfiguration(t *testing.T) {
	for _, hosts := range []string{"*", "*.example", "https://books.example", "books.example,", "[::1]:invalid"} {
		if _, err := ValidateHost(config.ServerConfig{AllowedHosts: hosts}); err == nil {
			t.Errorf("accepted %q", hosts)
		}
	}
	for _, bind := range []string{"", "0.0.0.0", "::", "::1"} {
		if _, err := ValidateHost(config.ServerConfig{Host: bind}); err != nil {
			t.Errorf("bind %q: %v", bind, err)
		}
	}
}
