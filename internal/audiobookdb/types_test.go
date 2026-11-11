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
