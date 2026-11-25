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
