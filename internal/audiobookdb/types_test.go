package audiobookdb

import (
	"encoding/json"
	"testing"
)

// TestReleaseDecodesLanguageAndPublishers is the repro+regression for
// issues #519 and #552: upstream sends a release language as a name-only
// reference and publisher credits as a `publishers` array with the
// singular `publisher` key null. Binding the language to a {id,title}
// struct or the publisher to the singular key decoded both as empty, so
// the Match page rendered "Language Unavailable"/"Publisher Unavailable"
// for releases that carry them.
func TestReleaseDecodesLanguageAndPublishers(t *testing.T) {
	// Trimmed from GET https://audiobookdb.org/api/releases/Bq93nTyJUUbd
	// with include=...,language,people,publishers.
	payload := []byte(`{
		"id": "Bq93nTyJUUbd",
		"title": "Accidental Astronaut 2",
		"publisher": null,
		"publishers": [{"id": "jwN6LnzQi4kQ", "name": "Podium Audio", "position": 0, "creditedName": "Podium Audio"}],
		"language": {"name": "English"}
	}`)

	var release Release
	if err := json.Unmarshal(payload, &release); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	if release.Language.Name != "English" {
		t.Errorf("language = %q; want English", release.Language.Name)
	}
	if got := release.PublisherName(); got != "Podium Audio" {
		t.Errorf("publisher = %q; want Podium Audio", got)
	}
}

// TestReleasePublisherName covers the display string built from the
// publishers array: upstream order is kept, blank entries are skipped, and
// multiple credited publishers are joined.
func TestReleasePublisherName(t *testing.T) {
	tests := []struct {
		name       string
		publishers []IdName
		want       string
	}{
		{name: "none", publishers: nil, want: ""},
		{name: "single", publishers: []IdName{{Name: "Podium Audio"}}, want: "Podium Audio"},
		{name: "blank entries skipped", publishers: []IdName{{Name: "  "}, {Name: "Podium Audio"}}, want: "Podium Audio"},
		{name: "multiple keep order", publishers: []IdName{{Name: "Podium Audio"}, {Name: "Second Press"}}, want: "Podium Audio, Second Press"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			release := &Release{Publishers: tc.publishers}
			if got := release.PublisherName(); got != tc.want {
				t.Errorf("PublisherName() = %q; want %q", got, tc.want)
			}
		})
	}
	var nilRelease *Release
	if got := nilRelease.PublisherName(); got != "" {
		t.Errorf("nil release PublisherName() = %q; want empty", got)
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
		{"fractional value integer label", `{"value":1.0,"label":"1"}`, 1},
		{"fractional value only", `{"value":1.5,"label":"1.5"}`, 0},
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
