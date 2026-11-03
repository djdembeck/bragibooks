package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/models"
)

// profileRoute injects a chi route context with the given {id} param.
func profileRoute(t *testing.T, id string) context.Context {
	t.Helper()
	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = append(ctx.URLParams.Keys, "id")
	ctx.URLParams.Values = append(ctx.URLParams.Values, id)
	return context.WithValue(context.Background(), chi.RouteCtxKey, ctx)
}

// readOnlyTestDir creates a directory the test process cannot write to and
// restores it for cleanup. Root bypasses the permission bits, so the
// callers that need the failure skip under root.
func readOnlyTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := os.WriteFile(filepath.Join(dir, "probe"), nil, 0o644); err == nil {
		_ = os.Remove(filepath.Join(dir, "probe"))
		t.Skip("process can write to a 0555 directory (root); cannot test denial")
	}
	return dir
}

func TestListOutputProfilesSortedCaseInsensitive(t *testing.T) {
	h := newTestHandler(t)
	h.svc.DB.Exec(`INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES
		('zebra', '/z', ''),
		('apple', '', '{author}/{title}'),
		('Mango', '/m', '')`)

	req := httptest.NewRequest("GET", "/api/output-profiles", nil)
	w := httptest.NewRecorder()
	h.ListOutputProfiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d — body: %s", w.Code, w.Body.String())
	}
	var resp OutputProfilesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var names []string
	for _, p := range resp.Profiles {
		names = append(names, p.Name)
		if p.CreatedAt == "" || p.UpdatedAt == "" {
			t.Errorf("profile %q timestamps = (%q, %q); want both non-empty", p.Name, p.CreatedAt, p.UpdatedAt)
		}
	}
	if strings.Join(names, ",") != "apple,Mango,zebra" {
		t.Errorf("sorted names = %v; want [apple Mango zebra]", names)
	}
}

