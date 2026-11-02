package audio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubFFprobe writes an executable shell script standing in for ffprobe.
// The input file is the last argv entry; its basename with the extension
// stripped selects <respDir>/<stem>.json, which is printed. A missing
// sidecar makes the script fail, so per-file failures are expressible.
// Every invocation touches <marker>.
func stubFFprobe(t *testing.T, respDir, marker string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
for last; do :; done
touch ` + marker + `
base=$(basename "$last")
stem="${base%.*}"
if [ -f "` + respDir + `/${stem}.json" ]; then
  cat "` + respDir + `/${stem}.json"
else
  echo "probe: no such file" >&2
  exit 1
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}
	return path
}

func writeProbeResponse(t *testing.T, dir, name, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write probe response %s: %v", name, err)
	}
}

func runExtraction(t *testing.T, ffprobe, srcPath string) *SourceMetadata {
	t.Helper()
	snap, err := ExtractSourceMetadata(context.Background(), ffprobe, srcPath)
	if err != nil {
		t.Fatalf("ExtractSourceMetadata: %v", err)
	}
	if snap == nil {
		t.Fatal("ExtractSourceMetadata returned nil snapshot")
	}
	return snap
}

func wantField(t *testing.T, snap *SourceMetadata, key, wantStatus string, wantValues ...string) {
	t.Helper()
	f, ok := snap.Fields[key]
	if !ok {
		t.Fatalf("field %q missing from snapshot", key)
	}
	if f.Status != wantStatus {
		t.Errorf("field %s status = %q, want %q", key, f.Status, wantStatus)
	}
	if len(wantValues) > 0 {
		if len(f.Values) != len(wantValues) {
			t.Errorf("field %s values = %v, want %v", key, f.Values, wantValues)
			return
		}
		for i, v := range wantValues {
			if f.Values[i] != v {
				t.Errorf("field %s values[%d] = %q, want %q", key, i, f.Values[i], v)
			}
		}
	}
}

func wantAllFields(t *testing.T, snap *SourceMetadata) {
	t.Helper()
	if len(snap.Fields) != len(fieldKeys) {
		t.Errorf("snapshot carries %d fields, want all %d", len(snap.Fields), len(fieldKeys))
	}
}

func wantAllFieldsStatus(t *testing.T, snap *SourceMetadata, status, wantSource string) {
	t.Helper()
	wantAllFields(t, snap)
	for key, f := range snap.Fields {
		if f.Status != status {
			t.Errorf("field %s status = %q, want %q", key, f.Status, status)
		}
		if f.Source != wantSource {
			t.Errorf("field %s source = %q, want %q", key, f.Source, wantSource)
		}
	}
}

