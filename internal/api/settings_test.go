package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/config"
	"github.com/bragibooks/bragibooks/internal/db"
)

// newTestSettingsHandler builds a Handler with a settings-migration DB in a
// temp dir and a ConfigManager pointed at a temp YAML file (before any
// SaveKeys, per the partial-save pattern).
func newTestSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.RunMigrations(d); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(dir, "config.yaml"))

	av := new(atomic.Value)
	av.Store(audiobookdb.NewClient("", ""))

	return &Handler{svc: Services{
		DB:          d,
		Config:      cm,
		Processor:   audio.NewProcessor("/bin/true", t.TempDir(), "", 1, "{author}/{title}", "info"),
		AudiobookDB: av,
	}}
}

func doPutSettings(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateSettings(rec, req)
	return rec
}

func TestUpdateSettingsOutputSchemeValidation(t *testing.T) {
	cases := []struct {
		name     string
		scheme   string
		want     int
		wantBody string
	}{
		{name: "forbidden colon rejected", scheme: `{title}:x`, want: http.StatusBadRequest, wantBody: "output scheme:"},
		{name: "clean scheme accepted", scheme: `{author}/{title}`, want: http.StatusOK, wantBody: `"output_scheme":"{author}/{title}"`},
		{name: "reserved device name rejected", scheme: `{con}`, want: http.StatusBadRequest, wantBody: "output scheme:"},
		{name: "year scheme accepted (warnings do not gate)", scheme: `{author}/{year}`, want: http.StatusOK, wantBody: `"output_scheme":"{author}/{year}"`},
		{name: "asin alone rejected", scheme: `{asin}`, want: http.StatusBadRequest, wantBody: "output scheme:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestSettingsHandler(t)
			rec := doPutSettings(t, h, `{"input_dir":"/i","output_dir":"/o","output_scheme":`+`"`+tc.scheme+`"}`)
			if rec.Code != tc.want {
				t.Fatalf("PUT output_scheme=%q: status = %d, want %d (body %s)", tc.scheme, rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("PUT output_scheme=%q: body %q does not contain %q", tc.scheme, rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestUpdateSettingsSchemeRoundTrip pins the wire contract: the raw template
// string is stored and returned unchanged.
func TestUpdateSettingsSchemeRoundTrip(t *testing.T) {
	h := newTestSettingsHandler(t)
	rec := doPutSettings(t, h, `{"input_dir":"/i","output_dir":"/o","output_scheme":"{author}/vol. {title}"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec2 := httptest.NewRecorder()
	h.GetSettings(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec2.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want %q", got["output_scheme"], "{author}/vol. {title}")
	}
}

// TestUpdateSettingsOmittedOutputSchemeRetained pins the nil-optional-field
// branch: a partial update that does not send output_scheme leaves the
// stored path format untouched.
func TestUpdateSettingsOmittedOutputSchemeRetained(t *testing.T) {
	h := newTestSettingsHandler(t)
	if rec := doPutSettings(t, h, `{"input_dir":"/i","output_dir":"/o","output_scheme":"{author}/vol. {title}"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// No output_scheme in the body.
	if rec := doPutSettings(t, h, `{"input_dir":"/in","output_dir":"/out"}`); rec.Code != http.StatusOK {
		t.Fatalf("partial PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	h.GetSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want retained %q", got["output_scheme"], "{author}/vol. {title}")
	}
}

// TestUpdateSettingsRejectedRequestChangesNothing pins no-partial-persistence:
// a 400 on one field must leave the other fields' stored values unchanged.
func TestUpdateSettingsRejectedRequestChangesNothing(t *testing.T) {
	h := newTestSettingsHandler(t)
	if rec := doPutSettings(t, h, `{"input_dir":"/i","output_dir":"/o","output_scheme":"{author}/vol. {title}"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec := doPutSettings(t, h, `{"input_dir":"/in","output_dir":"/out","output_scheme":`+`"`+`{con}`+"`")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rejected PUT: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec2 := httptest.NewRecorder()
	h.GetSettings(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET: status = %d, want 200", rec2.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET: decode: %v", err)
	}
	if got["input_dir"] != "/i" {
		t.Fatalf("GET input_dir = %v, want %q (should be unchanged after rejection)", got["input_dir"], "/i")
	}
	if got["output_dir"] != "/o" {
		t.Fatalf("GET output_dir = %v, want %q (should be unchanged after rejection)", got["output_dir"], "/o")
	}
	if got["output_scheme"] != "{author}/vol. {title}" {
		t.Fatalf("GET output_scheme = %v, want %q (should be unchanged after rejection)", got["output_scheme"], "{author}/vol. {title}")
	}
}
