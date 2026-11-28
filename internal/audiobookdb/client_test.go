package audiobookdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidateTargetPolicy covers the trusted-origin policy (finding: SSRF
// defense-in-depth): only the production host (https) is accepted; other
// origins, internal hosts, and non-HTTP schemes are refused; the dev switch
// opens localhost/127.
func TestValidateTargetPolicy(t *testing.T) {
	cases := []struct {
		url  string
		ok   bool
		env  string
		name string
	}{
		{name: "prod host exact", url: "https://audiobookdb.org/api", ok: true},
		{name: "subdomain", url: "https://api.audiobookdb.org/api", ok: true},
		{name: "prod host rejects http scheme", url: "http://audiobookdb.org/api", ok: false},
		{name: "lookalike subdomain of another zone", url: "https://audiobookdb.org.evil.example/api", ok: false},
		{name: "lookalike host prefix", url: "https://audiobookdb.org.attacker.example/api", ok: false},
		{name: "internal private host", url: "https://10.0.0.5/", ok: false},
		{name: "169.254 metadata", url: "http://169.254.169.254/", ok: false},
		{name: "localhost without dev", url: "http://localhost:8100/api", ok: false},
		{name: "localhost with dev=1", url: "http://localhost:8100/api", ok: true, env: "1"},
		{name: "localhost with dev=true", url: "http://localhost:8100/api", ok: true, env: "true"},
		{name: "127 with dev=yes", url: "http://127.0.0.1:8100/api", ok: true, env: "yes"},
		{name: "127.0.0.1 with dev=1", url: "http://127.0.0.1:8100/api", ok: true, env: "1"},
		{name: "v6 loopback literal with dev", url: "http://[::1]:8100/api", ok: true, env: "1"},
		// A name merely starting with 127. is NOT a loopback literal
		// (finding: 127-prefix) and must be refused in dev.
		{name: "127. lookalike domain with dev", url: "http://127.attacker.example:8100/api", ok: false, env: "1"},
		{name: "localhost with dev=0", url: "http://localhost:8100/api", ok: false, env: "0"},
		{name: "relative URL refused", url: "/api/search", ok: false},
		{name: "empty refused", url: "", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", tc.env)
			c := NewClient("", tc.url)
			err := c.validateTarget(tc.url)
			if (err == nil) != tc.ok {
				t.Errorf("validateTarget(%q) error = %v, want ok=%v", tc.url, err, tc.ok)
			}
			if !tc.ok && c.baseURL != "" {
				t.Errorf("untrusted base URL %q not blanked by NewClient", tc.url)
			}
		})
	}
}

// TestDoRefusesUntrustedTarget builds a client against a trusted origin and
// then attempts a request whose URL points at an untrusted host: the client
// must refuse with the trusted-origin error and never touch the network.
func TestDoRefusesUntrustedTarget(t *testing.T) {
	c := NewClient("k", "https://audiobookdb.org/api")
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/api/search", nil)
	err := c.do(req.Context(), req, nil)
	if err == nil {
		t.Fatal("request to untrusted target must be refused")
	}
	if !strings.Contains(err.Error(), "untrusted") {
		t.Errorf("error should mention untrusted target, got: %v", err)
	}
}

// TestCheckRedirectRefusesUntrustedHop covers the SSRF redirect vector
// end-to-end: NewClient installs a CheckRedirect closure that refuses hops
// to non-trusted origins, so the http.Client will not follow a 302 to an
// exfiltration sink.
func TestCheckRedirectRefusesUntrustedHop(t *testing.T) {
	c := NewClient("", "https://audiobookdb.org/api")
	check := c.http.CheckRedirect
	if check == nil {
		t.Fatal("CheckRedirect not installed on the client")
	}
	bad, _ := http.NewRequest(http.MethodGet, "https://some-internal.corp/leak", nil)
	if err := check(bad, nil); err == nil {
		t.Fatal("redirect to non-trusted origin must be refused")
	}
	good, _ := http.NewRequest(http.MethodGet, "https://cdn.audiobookdb.org/assets", nil)
	if err := check(good, nil); err != nil {
		t.Errorf("redirect to audiobookdb subdomain must be allowed, got %v", err)
	}
}

