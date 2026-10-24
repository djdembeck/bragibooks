package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCoverHandlerServesExtractedCoversOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "7.jpg"), []byte("\xff\xd8\xff\xe0jpeg"), 0o644); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	handler := CoverHandler(dir)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"existing id cover", "/covers/7.jpg", http.StatusOK},
		{"unknown id", "/covers/8.jpg", http.StatusNotFound},
		{"wrong extension", "/covers/secret.txt", http.StatusNotFound},
		{"name traversal", "/covers/../7.jpg", http.StatusNotFound},
		{"empty name", "/covers/", http.StatusNotFound},
		{"non-numeric id", "/covers/abc.jpg", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("%s status = %d; want %d — body: %s", tc.path, w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				if got := w.Header().Get("Content-Type"); got != "image/jpeg" {
					t.Errorf("content-type = %q; want image/jpeg", got)
				}
			}
		})
	}
}

func TestCoverHandlerDisabledWithoutDirectory(t *testing.T) {
	handler := CoverHandler("")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/covers/7.jpg", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}