func TestExtractSourceMetadataMapsTags(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// Stream-level title overrides the format-level one (narrower scope
	// wins); album_artist beats artist for author, composer beats artist
	// for narrator.
	writeProbeResponse(t, respDir, "1", `{
  "format": {
    "duration": "120.5",
    "tags": {
      "title": "Format Title",
      "album_artist": "Jane Author",
      "artist": "Sam Artist",
      "composer": "Rita Narrator",
      "series": "The Series",
      "track": "2/5",
      "date": "2021",
      "publisher": "Big Pub",
      "language": "en",
      "audible_asin": "B0MAPPING1"
    }
  },
  "streams": [
    {"codec_type": "audio", "tags": {"title": "Stream Title"}}
  ]
}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)

	if snap.SourceKind != "folder" {
		t.Errorf("source_kind = %q, want folder", snap.SourceKind)
	}
	if snap.FilesTotal != 1 || snap.FilesProbed != 1 {
		t.Errorf("files_total/probed = %d/%d, want 1/1", snap.FilesTotal, snap.FilesProbed)
	}
	if snap.DurationSeconds != 120.5 {
		t.Errorf("duration_seconds = %v, want 120.5", snap.DurationSeconds)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Stream Title")
	wantField(t, snap, FieldAuthor, StatusPresent, "Jane Author")
	wantField(t, snap, FieldNarrator, StatusPresent, "Rita Narrator")
	wantField(t, snap, FieldSeries, StatusPresent, "The Series")
	wantField(t, snap, FieldSeriesPart, StatusPresent, "2")
	wantField(t, snap, FieldDate, StatusPresent, "2021")
	wantField(t, snap, FieldPublisher, StatusPresent, "Big Pub")
	wantField(t, snap, FieldLanguage, StatusPresent, "en")
	wantField(t, snap, FieldASIN, StatusPresent, "B0MAPPING1")
	wantAllFields(t, snap)
	if len(snap.RawTags) != 1 || snap.RawTags[0].File != "1.mp3" {
		t.Fatalf("raw_tags = %+v, want one entry for 1.mp3", snap.RawTags)
	}
	if got := snap.RawTags[0].Tags["audible_asin"]; got != "B0MAPPING1" {
		t.Errorf("raw_tags[0].tags[audible_asin] = %q, want B0MAPPING1", got)
	}
}

func TestExtractSourceMetadataPreservesContradiction(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	writeProbeResponse(t, respDir, "a", `{"format": {"duration": "10", "tags": {"title": "Alpha"}}, "streams": []}`)
	writeProbeResponse(t, respDir, "b", `{"format": {"duration": "20", "tags": {"title": "Beta"}}, "streams": []}`)

	srcDir := t.TempDir()
	for _, name := range []string{"a.mp3", "b.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantField(t, snap, FieldTitle, StatusPresent, "Alpha", "Beta")
	if snap.DurationSeconds != 30 {
		t.Errorf("duration_seconds = %v, want 30", snap.DurationSeconds)
	}
}

func TestExtractSourceMetadataFallsBackToFilenameASIN(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	name := "b012345678 - book.m4b"
	writeProbeResponse(t, respDir, "b012345678 - book", `{"format": {"duration": "10", "tags": {"title": "Named"}}, "streams": []}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantField(t, snap, FieldASIN, StatusPresent, "B012345678")
	if got := snap.Fields[FieldASIN].Source; got != SourceFilename {
		t.Errorf("asin source = %q, want %q", got, SourceFilename)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Named")
}

func TestExtractSourceMetadataSurvivesPerFileFailure(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// 1.mp3 has no sidecar → the stub fails on it.
	writeProbeResponse(t, respDir, "2", `{"format": {"duration": "400", "tags": {"title": "Stub Title"}}, "streams": []}`)

	srcDir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if len(snap.Files) != 2 {
		t.Fatalf("files = %d entries, want 2", len(snap.Files))
	}
	if snap.Files[0].Error == "" {
		t.Error("files[0].error empty, want the stub's failure")
	}
	if snap.Files[1].DurationSeconds != 400 {
		t.Errorf("files[1].duration_seconds = %v, want 400", snap.Files[1].DurationSeconds)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Stub Title")
	// A probe error makes the pass incomplete: a field that no
	// successful probe supplied must be errored, not claimed missing.
	wantField(t, snap, FieldAuthor, StatusError)
	if snap.DurationSeconds != 400 {
		t.Errorf("duration_seconds = %v, want 400 (errored file contributes nothing)", snap.DurationSeconds)
	}
}

func TestExtractSourceMetadataAllFilesFailIsDegraded(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantAllFieldsStatus(t, snap, StatusError, "")
	if snap.DurationSeconds != 0 {
		t.Errorf("duration_seconds = %v, want 0", snap.DurationSeconds)
	}
	// The degraded override keeps per-file data: each file's probe error
	// survives, and the first one is quoted in every field's message.
	if snap.Files[0].Error == "" || snap.Files[1].Error == "" {
		t.Errorf("files = %+v, want per-file probe errors retained", snap.Files)
	}
	for _, f := range snap.Fields {
		if !strings.Contains(f.Error, "all 2 probed files failed") {
			t.Errorf("field error = %q, want the all-failed message", f.Error)
		}
	}
}

func TestExtractSourceMetadataEmptyFolderIsAllMissing(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantAllFieldsStatus(t, snap, StatusMissing, SourceEmbedded)
	if snap.FilesTotal != 0 || snap.FilesProbed != 0 {
		t.Errorf("files_total/probed = %d/%d, want 0/0", snap.FilesTotal, snap.FilesProbed)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("probe ran on an empty folder (marker present): %v", err)
	}
}

func TestExtractSourceMetadataMissingSourceIsDegraded(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	snap := runExtraction(t, ffprobe, filepath.Join(t.TempDir(), "absent.m4b"))
	wantAllFieldsStatus(t, snap, StatusError, "")
	for key, f := range snap.Fields {
		if f.Error == "" {
			t.Errorf("field %s error empty, want the stat failure", key)
		}
	}
}

// TestExtractSourceMetadataRealMedia runs the real ffprobe end to end: it
// builds two tagged MP3s and checks the mapped fields, the summed
// duration, and the retained raw tags.
func TestExtractSourceMetadataRealMedia(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	dir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		args := []string{
			"-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-metadata", "title=Real Media Book",
			"-metadata", "album_artist=Real Author",
			"-metadata", "composer=Real Narrator",
			"-c:a", "libmp3lame",
			filepath.Join(dir, name),
		}
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", name, err, out)
		}
	}

	snap := runExtraction(t, ffprobe, dir)
	if snap.SourceKind != "folder" || snap.FilesTotal != 2 {
		t.Fatalf("source_kind/files_total = %q/%d, want folder/2", snap.SourceKind, snap.FilesTotal)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Real Media Book")
	wantField(t, snap, FieldAuthor, StatusPresent, "Real Author")
	wantField(t, snap, FieldNarrator, StatusPresent, "Real Narrator")
	if snap.DurationSeconds < 3.5 {
		t.Errorf("duration_seconds = %v, want >= 3.5 for two 2s files", snap.DurationSeconds)
	}
	if len(snap.RawTags) == 0 {
		t.Error("raw_tags empty, want one entry per probed file")
	}
}
