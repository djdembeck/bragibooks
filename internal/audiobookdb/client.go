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
func NewClient(apiKey string, baseURL string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		APIKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
}

// do executes a request and decodes the JSON response into v.
func (c *Client) do(ctx context.Context, req *http.Request, v interface{}) error {
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

// Search calls POST /search and returns matching results.
// The upstream API accepts {"q": "..."} and returns a bare array of
// search documents with a "collection" field indicating the entity type.
// The "type" parameter is optional and causes 500s on some deployments,
// so we omit it and filter client-side instead.
func (c *Client) Search(ctx context.Context, query string, types []string, skip, take int) (*SearchResponse, error) {
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
	hits := make([]SearchHit, 0, len(docs))
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
		// Re-marshal the full doc as Data
		if full, err := json.Marshal(doc); err == nil {
			hit.Data = full
		}
		hits = append(hits, hit)
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
