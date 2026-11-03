package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bragibooks/bragibooks/internal/version"
)

// TestHealthCheckJSONResponse covers the health probe contract: the handler
// must answer with a JSON body carrying the status field and the stamped
// version (Version from internal/version, which is "dev" in an unstamped
// go test build).
func TestHealthCheckJSONResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	HealthCheck(rec, r)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}

	if got.Status != "ok" {
		t.Errorf("Status = %q, want %q", got.Status, "ok")
	}
	if got.Version != version.Version {
		t.Errorf("Version = %q, want version.Version = %q", got.Version, version.Version)
	}
}
