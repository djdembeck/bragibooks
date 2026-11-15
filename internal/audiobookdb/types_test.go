package audiobookdb

import (
	"encoding/json"
	"testing"
)

// TestReleaseDecodesLanguageName is the repro+regression for issue #519:
// upstream sends a release language as a name-only reference, so a
// {id,title} struct decoded every release language as empty and the Match
// page rendered "Language Unavailable" for releases that carry one.
func TestReleaseDecodesLanguageName(t *testing.T) {
	// Trimmed from GET https://audiobookdb.org/api/releases/Bq93nTyJUUbd
	// with include=...,language,people,publisher.
	payload := []byte(`{
		"id": "Bq93nTyJUUbd",
		"title": "Accidental Astronaut 2",
		"publisher": {"id": "jwN6LnzQi4kQ", "name": "Podium Audio"},
		"language": {"name": "English"}
	}`)

	var release Release
	if err := json.Unmarshal(payload, &release); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	if release.Language.Name != "English" {
		t.Errorf("language = %q; want English", release.Language.Name)
	}
	if release.Publisher.Name != "Podium Audio" {
		t.Errorf("publisher = %q; want Podium Audio", release.Publisher.Name)
	}
}

// TestBookDecodesRangedSeriesPosition is the repro+regression for issue
// #528: a box-set/omnibus position arrives as a range object
// ({"value":"1-4","label":"1–4","kind":"range"}), which failed the whole
// Book decode and made every book fetch for that title a 502.
func TestBookDecodesRangedSeriesPosition(t *testing.T) {
	// Trimmed from GET https://audiobookdb.org/api/books/BWFkk3Vb9N83
	// with include=series.
	payload := []byte(`{
		"id": "BWFkk3Vb9N83",
		"title": "A Winter's Promise",
		"series": [
			{
				"ordinal": "1",
				"position": {
					"value": "1-4",
					"label": "1–4",
					"sortKey": 1,
					"kind": "range",
					"status": "complete",
					"spans": [{"start": 1, "end": 4}]
				},
				"series": {"id": "zVngdUDXgHNN", "title": "The Mirror Visitor Quartet"}
			}
		]
	}`)

	var book Book
	if err := json.Unmarshal(payload, &book); err != nil {
		t.Fatalf("unmarshal book: %v", err)
	}
	if book.Title != "A Winter's Promise" {
		t.Errorf("title = %q; want A Winter's Promise", book.Title)
	}
	if len(book.Series) != 1 {
		t.Fatalf("series entries = %d; want 1", len(book.Series))
	}
	if got := book.Series[0].Position; got != 0 {
		t.Errorf("ranged position = %d; want 0", got)
	}
}

// TestFlexIntDecodesUpstreamPositionForms covers the position shapes
// upstream sends: a bare number, a numeric/string scalar, and an object
// carrying value/label. A shape that is not a plain integer keeps 0
// instead of failing the enclosing decode.
func TestFlexIntDecodesUpstreamPositionForms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want flexInt
	}{
		{"number", `3`, 3},
		{"string", `"3"`, 3},
		{"object value", `{"value":"3","label":"3"}`, 3},
		{"object numeric value", `{"value":3,"label":3}`, 3},
		{"object label only", `{"label":"7"}`, 7},
		{"range", `{"value":"1-4","label":"1–4","kind":"range"}`, 0},
		{"string range", `"1-4"`, 0},
		{"missing", `{}`, 0},
		{"null", `null`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got flexInt
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("position = %d; want %d", got, tc.want)
			}
		})
	}
}
