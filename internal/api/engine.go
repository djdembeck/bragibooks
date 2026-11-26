package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
// maxEnginePreviewBodyBytes bounds a preview request: the payload is one
// normalized snapshot (a handful of metadata arrays) — an unbounded body
// decoded before any validation would turn the preview endpoint into a
// memory amplifier.
const maxEnginePreviewBodyBytes = 1 << 20

func (h *Handler) EnginePreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxEnginePreviewBodyBytes)
	// Read the FULL bounded body before unmarshalling: a decoder stops at
	// the first JSON value, so a valid preview object with trailing data
	// would slip past the cap (the 413 must cover the whole request).
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "preview request body exceeds the limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid preview request: "+err.Error())
		return
	}
	var req enginePreviewRequest
	if err := json.Unmarshal(body, &req); err != nil {
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
	// Bound the preview subprocess: a concurrency slot (the cap is small —
	// previews are short, non-mutating, and off the worker pool) and a
	// server-side deadline (r.Context() alone only fires when the client
	// gives up, so a client holding its connection open would pin a
	// slow/stuck engine for as long as it wished). The slot is released
	// when the preview returns, even on the deadline path.
	select {
	case h.previewSlots <- struct{}{}:
		defer func() { <-h.previewSlots }()
	default:
		writeError(w, http.StatusTooManyRequests, "too many concurrent engine previews; retry shortly")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), enginePreviewDeadline)
	defer cancel()
	resp, err := h.svc.Processor.Preview(ctx, req.Metadata, req.PathFormat)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "engine preview timed out")
			return
		}
		writeError(w, http.StatusBadGateway, "engine preview failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
