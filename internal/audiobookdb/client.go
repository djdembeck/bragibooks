package audiobookdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client wraps the audiobookdb.org REST API.
type Client struct {
	APIKey  string
	baseURL string
	http    *http.Client

	csrfMu  sync.Mutex
	csrfTok string
}

// NewClient creates an audiobookdb client. An empty apiKey is valid for
// keyless/Guest requests; a non-empty one is sent as X-API-Key. If baseURL
// does not conform to the trusted-origin policy (https://audiobookdb.org or
// a subdomain; localhost dev targets only behind BRAGIBOOKS_ALLOW_LOCAL_ADB)
// the request and every subsequent request it built is treated as
// untrusted and fails client-side.
func NewClient(apiKey string, baseURL string) *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		APIKey:  apiKey,
		baseURL: baseURL,
	}
	c.http = &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		// Defense-in-depth (finding: SSRF): every redirect hop must stay
		// on a trusted origin, refusing a same-origin server that
		// redirects to an exfiltration sink.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := c.validateTarget(req.URL.String()); err != nil {
				return fmt.Errorf("refused audiobookdb redirect to untrusted target: %w", err)
			}
			return nil
		},
	}
	if err := c.validateTarget(baseURL); err != nil {
		// Blank the base URL so all requests fail with the same clear
		// trusted-origin error; never attempt the network.
		c.baseURL = ""
	}
	return c
}

// validateTarget enforces the trusted-origin policy for the target URL
// (finding: SSRF defense-in-depth). The settings endpoint validates the
// same thing at update time; this is the second line at request time, so a
// configured-out-of-band base URL (env/YAML) cannot become an exfiltration
// sink either. localhost dev targets are allowed only behind
// BRAGIBOOKS_ALLOW_LOCAL_ADB=1. The policy itself lives in TrustedBaseURL,
// shared with the settings validator.
func (c *Client) validateTarget(baseURL string) error {
	_, err := TrustedBaseURL(baseURL)
	return err
}

// do executes a request and decodes the JSON response into v. The request
// target is re-validated here (defense-in-depth against SSRF) so that even
// a request built against a non-conforming base URL (e.g. after a redirect
// to an attacker-controlled origin, or an out-of-band base URL) is refused.
func (c *Client) do(ctx context.Context, req *http.Request, v interface{}) error {
	// Defense-in-depth SSRF gate: refuse any request whose target does not
	// conform to the same trusted-origin policy the base URL was checked
	// against at construction. This also fails closed when a request
	// somehow carries an untrusted origin (e.g. a redirect target).
	if err := c.validateTarget(req.URL.String()); err != nil {
		return fmt.Errorf("refused audiobookdb request to untrusted target: %w", err)
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	} else if !safeMethod(req.Method) {
		// Keyless POST/PUT/PATCH/DELETE: the double-submit CSRF token is
		// required (X-API-Key and Authorization callers bypass CSRF upstream).
		token, err := c.csrfToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("X-CSRF-Token", token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bragibooks/1.0 (+https://github.com/djdembeck/bragibooks)")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("audiobookdb request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("audiobookdb read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("audiobookdb %d: %s", resp.StatusCode, body)
	}

	if v == nil {
		return nil
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("audiobookdb unmarshal %s: %w", resp.Request.URL.Path, err)
	}
	return nil
}

// safeMethod reports whether m is exempt from CSRF validation upstream
// (GET/HEAD/OPTIONS pass through and also prime the cookie).
func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// csrfToken returns the upstream CSRF token, reading it from the cookie jar
// (set by any prior safe request) or priming it with a GET to the base URL.
// The upstream CSRFEnsureCookie middleware sets csrf_token on every GET.
func (c *Client) csrfToken(ctx context.Context) (string, error) {
	c.csrfMu.Lock()
	defer c.csrfMu.Unlock()

	if c.csrfTok != "" {
		return c.csrfTok, nil
	}

	if tok := c.jarToken(); tok != "" {
		c.csrfTok = tok
		return tok, nil
	}

	prime, err := http.NewRequestWithContext(
		ctx, http.MethodGet, c.baseURL+"/search?q=", nil)
	if err != nil {
		return "", fmt.Errorf("build CSRF prime request: %w", err)
	}
	prime.Header.Set("Accept", "application/json")
	prime.Header.Set("User-Agent", "Bragibooks/1.0 (+https://github.com/djdembeck/bragibooks)")
	resp, err := c.http.Do(prime)
	if err != nil {
		return "", fmt.Errorf("audiobookdb CSRF prime request failed: %w", err)
	}
	resp.Body.Close()

	tok := c.jarToken()
	if tok == "" {
		return "", fmt.Errorf("audiobookdb CSRF prime returned no csrf_token cookie")
	}
	c.csrfTok = tok
	return tok, nil
}

// jarToken reads the csrf_token cookie for the client's base URL from the
// shared cookie jar.
func (c *Client) jarToken() string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name == "csrf_token" {
			return cookie.Value
		}
	}
	return ""
}

