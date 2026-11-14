package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUpdateSettingsNoneSelectedFieldsRoundTrip pins the #518 none-selected
// distinction: an explicit empty metadata_fields array is "write nothing"
// (a non-nil empty selection), NOT the nil default all-supported-available.
// A save of [] must survive the DB + YAML + live-config round trip so the
// next queued job freezes a none-selected policy (all preserve, no cover,
// no chapter sidecar) rather than silently falling back to writing every
// supported field.
func TestUpdateSettingsNoneSelectedFieldsRoundTrip(t *testing.T) {
	h := newSettingsTestService(t, newSettingsTestDB(t))
	r := buildSettingsRouter(h)

	put := func(body string) *httptestResponse {
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out := map[string]any{}
		if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatalf("decode response: %v (%s)", err, data)
			}
		}
		return &httptestResponse{code: rec.Code, body: out}
	}

	resp := put(`{"metadata_fields":[],"metadata_chapters":"preserve"}`)
	if resp.code != 200 {
		t.Fatalf("PUT empty selection: want 200, got %d: %v", resp.code, resp.body)
	}

	// The PUT response itself must report the saved selection as a non-nil
	// empty list, not null (the nil default).
	if _, present := resp.body["metadata_fields"]; !present {
		t.Fatalf("PUT response missing metadata_fields: %v", resp.body)
	}
	// Re-serialize to a raw string so we can distinguish [] from null.
	raw, _ := json.Marshal(resp.body["metadata_fields"])
	if string(raw) != "[]" {
		t.Errorf("PUT response metadata_fields: want [], got %s", raw)
	}

	// A subsequent GET must agree: the live selection is still the explicit
	// empty list.
	req := httptest.NewRequest("GET", "/api/settings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var getBody map[string]any
	if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
		json.Unmarshal(data, &getBody)
	}
	getRaw, _ := json.Marshal(getBody["metadata_fields"])
	if string(getRaw) != "[]" {
		t.Errorf("GET metadata_fields: want [], got %s", getRaw)
	}
}

type httptestResponse struct {
	code int
	body map[string]any
}

// storedString renders a stored field selection the way the DB row does:
// null for the unset default, a JSON array otherwise.
func storedString(fields *[]string) string {
	if fields == nil {
		return "null"
	}
	b, _ := json.Marshal(*fields)
	return string(b)
}

// TestUpdateSettingsOmittedFieldsRetainsSelection pins the partial-update
// semantics: a PUT that does not send metadata_fields must leave the saved
// selection untouched — a saved explicit selection (including the none-
// selected empty list) survives unrelated setting saves. The zero/absent
// value is the default all-supported-available baseline; an omitted key is
// not a reset.
func TestUpdateSettingsOmittedFieldsRetainsSelection(t *testing.T) {
	h := newSettingsTestService(t, newSettingsTestDB(t))
	r := buildSettingsRouter(h)

	// Save an explicit title-only selection.
	putTitle := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"metadata_fields":["title"],"metadata_chapters":"preserve"}`))
	putTitle.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, putTitle)
	if rec.Code != 200 {
		t.Fatalf("PUT title-only: %d", rec.Code)
	}

	// An unrelated save that OMITS metadata_fields must not clear it.
	omit := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"num_cpus":2}`))
	omit.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, omit)
	if rec2.Code != 200 {
		t.Fatalf("PUT num_cpus: %d", rec2.Code)
	}

	req := httptest.NewRequest("GET", "/api/settings", nil)
	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, req)
	var body map[string]any
	if data, _ := io.ReadAll(rec3.Result().Body); len(data) > 0 {
		json.Unmarshal(data, &body)
	}
	raw, _ := json.Marshal(body["metadata_fields"])
	if string(raw) != `["title"]` {
		t.Errorf("omitted metadata_fields after unrelated save: want [\"title\"], got %s (the selection must be retained, not reset)", raw)
	}
}

