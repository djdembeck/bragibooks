package audiobookdb

import (
	"encoding/json"
	"strconv"
)

// Book represents an audiobookdb book entity.
type Book struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	// Subtitle and Disambiguation feed the snapshot's subtitle capture with
	// the engine's own book-fallback, disambiguation-last precedence (the
	// selected release's own subtitle wins; see captureSnapshot).
	Subtitle              *string              `json:"subtitle"`
	Disambiguation        *string              `json:"disambiguation"`
	Type                  *string              `json:"type"`
	OriginallyPublishedAt *string              `json:"originallyPublishedAt"`
	Images                []Image              `json:"images"`
	People                []PersonRoleRelation `json:"people"`
	Releases              []IdTitle            `json:"releases"`
	Series                []BookInSeries       `json:"series"`
	External              []ExternalRelation   `json:"external"`
	Genres                []IdTitle            `json:"genres"`
	Tags                  []IdTitle            `json:"tags"`
}

// Release represents an audiobookdb release entity.
type Release struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Subtitle is the release's own subtitle; it wins over the book-level
	// value in the snapshot's subtitle capture (the engine's documented
	// release-first precedence).
	Subtitle         string               `json:"subtitle"`
	Duration         string               `json:"duration"`
	RuntimeLengthMs  int                  `json:"runtimeLengthMs"`
	RuntimeLengthSec int                  `json:"runtimeLengthSec"`
	ISBN             *string              `json:"isbn"`
	ReleaseDate      *string              `json:"releaseDate"`
	ChapterDetail    *ChapterDetail       `json:"chapterDetail"`
	People           []PersonRoleRelation `json:"people"`
	Publisher        IdName               `json:"publisher"`
	// Upstream sends the release language as a name-only reference
	// ({"name":"English"}), not the {id,title} shape used by genres,
	// tags, and series.
	Language NameRef `json:"language"`
	Images   []Image `json:"images"`
}

// ChapterDetail wraps the chapters array on a release.
type ChapterDetail struct {
	Chapters []Chapter `json:"chapters"`
}

// Chapter represents a single chapter within an audiobook release.
type Chapter struct {
	Title         string `json:"title"`
	Ordinal       int    `json:"ordinal"`
	StartOffsetMs int64  `json:"startOffsetMs"`
	LengthMs      int64  `json:"lengthMs"`
	LengthString  string `json:"lengthString"`
}

// PersonRoleRelation links a person to their role on a book or release.
type PersonRoleRelation struct {
	Role   RoleRef   `json:"role"`
	Person PersonRef `json:"person"`
}

// RoleRef identifies a role by name (e.g. "Author", "Narrator").
type RoleRef struct {
	Name string `json:"name"`
}

// PersonRef identifies a person by name and audiobookdb ID.
type PersonRef struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Image represents a cover image or other media asset.
type Image struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// IdTitle is a lightweight reference with an ID and title.
type IdTitle struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// IdName is a lightweight reference with an ID and name.
type IdName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NameRef is a name-only reference, used where upstream sends just a label
// (for example a release's language).
type NameRef struct {
	Name string `json:"name"`
}

// BookInSeries indicates a book's position within a series. Upstream
// sends Position as either a plain integer or an object
// ({"value":"1","label":"1",...}) depending on the record; a box set or
// omnibus carries a range instead ("1-4"), which has no single integer
// position and decodes as 0.
type BookInSeries struct {
	SeriesId string  `json:"seriesId"`
	Position flexInt `json:"position"`
}

// flexInt accepts a JSON number or an object whose "value"/"label" field is
// a number or string-encoded number, as upstream series positions vary. A
// position that is not a plain integer (a "1-4" range, a null, an
// unexpected shape) decodes as 0: one odd series record must not fail the
// enclosing book.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	*f = 0
	// A bare number or string position.
	var flat flexText
	if err := json.Unmarshal(data, &flat); err == nil && flat != "" {
		if n, ok := flexIntOf(flat); ok {
			*f = n
		}
		return nil
	}
	// An object position: {"value":"1","label":"1","kind":"range",...}.
	var obj struct {
		Value flexText `json:"value"`
		Label flexText `json:"label"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	// value wins over label, but an unparseable value falls through to the
	// label ({"value":1.0,"label":"1"}) rather than pinning the position to
	// the zero fallback.
	for _, cand := range []flexText{obj.Value, obj.Label} {
		if cand == "" {
			continue
		}
		if n, ok := flexIntOf(cand); ok {
			*f = n
			return nil
		}
	}
	return nil
}

// flexText keeps a JSON string or number as its literal text; any other
// JSON value (object, array, bool, null) decodes as empty.
type flexText string

func (t *flexText) UnmarshalJSON(data []byte) error {
	*t = ""
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*t = flexText(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*t = flexText(n)
		return nil
	}
	return nil
}

// flexIntOf parses a plain integer. The bool is false when the text is not
// an integer (e.g. the "1-4" range of a box set, or a fractional 1.0).
func flexIntOf(t flexText) (flexInt, bool) {
	n, err := strconv.Atoi(string(t))
	if err != nil {
		return 0, false
	}
	return flexInt(n), true
}

// ExternalRelation links to an external resource (Goodreads, etc.).
type ExternalRelation struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	URL  string `json:"url"`
}

// SearchResponse wraps the paginated search results.
type SearchResponse struct {
	Results []SearchHit `json:"results"`
}

// SearchHit is a single result row from a search query.
type SearchHit struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
