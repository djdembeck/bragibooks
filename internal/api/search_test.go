package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bragibooks/bragibooks/internal/audiobookdb"
)

func asinHandlerRequest(t *testing.T, asin string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/search/by-asin/"+asin, nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Keys = []string{"asin"}
	ctx.URLParams.Values = []string{asin}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

func TestGetBookByASINHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/audiobooks/external/audible/B00B5HZGUG":
			fmt.Fprint(w, `{"id":"WQQDcMBQ3qUT","title":"The Martian"}`)
		case "/audiobooks/external/audible/NOPE000000":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[{"status":"404","title":"Not Found","detail":"not found"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")

	h := newTestHandler(t)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	t.Run("found", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.GetBookByASIN(w, asinHandlerRequest(t, "B00B5HZGUG"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200: %s", w.Code, w.Body)
		}
		var book map[string]any
		json.Unmarshal(w.Body.Bytes(), &book)
		if book["id"] != "WQQDcMBQ3qUT" {
			t.Errorf("book id = %v", book["id"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.GetBookByASIN(w, asinHandlerRequest(t, "NOPE000000"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404: %s", w.Code, w.Body)
		}
	})
}

// TestGetBookByASINHandlerRejectsInvalidASIN pins the URL-injection guard:
// chi decodes %2F and ? in path parameters, so without validation a
// structurally-altering value would change the upstream request that
// carries the API key. The handler must answer 400 without reaching
// upstream.
func TestGetBookByASINHandlerRejectsInvalidASIN(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"WQQDcMBQ3qUT","title":"The Martian"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")

	h := newTestHandler(t)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	for _, asin := range []string{
		"b00b5hzgug",     // lowercase
		"B00B5HZGU",      // 9 chars
		"B00B5HZGUGG",    // 11 chars
		"B00B5HZG%2FG",   // decoded slash
		"B00B5HZGUG?x=1", // decoded query char
	} {
		w := httptest.NewRecorder()
		h.GetBookByASIN(w, asinHandlerRequest(t, asin))
		if w.Code != http.StatusBadRequest {
			t.Errorf("asin %q: status = %d; want 400: %s", asin, w.Code, w.Body)
		}
	}
	if hits != 0 {
		t.Errorf("upstream hits = %d; want 0 (validation must happen before the request)", hits)
	}
}

// TestGetBookByASINHandlerMapsUpstream5xxTo502 pins the upstream-failure
// mapping: every non-404 client error (500, 503, …) must surface as a 502
// Bad Gateway. A 404 would be read by the UI as "not in catalog" and would
// silently fall back to title search on a transient upstream outage.
func TestGetBookByASINHandlerMapsUpstream5xxTo502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/audiobooks/external/audible/FAIL500000":
			w.WriteHeader(http.StatusInternalServerError)
		case "/audiobooks/external/audible/FAIL503000":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprint(w, `{"errors":[{"detail":"upstream failure"}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")

	h := newTestHandler(t)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	for _, asin := range []string{"FAIL500000", "FAIL503000"} {
		t.Run(asin, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.GetBookByASIN(w, asinHandlerRequest(t, asin))
			if w.Code != http.StatusBadGateway {
				t.Fatalf("upstream %s: status = %d; want 502 (and not 404): %s", asin, w.Code, w.Body)
			}
		})
	}
}

// TestGetBookByASINHandlerRequiresASIN pins the empty-parameter branch: an
// empty route parameter answers 400 before any upstream request is made.
func TestGetBookByASINHandlerRequiresASIN(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"WQQDcMBQ3qUT","title":"The Martian"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BRAGIBOOKS_ALLOW_LOCAL_ADB", "1")

	h := newTestHandler(t)
	av := &syncValue{}
	av.Store(audiobookdb.NewClient("k", srv.URL))
	h.svc.AudiobookDB = av

	w := httptest.NewRecorder()
	h.GetBookByASIN(w, asinHandlerRequest(t, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400: %s", w.Code, w.Body)
	}
	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "asin parameter is required" {
		t.Errorf("error = %q; want %q", errResp["error"], "asin parameter is required")
	}
	if hits != 0 {
		t.Errorf("upstream hits = %d; want 0 (empty param must be rejected before the request)", hits)
	}
}
