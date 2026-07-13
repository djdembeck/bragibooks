package audiobookdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps the audiobookdb.org REST API.
type Client struct {
	APIKey  string
	baseURL string
	http    *http.Client
}

// NewClient creates an audiobookdb client. An empty apiKey is valid for
func NewClient(apiKey string, baseURL string) *Client {
	return &Client{
		APIKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// do executes a request and decodes the JSON response into v.
func (c *Client) do(ctx context.Context, req *http.Request, v interface{}) error {
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
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

// Search calls POST /search and returns matching results across the given
// entity types (e.g. "books", "releases").
func (c *Client) Search(ctx context.Context, query string, types []string, skip, take int) (*SearchResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"query": query,
		"types": types,
		"skip":  skip,
		"take":  take,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var res SearchResponse
	if err := c.do(ctx, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
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