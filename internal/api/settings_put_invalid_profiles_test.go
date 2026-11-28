package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// TestUpdateSettingsResponseCarriesInvalidOutputProfiles pins the PUT
// response contract: the successful save body must carry
// invalid_output_profiles in the SAME shape as GET — empty array (never
// null) on a clean store, and the rejected profile verbatim when the
// store holds one. The non-empty half is reached through a REAL engine
// swap: the stored profile scheme passes the swap's candidate
// revalidation (which checks the token vocabulary, not the relative-path
// structure) but fails the relative-structure rule the post-swap
// refreshInvalidProfiles applies — so the swap lands and the response
// surfaces the re-derived verdict.
func TestUpdateSettingsResponseCarriesInvalidOutputProfiles(t *testing.T) {
	db := newSettingsTestDB(t)
	h := newSettingsTestService(t, db)
	SetBootInvalidProfiles(nil)
	t.Cleanup(func() { SetBootInvalidProfiles(nil) })
	// The accepted swap republishes the CANDIDATE's tokens into the
	// process-global live registry; restore whatever it held so the
	// suite's other tests see their expected starting state.
	prevTokens := audio.CapturedPathFormatTokens()
	t.Cleanup(func() { audio.SetPathFormatTokens(prevTokens) })

	// Clean store: the PUT response must carry the field as [] (not null,
	// and not absent — the client copies it from the response into the
	// visible repair list).
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"num_cpus":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	buildSettingsRouter(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings = %d — body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"invalid_output_profiles":[]`) {
		t.Fatalf("PUT response = %q; want invalid_output_profiles as an empty array, not null", rec.Body.String())
	}

	// Seed a profile whose scheme the candidate engine's token vocabulary
	// accepts but that is absolute (the structure rule refreshInvalidProfiles
	// enforces): the swap is accepted, and the post-swap re-derivation
	// records the profile as invalid.
	if _, err := db.Exec(`CREATE TABLE output_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL COLLATE NOCASE UNIQUE,
		output_dir TEXT NOT NULL DEFAULT '',
		output_scheme TEXT NOT NULL DEFAULT '',
		created_at TEXT DEFAULT (datetime('now')),
		updated_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO output_profiles (name, output_dir, output_scheme) VALUES ('Wide', '', '/books/{title}')"); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate-engine")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "m4b-merge 2.2.0"; exit 0; fi
if [ "$1" = "--capabilities" ]; then
  echo '{"schema_version":1,"resolver_version":"1","default_path_format":"{title}","path_tokens":[{"name":"book_id","value":"b","example":"B"},{"name":"authors","value":"a","example":"A"},{"name":"narrators","value":"n","example":"N"},{"name":"author","value":"a","example":"A"},{"name":"title","value":"t","example":"T"}],"field_policies":{"values":["write","preserve","clear"],"scalar_fields":["title","subtitle","authors","narrators","series","description","genres","year"]},"cover_policies":{"constraint":"str","variants":["write_url","preserve","clear"]},"chapter_policies":{"constraint":"str","variants":["write","preserve","clear"]}}'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(candidate, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"m4b_merge_binary":"`+candidate+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	buildSettingsRouter(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("swap PUT = %d — body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"invalid_output_profiles":[{`) {
		t.Fatalf("swap PUT response = %q; want the re-derived non-empty invalid list", rec.Body.String())
	}
	for _, want := range []string{"\"name\":\"Wide\"", `"format":"/books/{title}"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("swap PUT response = %q; want it to name the rejected profile (%q)", rec.Body.String(), want)
		}
	}
}
