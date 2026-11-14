package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// TestBuildEngineJobEmptyAuthorsSerializeAsArray guards the S4 fix: an
// authorless book must produce a non-null authors array in input_supplied,
// because the engine's serde accepts a missing field, not JSON null.
func TestBuildEngineJobEmptyAuthorsSerializeAsArray(t *testing.T) {
	snap := BookSnapshot{
		BookID:  7,
		Title:   "No Author Book",
		Authors: []string{},
	}
	jf, staged, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if staged != "" {
		t.Fatalf("staged = %q, want empty (no cover captured)", staged)
	}
	data, err := audio.JobFileJSON(jf)
	if err != nil {
		t.Fatalf("JobFileJSON: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var supplied struct {
		Authors []string `json:"authors"`
	}
	if err := json.Unmarshal(raw["input_supplied"], &supplied); err != nil {
		t.Fatalf("unmarshal input_supplied: %v", err)
	}
	if supplied.Authors == nil {
		t.Fatalf("authors serialized as null; the engine's Vec<String> serde rejects null")
	}
	if len(supplied.Authors) != 0 {
		t.Fatalf("authors = %v, want empty", supplied.Authors)
	}
}

// TestBuildEngineJobBlankTitlePreserves guards the S6 fix: a blank title must
// get a "preserve" write policy, never an unconditional "write", because a
// title write of blank text is a diagnosed engine failure.
func TestBuildEngineJobBlankTitlePreserves(t *testing.T) {
	for _, title := range []string{"", "   "} {
		snap := BookSnapshot{BookID: 1, Title: title}
		jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}}, "job-1", snap, t.TempDir(), "{title}")
		if err != nil {
			t.Fatalf("buildEngineJob(%q): %v", title, err)
		}
		if jf.Policies.Title != "preserve" {
			t.Fatalf("blank title %q: policy title = %q, want preserve", title, jf.Policies.Title)
		}
	}
	// A non-blank title keeps "write": the fix is only for the blank case.
	snap := BookSnapshot{BookID: 1, Title: "Named"}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}}, "job-1", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if jf.Policies.Title != "write" {
		t.Fatalf("non-blank title: policy title = %q, want write", jf.Policies.Title)
	}
}

// TestBuildEngineJobStagedCoverIsJobScoped guards the S7 fixes: the staged
// cover name carries the job ID, the output root is precreated, and the
// staged path is returned for post-run cleanup.
func TestBuildEngineJobStagedCoverIsJobScoped(t *testing.T) {
	covers := t.TempDir()
	oldCovers := coversDirPath
	coversDirPath = covers
	t.Cleanup(func() { coversDirPath = oldCovers })

	coverName := "7-snapshot.jpg"
	if err := os.WriteFile(filepath.Join(covers, coverName), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "not-yet-created")
	snap := BookSnapshot{BookID: 7, Title: "Covered", CoverFile: coverName}
	jf, staged, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}}, "job-7", snap, root, "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if staged == "" {
		t.Fatal("staged = empty, want the staged cover path")
	}
	if !strings.Contains(filepath.Base(staged), "job-7") {
		t.Fatalf("staged name %q does not carry the job ID", filepath.Base(staged))
	}
	if info, err := os.Stat(staged); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("staged file missing or not regular: %v", err)
	}
	// The output root must exist before staging (S7 root precreate).
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("output root %s not precreated: %v", root, err)
	}
	// The job references the staged file by absolute path.
	var coverObj map[string]string
	if err := json.Unmarshal(jf.Cover, &coverObj); err != nil {
		t.Fatalf("cover policy %s: %v", jf.Cover, err)
	}
	if coverObj["write"] != staged {
		t.Fatalf("cover write path = %q, want %q", coverObj["write"], staged)
	}
}