func TestCreateOutputProfileTrimmingAndFields(t *testing.T) {
	h := newTestHandler(t)
	dir := filepath.Join(t.TempDir(), "Books")
	body, _ := json.Marshal(OutputProfileRequest{
		Name: "  My Shelf  ", OutputDir: " " + dir + " ", OutputScheme: " {author}/{title} ",
	})
	req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateOutputProfile(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d — body: %s", w.Code, w.Body.String())
	}
	var p struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		OutputDir    string `json:"output_dir"`
		OutputScheme string `json:"output_scheme"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "My Shelf" || p.OutputDir != dir || p.OutputScheme != "{author}/{title}" {
		t.Errorf("stored profile = %+v; want trimmed values", p)
	}
	if p.ID <= 0 {
		t.Errorf("id = %d; want positive", p.ID)
	}
	if p.CreatedAt == "" || p.UpdatedAt == "" {
		t.Errorf("create response timestamps = (%q, %q); want both non-empty", p.CreatedAt, p.UpdatedAt)
	}
}

func TestCreateOutputProfileDuplicateName409(t *testing.T) {
	h := newTestHandler(t)
	mk := func(name, dir string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(OutputProfileRequest{Name: name, OutputDir: dir})
		req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.CreateOutputProfile(w, req)
		return w
	}
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	if w := mk("Shelf", a); w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d — body: %s", w.Code, w.Body.String())
	}
	// Case-insensitive UNIQUE (COLLATE NOCASE) is authoritative: "shelf"
	// must 409, and so must a concurrently-created identical name.
	if w := mk("  shelf  ", b); w.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d; want 409 — body: %s", w.Code, w.Body.String())
	}
	var n int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM output_profiles").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("profile count = %d; want 1 after rejected duplicate", n)
	}
}

func TestCreateOutputProfileValidationErrors(t *testing.T) {
	h := newTestHandler(t)
	cases := []struct {
		name string
		body OutputProfileRequest
		want int
		blob string
	}{
		{"blank name", OutputProfileRequest{Name: "  ", OutputDir: "/a"}, http.StatusBadRequest, "name is required"},
		{"both empty", OutputProfileRequest{Name: "N"}, http.StatusBadRequest, "set an output directory, a path format, or both"},
		{"relative dir", OutputProfileRequest{Name: "N", OutputDir: "media"}, http.StatusBadRequest, "must be an absolute path"},
		{"absolute scheme", OutputProfileRequest{Name: "N", OutputScheme: "/abs/{title}"}, http.StatusBadRequest, "must not start with '/'"},
		{"dotdot scheme", OutputProfileRequest{Name: "N", OutputScheme: "../x/{title}"}, http.StatusBadRequest, "'..' path segments"},
		{"placeholder-free scheme", OutputProfileRequest{Name: "N", OutputScheme: "plain"}, http.StatusBadRequest, "placeholder"},
		// A directory that does not exist yet is accepted as long as the
		// nearest existing ancestor is writable: the job creates it later.
		{"nonexistent dir under writable parent is valid", OutputProfileRequest{Name: "N", OutputDir: filepath.Join(t.TempDir(), "new", "shelf")}, http.StatusCreated, ""},
		{"nonexistent dir under read-only parent rejected", OutputProfileRequest{Name: "N", OutputDir: filepath.Join(readOnlyTestDir(t), "shelf")}, http.StatusBadRequest, "is not writable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
			w := httptest.NewRecorder()
			h.CreateOutputProfile(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d; want %d — body: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.blob != "" && !strings.Contains(w.Body.String(), tc.blob) {
				t.Errorf("body = %q; want it to contain %q", w.Body.String(), tc.blob)
			}
		})
	}
}

func TestUpdateOutputProfilePersistsAndRefreshes(t *testing.T) {
	h := newTestHandler(t)
	first := filepath.Join(t.TempDir(), "one")
	second := filepath.Join(t.TempDir(), "two")
	body, _ := json.Marshal(OutputProfileRequest{Name: "One", OutputDir: first})
	req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateOutputProfile(w, req)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if created.ID != 1 {
		t.Fatalf("created id = %d; test assumes 1", created.ID)
	}

	upd, _ := json.Marshal(OutputProfileRequest{Name: "One", OutputDir: second, OutputScheme: "F/{title}"})
	req = httptest.NewRequest("PUT", "/api/output-profiles/1", bytes.NewReader(upd))
	req = req.WithContext(profileRoute(t, "1"))
	w = httptest.NewRecorder()
	h.UpdateOutputProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d — body: %s", w.Code, w.Body.String())
	}
	var updated models.OutputProfile
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v (body %s)", err, w.Body.String())
	}
	if updated.CreatedAt == "" || updated.UpdatedAt == "" {
		t.Errorf("update response timestamps = (%q, %q); want both non-empty", updated.CreatedAt, updated.UpdatedAt)
	}
	var name, dir, scheme string
	if err := h.svc.DB.QueryRow("SELECT name, output_dir, output_scheme FROM output_profiles WHERE id = 1").
		Scan(&name, &dir, &scheme); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if name != "One" || dir != second || scheme != "F/{title}" {
		t.Errorf("updated profile = (%q, %q, %q); want (One, /two, F/{title})", name, dir, scheme)
	}
}

func TestUpdateOutputProfileUnknownAndInvalidID(t *testing.T) {
	h := newTestHandler(t)
	upd, _ := json.Marshal(OutputProfileRequest{Name: "X", OutputDir: "/x"})
	for id, want := range map[string]int{"99": http.StatusNotFound, "not-a-number": http.StatusBadRequest, "-4": http.StatusBadRequest} {
		req := httptest.NewRequest("PUT", "/api/output-profiles/"+id, bytes.NewReader(bytes.Clone(upd)))
		req = req.WithContext(profileRoute(t, id))
		w := httptest.NewRecorder()
		h.UpdateOutputProfile(w, req)
		if w.Code != want {
			t.Errorf("update id=%q status = %d; want %d — body: %s", id, w.Code, want, w.Body.String())
		}
	}
}

func TestDeleteOutputProfileUnknownAndInvalidID(t *testing.T) {
	h := newTestHandler(t)
	for id, want := range map[string]int{"99": http.StatusNotFound, "abc": http.StatusBadRequest} {
		req := httptest.NewRequest("DELETE", "/api/output-profiles/"+id, nil)
		req = req.WithContext(profileRoute(t, id))
		w := httptest.NewRecorder()
		h.DeleteOutputProfile(w, req)
		if w.Code != want {
			t.Errorf("delete id=%q status = %d; want %d — body: %s", id, w.Code, want, w.Body.String())
		}
	}

	// Valid delete: create then delete -> 204, then 404 on re-delete.
	body, _ := json.Marshal(OutputProfileRequest{Name: "Doomed", OutputDir: filepath.Join(t.TempDir(), "d")})
	req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateOutputProfile(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d — body: %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("DELETE", "/api/output-profiles/1", nil)
	req = req.WithContext(profileRoute(t, "1"))
	w = httptest.NewRecorder()
	h.DeleteOutputProfile(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204 — body: %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("DELETE", "/api/output-profiles/1", nil)
	req = req.WithContext(profileRoute(t, "1"))
	w = httptest.NewRecorder()
	h.DeleteOutputProfile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("re-delete status = %d; want 404 — body: %s", w.Code, w.Body.String())
	}
}

// TestOutputProfileWriteAccess covers the save-time rule: a submitted
// destination the server cannot create subdirectories in is a 400 naming the
// path, while an unchanged stored value is not re-probed (a mount that went
// away must not block renaming the profile).
func TestOutputProfileWriteAccess(t *testing.T) {
	h := newTestHandler(t)
	locked := readOnlyTestDir(t)
	blocked := filepath.Join(locked, "books")

	body, _ := json.Marshal(OutputProfileRequest{Name: "Locked", OutputDir: blocked})
	req := httptest.NewRequest("POST", "/api/output-profiles", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateOutputProfile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with unwritable dir status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
	for _, want := range []string{blocked, "is not writable", "permission denied"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body = %q; want it to contain %q", w.Body.String(), want)
		}
	}
	var n int
	if err := h.svc.DB.QueryRow("SELECT COUNT(*) FROM output_profiles").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("profile count = %d; want 0 after a rejected create", n)
	}

	// Seed a profile directly so the stored output_dir can point at a path
	// that is no longer writable (the mount-went-away case).
	if _, err := h.svc.DB.Exec(`INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES ('Locked', ?, '')`, blocked); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	upd, _ := json.Marshal(OutputProfileRequest{Name: "Renamed", OutputDir: blocked})
	req = httptest.NewRequest("PUT", "/api/output-profiles/1", bytes.NewReader(upd))
	req = req.WithContext(profileRoute(t, "1"))
	w = httptest.NewRecorder()
	h.UpdateOutputProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unchanged unwritable dir status = %d; want 200 (rename must stay possible) — body: %s", w.Code, w.Body.String())
	}

	moved, _ := json.Marshal(OutputProfileRequest{Name: "Renamed", OutputDir: filepath.Join(locked, "other")})
	req = httptest.NewRequest("PUT", "/api/output-profiles/1", bytes.NewReader(moved))
	req = req.WithContext(profileRoute(t, "1"))
	w = httptest.NewRecorder()
	h.UpdateOutputProfile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("changed unwritable dir status = %d; want 400 — body: %s", w.Code, w.Body.String())
	}
}