// searchTakeCap bounds the number of documents fetched from upstream.
// The upstream API has no reliable paged-search contract; capping the take
// keeps a large UI request from pulling the full index.
const searchTakeCap = 500

// Search calls POST /search and returns matching results.
// The upstream API accepts {"q": "..."} and returns a bare array of
// search documents with a "collection" field indicating the entity type.
// The "type" parameter is optional and causes 500s on some deployments,
// so it is omitted and the results are filtered client-side instead.
// skip/take are honored client-side after the type filter: negative values
// are treated as zero and take is clamped to searchTakeCap.
func (c *Client) Search(ctx context.Context, query string, types []string, skip, take int) (*SearchResponse, error) {
	if skip < 0 {
		skip = 0
	}
	// take <= 0 means "no explicit cap" — bounded by searchTakeCap so an
	// unbounded request can never pull the full index.
	if take < 0 {
		take = 0
	}
	if take == 0 || take > searchTakeCap {
		take = searchTakeCap
	}
	body, _ := json.Marshal(map[string]any{
		"q": query,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// API returns a bare array of search documents
	var docs []map[string]json.RawMessage
	if err := c.do(ctx, req, &docs); err != nil {
		return nil, err
	}

	// Wrap into SearchHit{ID, Type, Data}
	wantTypes := make(map[string]bool, len(types))
	for _, t := range types {
		wantTypes[strings.TrimSpace(t)] = true
	}
	hits := make([]SearchHit, 0, take)
	for _, doc := range docs {
		hit := SearchHit{Data: json.RawMessage("{}")}
		if rawID, ok := doc["id"]; ok {
			_ = json.Unmarshal(rawID, &hit.ID)
		}
		if rawCol, ok := doc["collection"]; ok {
			_ = json.Unmarshal(rawCol, &hit.Type)
		}
		// Client-side type filter (replaces server-side "type" param)
		if len(wantTypes) > 0 && !wantTypes[hit.Type] {
			continue
		}
		// Pagination: skip-leading, take-capped.
		if skip > 0 {
			skip--
			continue
		}
		// Re-marshal the full doc as Data (take == 0 means unbounded, so
		// the cap only applies once the first hit lands).
		if full, err := json.Marshal(doc); err == nil {
			hit.Data = full
		}
		hits = append(hits, hit)
		if take > 0 && len(hits) >= take {
			break
		}
	}
	return &SearchResponse{Results: hits}, nil
}

// GetBook calls GET /books/{id} and returns the book. The include parameter
// controls which relations are expanded (comma-separated).
func (c *Client) GetBook(ctx context.Context, id string, include string) (*Book, error) {
	url := fmt.Sprintf("%s/books/%s", c.baseURL, id)
	if include != "" {
		url += "?include=" + include
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build book request: %w", err)
	}

	var book Book
	if err := c.do(ctx, req, &book); err != nil {
		return nil, err
	}
	return &book, nil
}

// GetRelease calls GET /releases/{id} and returns the release. The include
// parameter controls which relations are expanded (comma-separated).
func (c *Client) GetRelease(ctx context.Context, id string, include string) (*Release, error) {
	url := fmt.Sprintf("%s/releases/%s", c.baseURL, id)
	if include != "" {
		url += "?include=" + include
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build release request: %w", err)
	}

	var release Release
	if err := c.do(ctx, req, &release); err != nil {
		return nil, err
	}
	return &release, nil
}