// newSearchServer serves a fixed bare-array JSON search response.
func newSearchServer(t *testing.T, docs []map[string]any) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(docs)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSearchHonorsSkipTake validates the client-side pagination applied
// after the type filter: skip drops the first N filtered hits and take
// caps the window.
func TestSearchHonorsSkipTake(t *testing.T) {
	// 6 docs, alternating A/B; the B-window is ids 1, 3, 5.
	docs := []map[string]any{
		{"collection": "A", "id": 0},
		{"collection": "B", "id": 1},
		{"collection": "A", "id": 2},
		{"collection": "B", "id": 3},
		{"collection": "A", "id": 4},
		{"collection": "B", "id": 5},
	}
	srv := newSearchServer(t, docs)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	c := NewClient("test-key", srv.URL)

	r, err := c.Search(context.Background(), "q", []string{"B"}, 0, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Results) != 3 {
		t.Fatalf("unfiltered-type results = %d, want 3", len(r.Results))
	}

	r, err = c.Search(context.Background(), "q", []string{"B"}, 1, 0)
	if err != nil {
		t.Fatalf("Search skip=1: %v", err)
	}
	if len(r.Results) != 2 {
		t.Errorf("skip=1 results = %d, want 2", len(r.Results))
	}

	r, err = c.Search(context.Background(), "q", []string{"B"}, 0, 1)
	if err != nil {
		t.Fatalf("Search take=1: %v", err)
	}
	if len(r.Results) != 1 {
		t.Errorf("take=1 results = %d, want 1", len(r.Results))
	}

	r, err = c.Search(context.Background(), "q", []string{"B"}, 5, 10)
	if err != nil {
		t.Fatalf("Search skip=5: %v", err)
	}
	if len(r.Results) != 0 {
		t.Errorf("skip=5 results = %d, want 0", len(r.Results))
	}
}

// TestSearchTakeCap clamps the upstream window: a take beyond the cap
// yields at most searchTakeCap results.
func TestSearchTakeCap(t *testing.T) {
	docs := make([]map[string]any, 0, searchTakeCap+1)
	for i := range searchTakeCap + 1 {
		docs = append(docs, map[string]any{"collection": "A", "id": i})
	}
	srv := newSearchServer(t, docs)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	c := NewClient("test-key", srv.URL)

	r, err := c.Search(context.Background(), "q", nil, 0, searchTakeCap+50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Results) != searchTakeCap {
		t.Errorf("capped results = %d, want %d", len(r.Results), searchTakeCap)
	}
}

func TestGetBookByASIN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/audiobooks/external/audible/B00B5HZGUG":
			fmt.Fprint(w, `{"id":"WQQDcMBQ3qUT","title":"The Martian"}`)
		case "/audiobooks/external/audible/NOPE000000":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[{"status":"404","title":"Not Found","detail":"audiobook with id audible/NOPE000000 not found"}]}`)
		case "/audiobooks/external/audible/BROKEN0000":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"errors":[{"status":"502","title":"Bad Gateway","detail":"404 not in this body"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	c := NewClient("k", srv.URL)

	t.Run("found", func(t *testing.T) {
		book, err := c.GetBookByASIN(context.Background(), "B00B5HZGUG")
		if err != nil {
			t.Fatalf("GetBookByASIN: %v", err)
		}
		if book.ID != "WQQDcMBQ3qUT" || book.Title != "The Martian" {
			t.Errorf("book = %+v", book)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := c.GetBookByASIN(context.Background(), "NOPE000000")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v; want ErrNotFound", err)
		}
	})

	t.Run("other status is not ErrNotFound", func(t *testing.T) {
		// 502 body deliberately contains "404" text: only the status
		// code decides, never the body.
		_, err := c.GetBookByASIN(context.Background(), "BROKEN0000")
		if errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v; must not be ErrNotFound", err)
		}
		if err == nil {
			t.Error("502 must return an error")
		}
	})
}

func TestGetBookByASINRejectsInvalidASIN(t *testing.T) {
	// The client must refuse non-Audible-shaped ASINs before they are
	// interpolated into the upstream URL; no server is reached at all.
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")
	c := NewClient("k", "http://127.0.0.1:0")

	for _, asin := range []string{
		"",                 // empty
		"b00b5hzgug",       // lowercase
		"B00B5HZGU",        // 9 chars
		"B00B5HZGUGG",      // 11 chars
		"B00B5HZG%2FG",     // encoded slash
		"B00B5HZGUG?x=1",   // query char
		"B00B5HZGUG/extra", // path separator
		"B00B5HZGUG ",      // trailing space
		"B00B5HZGUG-",      // non-alphanumeric
	} {
		if _, err := c.GetBookByASIN(context.Background(), asin); !errors.Is(err, ErrInvalidASIN) {
			t.Errorf("GetBookByASIN(%q) err = %v; want ErrInvalidASIN", asin, err)
		}
	}
}