// TestUpdateSettingsIgnoresPinnedMetadataSelection pins the env-pin
// contract: while PROCESSING_METADATA_FIELDS / PROCESSING_METADATA_CHAPTERS
// are set, a PUT of the pinned value must NOT persist it as if it had taken
// effect (env outranks the settings stores; the pin stays authoritative for
// jobs). The pinned key is dropped from the save; unrelated settings in the
// same PUT still land; GET reports which side is pinned so the UI renders
// those controls read-only. Same env-managed contract as the API endpoint.
func TestUpdateSettingsIgnoresPinnedMetadataSelection(t *testing.T) {
	h := newSettingsTestService(t, newSettingsTestDB(t))
	r := buildSettingsRouter(h)

	put := func(body string) *httptestResponse {
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
			json.Unmarshal(data, &out)
		}
		return &httptestResponse{code: rec.Code, body: out}
	}

	// Baseline: unpinned, the selection saves.
	if resp := put(`{"metadata_fields":["title"],"metadata_chapters":"preserve"}`); resp.code != 200 {
		t.Fatalf("unpinned PUT: want 200, got %d: %v", resp.code, resp.body)
	}

	// GET reports neither side pinned.
	req := httptest.NewRequest("GET", "/api/settings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var getBody map[string]any
	if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
		json.Unmarshal(data, &getBody)
	}
	if getBody["metadata_fields_pinned"] != false || getBody["metadata_chapters_pinned"] != false {
		t.Errorf("unpinned GET: want both flags false, got %v / %v", getBody["metadata_fields_pinned"], getBody["metadata_chapters_pinned"])
	}

	// Both pinned: the metadata keys in the PUT are ignored (the pin stays
	// authoritative), the unrelated num_cpus in the same PUT still saves,
	// and the response reports the pin, not the submitted value.
	t.Run("bothPinned", func(t *testing.T) {
		t.Setenv("PROCESSING_METADATA_FIELDS", "title,subtitle")
		t.Setenv("PROCESSING_METADATA_CHAPTERS", "write")
		resp := put(`{"metadata_fields":["title"],"metadata_chapters":"preserve","num_cpus":2}`)
		if resp.code != 200 {
			t.Fatalf("pinned PUT: want 200 (unrelated keys still save), got %d: %v", resp.code, resp.body)
		}
		if resp.body["num_cpus"] != 2.0 {
			t.Errorf("unrelated num_cpus not saved alongside the pin: %v", resp.body["num_cpus"])
		}
		// The response must NOT echo the submitted (non-pinned) values as
		// if they had been applied; the live config carries the pin.
		raw, _ := json.Marshal(resp.body["metadata_chapters"])
		if string(raw) != `"write"` {
			t.Errorf("pinned PUT response metadata_chapters: want the pin (write), got %s", raw)
		}
		req := httptest.NewRequest("GET", "/api/settings", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var getBody map[string]any
		if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
			json.Unmarshal(data, &getBody)
		}
		if getBody["metadata_fields_pinned"] != true || getBody["metadata_chapters_pinned"] != true {
			t.Errorf("pinned GET: want both flags true, got %v / %v", getBody["metadata_fields_pinned"], getBody["metadata_chapters_pinned"])
		}
		// The live config must carry the pin values, not the submitted
		// (non-pinned) ones (ReassertMetadataPins re-applies them after
		// the store swap).
		raw, _ = json.Marshal(getBody["metadata_fields"])
		if string(raw) != `["title","subtitle"]` {
			t.Errorf("live config fields with pins: want the pin [title,subtitle], got %s", raw)
		}
		raw, _ = json.Marshal(getBody["metadata_chapters"])
		if string(raw) != `"write"` {
			t.Errorf("live config chapters with pins: want the pin (write), got %s", raw)
		}
		// The persisted DB row must keep the BASELINE saved values: the
		// pinned keys are dropped before the save, so the pin must NOT
		// leak into the store, and neither must the submitted values.
		var storedFields, storedChapters string
		if err := h.svc.DB.QueryRow(
			`SELECT metadata_fields, metadata_chapters FROM settings WHERE id = 1`,
		).Scan(&storedFields, &storedChapters); err != nil {
			t.Fatalf("read settings row: %v", err)
		}
		if string(storedFields) != `["title"]` {
			t.Errorf("pinned save leaked into DB row: want the baseline [\"title\"], got %s", storedFields)
		}
		if storedChapters != "preserve" {
			t.Errorf("pinned save leaked into DB row: want the baseline preserve, got %s", storedChapters)
		}

		// A malformed value for the pinned key must not 400 the save of an
		// unrelated key (the pinned key is dropped before validation).
		resp = put(`{"num_cpus":4,"metadata_fields":["bogus_field"],"metadata_chapters":"not-a-policy"}`)
		if resp.code != 200 {
			t.Fatalf("malformed pinned values + unrelated key: want 200, got %d: %v", resp.code, resp.body)
		}
		if resp.body["num_cpus"] != 4.0 {
			t.Errorf("unrelated num_cpus not saved alongside malformed pinned values: %v", resp.body["num_cpus"])
		}
		// A second save while the pin is active must NOT leak the pin (now
		// riding in the live config) into the durable stores: the live
		// config carries the pin, but the row and the config file keep the
		// baseline saved values.
		if err := h.svc.DB.QueryRow(
			`SELECT metadata_fields, metadata_chapters FROM settings WHERE id = 1`,
		).Scan(&storedFields, &storedChapters); err != nil {
			t.Fatalf("read settings row after second save: %v", err)
		}
		if string(storedFields) != `["title"]` {
			t.Errorf("pin leaked into DB row on second save: want the baseline [\"title\"], got %s", storedFields)
		}
		if storedChapters != "preserve" {
			t.Errorf("pin leaked into DB row on second save: want the baseline preserve, got %s", storedChapters)
		}
		if gotF, gotC, _ := h.svc.Config.StoredMetadataSelection(); storedString(gotF) != `["title"]` || gotC != "preserve" {
			t.Errorf("pin leaked into config file on second save: want [\"title\"]/preserve, got %s/%s", storedString(gotF), gotC)
		}
		// The live config still carries the pin (env outranks the stores).
		live := h.svc.Config.Config()
		if live.Processing.MetadataChapters != "write" {
			t.Errorf("live config lost the chapter pin: want write, got %s", live.Processing.MetadataChapters)
		}
		if gotF := storedString(live.Processing.MetadataFields); gotF != `["title","subtitle"]` {
			t.Errorf("live config lost the fields pin: want [title,subtitle], got %s", gotF)
		}
	})

	// Only the chapter value pinned: the field selection in the same PUT
	// still saves; the chapter value is ignored.
	t.Run("chapterPinned", func(t *testing.T) {
		t.Setenv("PROCESSING_METADATA_CHAPTERS", "write")
		resp := put(`{"metadata_fields":["title"],"metadata_chapters":"preserve"}`)
		if resp.code != 200 {
			t.Fatalf("PUT with only the chapter pin: want 200, got %d: %v", resp.code, resp.body)
		}
		raw, _ := json.Marshal(resp.body["metadata_chapters"])
		if string(raw) != `"write"` {
			t.Errorf("chapter-pinned PUT response: want the pin (write), got %s", raw)
		}
		// The fields selection DID take effect.
		req := httptest.NewRequest("GET", "/api/settings", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var getBody map[string]any
		if data, _ := io.ReadAll(rec.Result().Body); len(data) > 0 {
			json.Unmarshal(data, &getBody)
		}
		raw, _ = json.Marshal(getBody["metadata_fields"])
		if string(raw) != `["title"]` {
			t.Errorf("fields selection with only the chapter pin: want [title] saved, got %s", raw)
		}
		// Direct DB row check: the unpinned fields selection landed, the
		// pinned chapter value did NOT (the row keeps the baseline).
		var storedFields, storedChapters string
		if err := h.svc.DB.QueryRow(
			`SELECT metadata_fields, metadata_chapters FROM settings WHERE id = 1`,
		).Scan(&storedFields, &storedChapters); err != nil {
			t.Fatalf("read settings row: %v", err)
		}
		if string(storedFields) != `["title"]` {
			t.Errorf("unpinned fields selection not persisted with chapter pin: want [\"title\"], got %s", storedFields)
		}
		if storedChapters != "preserve" {
			t.Errorf("chapter pin leaked into DB row: want the baseline preserve, got %s", storedChapters)
		}
	})
}
