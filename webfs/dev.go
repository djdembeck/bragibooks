//go:build !webui

package webfs

import "net/http"

// Handler returns a fallback handler for dev mode (no embedded SPA).
// In dev mode, the frontend is served by Vite via reverse proxy.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
}