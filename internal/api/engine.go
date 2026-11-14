package api

import (
	"encoding/json"
	"net/http"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// EngineCapabilities serves the verified engine --capabilities document:
// the schema/resolver identity, the path-format token vocabulary (with the
// engine's own labels and examples), and the supported policy value sets.
// The Settings picker and every path-format example render from this; the
// server owns the token registry, the UI owns no parallel one.
func (h *Handler) EngineCapabilities(w http.ResponseWriter, r *http.Request) {
	caps := h.svc.Processor.Capabilities()
	if caps == nil {
		// The startup probe is fatal on failure, so nil means "not run
		// yet" (tests), not a broken engine: refuse, do not fabricate.
		writeError(w, http.StatusServiceUnavailable, "engine capabilities not verified")
		return
	}
	writeJSON(w, http.StatusOK, caps)
}

// enginePreviewRequest is the normalized preview payload: EXACTLY the
// snapshot and path format that enqueue would use, so a preview can never
// diverge from what the job will run. Supplied mode is offline: no catalog
// request is made, which keeps Match previews working with the catalog
// unavailable.
type enginePreviewRequest struct {
	Metadata   audio.SuppliedMetadata `json:"metadata"`
	PathFormat string                 `json:"path_format"`
}

// EnginePreview runs the engine's non-mutating job preview for the caller's
// normalized selected/edited snapshot and relative path format. It returns
// the exact sanitized relative output path and the effective metadata the
// engine resolved. The preview path is truth about NAMING only — it does not
// publish files, reserve names, or create directories.
func (h *Handler) EnginePreview(w http.ResponseWriter, r *http.Request) {
	var req enginePreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid preview request: "+err.Error())
		return
	}
	if h.svc.Processor.Capabilities() == nil {
		writeError(w, http.StatusServiceUnavailable, "engine capabilities not verified")
		return
	}
	// The token registry is capability-driven: an authored format the
	// engine would reject (unknown braces are literal text, so the check
	// is "no recognized token at all") is refused before the probe.
	if req.PathFormat != "" {
		if err := audio.ValidateRelativePathFormat(req.PathFormat); err != nil {
			writeError(w, http.StatusBadRequest, "invalid path format: "+err.Error())
			return
		}
	}
	resp, err := h.svc.Processor.Preview(r.Context(), req.Metadata, req.PathFormat)
	if err != nil {
		writeError(w, http.StatusBadGateway, "engine preview failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
