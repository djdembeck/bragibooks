package audiobookdb

import (
	"database/sql"
	"encoding/json"
)

// Book represents an audiobookdb book entity.
type Book struct {
	ID                    string               `json:"id"`
	Title                 string               `json:"title"`
	Description           sql.NullString       `json:"description"`
	Disambiguation        sql.NullString       `json:"disambiguation"`
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
	ID               string               `json:"id"`
	Title            string               `json:"title"`
	Duration         string               `json:"duration"`
	RuntimeLengthMs  int                  `json:"runtimeLengthMs"`
	RuntimeLengthSec int                  `json:"runtimeLengthSec"`
	ISBN             sql.NullString       `json:"isbn"`
	ReleaseDate      sql.NullString       `json:"releaseDate"`
	ChapterDetail    *ChapterDetail       `json:"chapterDetail"`
	People           []PersonRoleRelation `json:"people"`
	Publisher        IdName               `json:"publisher"`
	Language         IdTitle              `json:"language"`
	Images           []Image              `json:"images"`
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

// BookInSeries indicates a book's position within a series.
type BookInSeries struct {
	SeriesId string `json:"seriesId"`
	Position int    `json:"position"`
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