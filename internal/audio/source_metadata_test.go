package audio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// TestExtractSourceMetadataAllFilesFailKeepsFilenameASIN checks that the
// all-probes-failed degradation keeps the filename ASIN: the degraded
// override must not write over a value no successful probe could supply.
func TestExtractSourceMetadataAllFilesFailKeepsFilenameASIN(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// No sidecars: both probes fail.
	srcDir := t.TempDir()
	for _, name := range []string{"b012345678 - book.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantField(t, snap, FieldASIN, StatusPresent, "B012345678")
	if got := snap.Fields[FieldASIN].Source; got != SourceFilename {
		t.Errorf("asin source = %q, want %q", got, SourceFilename)
	}
	for _, f := range fieldKeys {
		if f == FieldASIN {
			continue
		}
		if sf := snap.Fields[f]; sf.Status != StatusError || !strings.Contains(sf.Error, "all 2 probed files failed") {
			t.Errorf("field %s = %+v, want the all-failed error", f, sf)
		}
	}
	if snap.Files[0].Error == "" || snap.Files[1].Error == "" {
		t.Errorf("files = %+v, want per-file probe errors retained", snap.Files)
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

// TestExtractSourceMetadataMissingSourceFilenameASINIsDegraded checks that
// a stat failure with a canonical ASIN in the filename keeps the value for
// display but marks the pass degraded: the surviving ASIN is not complete
// evidence, so Complete() must not report a clean extraction.
func TestExtractSourceMetadataMissingSourceFilenameASINIsDegraded(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcPath := filepath.Join(t.TempDir(), "b012345678 - gone.m4b")
	snap := runExtraction(t, ffprobe, srcPath)

	// The filename ASIN survives for display.
	wantField(t, snap, FieldASIN, StatusPresent, "B012345678")
	if got := snap.Fields[FieldASIN].Source; got != SourceFilename {
		t.Errorf("asin source = %q, want %q", got, SourceFilename)
	}
	// ...but the unreadable source degrades the whole pass.
	if !snap.Degraded {
		t.Error("degraded = false, want true for an unreadable source")
	}
	if snap.Complete() {
		t.Error("Complete() = true; a stat-failed source is incomplete evidence")
	}
	for key, f := range snap.Fields {
		if key == FieldASIN {
			continue // the surviving filename value carries no probe error
		}
		if f.Status != StatusError || f.Error == "" {
			t.Errorf("field %s = %+v, want the stat-failure error", key, f)
		}
	}
	// The stat failure short-circuits before any probe: the stub marker
	// must be absent, proving the probe pass was skipped rather than merely
	// unsuccessful.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("probe ran on an unreadable source (marker present): %v", err)
	}
}

// TestExtractSourceMetadataProbeOutputCapExercises exercises the 1 MiB
// probe-output cap: the probe is errored, which makes the pass incomplete,
// so every empty field is errored (extraction incomplete), never claimed
// missing.
func TestExtractSourceMetadataProbeOutputCapExercises(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// A 1.05 MiB tag value pushes the probe output over the 1 MiB cap
	// (the value itself is under the 2 KiB per-value cap).
	longValue := strings.Repeat("x", 1050*1024)
	writeProbeResponse(t, respDir, "1", `{"format": {"duration": "10", "tags": {"title": "`+longValue+`"}}, "streams": []}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if len(snap.Files) != 1 || !strings.Contains(snap.Files[0].Error, "probe output exceeds 1 MiB") {
		t.Fatalf("files = %+v, want the capped probe errored", snap.Files)
	}
	// The probe error makes the pass incomplete: no field may claim the
	// source lacks a tag.
	wantAllFieldsStatus(t, snap, StatusError, SourceDegraded)
}

// TestExtractSourceMetadataFileCapIsTruncated exercises the 100-file probe
// cap: file 101 is never probed, so the pass is incomplete and an empty
// field is errored, not missing.
func TestExtractSourceMetadataFileCapIsTruncated(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	for i := 1; i <= 101; i++ {
		name := fmt.Sprintf("%03d.mp3", i)
		if i == 1 {
			// Only file 1 supplies a title: the file that never gets
			// probed would have supplied the author.
			writeProbeResponse(t, respDir, name[:len(name)-4],
				`{"format": {"duration": "1", "tags": {"title": "Capped"}}, "streams": []}`)
		} else {
			writeProbeResponse(t, respDir, name[:len(name)-4],
				`{"format": {"duration": "1", "tags": {}}, "streams": []}`)
		}
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	// The cap is enforced at discovery: only 100 of the 101 matched
	// files are kept, truncated is flagged, and all kept files are
	// probed.
	if !snap.Truncated {
		t.Fatal("truncated = false, want true (file cap hit)")
	}
	if snap.FilesTotal != 100 || snap.FilesProbed != 100 {
		t.Fatalf("files_total/probed = %d/%d, want 100/100", snap.FilesTotal, snap.FilesProbed)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Capped")
	// Truncation alone makes the pass incomplete: the unscanned file
	// could have supplied the author.
	wantField(t, snap, FieldAuthor, StatusError)
}

// TestExtractSourceMetadataRawTagCapIsTruncated exercises the 64 KiB
// raw-tag retention cap: the first file's tags are kept, the second file's
// are dropped, and the pass is marked truncated.
func TestExtractSourceMetadataRawTagCapIsTruncated(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// Each tag value is capped at 2 KiB, so the 64 KiB raw cap is only
	// reachable across many wide tags: file 1's 32 (~64 KiB) just fit,
	// file 2's push it over.
	wideDoc := func(ch byte, n int) string {
		tags := make([]string, n)
		for i := range n {
			tags[i] = fmt.Sprintf(`"t%02d": "%s"`, i, strings.Repeat(string(ch), 2000))
		}
		return `{"format": {"duration": "10", "tags": {` + strings.Join(tags, ", ") + `}}, "streams": []}`
	}
	writeProbeResponse(t, respDir, "1", wideDoc('a', 32))
	writeProbeResponse(t, respDir, "2", `{"format": {"duration": "10", "tags": {"publisher": "`+strings.Repeat("b", 2044)+`"}}, "streams": []}`)

	srcDir := t.TempDir()
	for _, name := range []string{"1.mp3", "2.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if !snap.Truncated {
		t.Fatal("truncated = false, want true (raw-tag cap hit)")
	}
	if len(snap.RawTags) != 1 || snap.RawTags[0].File != "1.mp3" {
		t.Fatalf("raw_tags = %d entries, want one entry for 1.mp3", len(snap.RawTags))
	}
	// File 2 was fully probed (no per-file error), so its publisher value
	// survives even though its raw tags were dropped.
	wantField(t, snap, FieldPublisher, StatusPresent)
	// Truncation makes the pass incomplete: a field no probe supplied
	// must be errored, never claimed missing.
	wantField(t, snap, FieldTitle, StatusError)
	wantField(t, snap, FieldAuthor, StatusError)
}

// TestExtractSourceMetadataMissingSourceKeepsFilenameASIN checks that a
// vanished source degrades to all-error rows, except the ASIN field, which
// the filename still supplies.
func TestExtractSourceMetadataMissingSourceKeepsFilenameASIN(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	snap := runExtraction(t, ffprobe, filepath.Join(t.TempDir(), "b012345678 - book.m4b"))
	wantField(t, snap, FieldASIN, StatusPresent, "B012345678")
	if got := snap.Fields[FieldASIN].Source; got != SourceFilename {
		t.Errorf("asin source = %q, want %q", got, SourceFilename)
	}
	wantField(t, snap, FieldTitle, StatusError)
	wantField(t, snap, FieldAuthor, StatusError)
	if snap.SourceKind != "unknown" {
		t.Errorf("source_kind = %q, want unknown", snap.SourceKind)
	}
}

// TestExtractSourceMetadataStreamWinsOverDifferentlyCasedFormatTag pins
// the case-insensitive format/stream collision: the format tag is spelled
// TITLE and the first audio stream's title is spelled title. The narrower
// (stream) scope must win deterministically, not by map iteration order.
func TestExtractSourceMetadataStreamWinsOverDifferentlyCasedFormatTag(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	writeProbeResponse(t, respDir, "1", `{
  "format": {"duration": "10", "tags": {"TITLE": "Format Value"}},
  "streams": [{"codec_type": "audio", "tags": {"title": "Stream Value"}}]
}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for range 10 {
		snap := runExtraction(t, ffprobe, srcDir)
		wantField(t, snap, FieldTitle, StatusPresent, "Stream Value")
	}
}

// TestExtractSourceMetadataProbeRestrictsProtocols asserts the probe is
// launched with the local-only protocol whitelist: a crafted container
// must not be able to make ffprobe reach the network.
func TestExtractSourceMetadataProbeRestrictsProtocols(t *testing.T) {
	respDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv")
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
printf '%s
' "$@" > ` + argsFile + `
for last; do :; done
base=$(basename "$last")
cat "` + respDir + `/${base}.json"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}
	writeProbeResponse(t, respDir, "1.mp3", `{"format": {"duration": "10", "tags": {"title": "Named"}}, "streams": []}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, bin, srcDir)
	wantField(t, snap, FieldTitle, StatusPresent, "Named")

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read argv sidecar: %v", err)
	}
	args := strings.Fields(string(raw))
	for i, a := range args {
		if a == "-protocol_whitelist" {
			if i+1 >= len(args) || args[i+1] != "file,pipe,data" {
				t.Fatalf("probe argv = %q, want -protocol_whitelist file,pipe,data", args)
			}
			return
		}
	}
	t.Fatalf("probe argv = %q, want the -protocol_whitelist flag", args)
}

// TestExtractSourceMetadataOverLimitProbeIsKilledAndReaped pins the reap
// fix: when the probe output exceeds the 1 MiB cap the child is killed
// AND reaped, so after the extraction returns nothing is left alive (or
// as a zombie) from the probe.
func TestExtractSourceMetadataOverLimitProbeIsKilledAndReaped(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "probe.pid")
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
echo $$ > ` + pidFile + `
head -c 1100000 /dev/zero | tr '\0' 'x'
sleep 300
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, bin, srcDir)
	if len(snap.Files) != 1 || !strings.Contains(snap.Files[0].Error, "probe output exceeds 1 MiB") {
		t.Fatalf("files = %+v, want the capped probe errored", snap.Files)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read probe pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse probe pid: %v", err)
	}
	// Bounded settle loop, not a sleep: kill -0 also reports zombies, so
	// a live process here means the reap (kill + wait) did not happen.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe pid %d still alive after extraction returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestExtractSourceMetadataTagValueCapIsIndependent pins the per-tag 2 KiB
// value cap: a probe response comfortably under the 1 MiB output cap still
// has its oversized value truncated in both the merged field and the
// retained raw tags, and the probe itself succeeds.
func TestExtractSourceMetadataTagValueCapIsIndependent(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	long := strings.Repeat("v", 3000)
	writeProbeResponse(t, respDir, "1", `{"format": {"duration": "10", "tags": {"title": "`+long+`"}}, "streams": []}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if len(snap.Files) != 1 || snap.Files[0].Error != "" {
		t.Fatalf("files = %+v, want one clean probe", snap.Files)
	}
	wantValue := strings.Repeat("v", 2048) + "…"
	wantField(t, snap, FieldTitle, StatusPresent, wantValue)
	if len(snap.RawTags) != 1 || snap.RawTags[0].Tags["title"] != wantValue {
		t.Errorf("raw title = %q, want the same 2 KiB-capped value", snap.RawTags[0].Tags["title"])
	}
}

// TestExtractSourceMetadataProbeStderrIsBounded pins the 512-byte stderr
// cap: a probe failing with a large stderr has only its first 512 bytes
// retained in the per-file error, and the extraction returns promptly
// (the bounded writer never makes the child block).
func TestExtractSourceMetadataProbeStderrIsBounded(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
head -c 2000 /dev/zero | tr '\0' 'e' >&2
exit 1
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, bin, srcDir)
	if len(snap.Files) != 1 {
		t.Fatalf("files = %+v, want one per-file entry", snap.Files)
	}
	const marker = "; stderr: "
	idx := strings.Index(snap.Files[0].Error, marker)
	if idx < 0 {
		t.Fatalf("file error = %q, want the stderr quote", snap.Files[0].Error)
	}
	if retained := snap.Files[0].Error[idx+len(marker):]; len(retained) != 512 {
		t.Fatalf("retained stderr = %d bytes, want the 512-byte cap", len(retained))
	}
}

// TestExtractSourceMetadataDistinctValueCapPinsTheFirstTwenty pins the
// per-field 20-distinct-value bound: 21 files with distinct titles keep
// exactly the first 20 in first-seen (sorted path) order, and the field
// carries the cap hit so a reader never has to infer it from the length.
func TestExtractSourceMetadataDistinctValueCapPinsTheFirstTwenty(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	for i := range 21 {
		stem := fmt.Sprintf("%03d", i)
		writeProbeResponse(t, respDir, stem,
			fmt.Sprintf(`{"format": {"duration": "1", "tags": {"title": "T%02d"}}, "streams": []}`, i))
		if err := os.WriteFile(filepath.Join(srcDir, stem+".mp3"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	want := make([]string, 20)
	for i := range want {
		want[i] = fmt.Sprintf("T%02d", i)
	}
	wantField(t, snap, FieldTitle, StatusPresent, want...)
	if !snap.Fields[FieldTitle].Truncated {
		t.Error("title field truncated = false, want true (the 21st distinct value was dropped)")
	}
	// The snapshot-level flag covers discovery/probe/raw-tag bounds only:
	// a per-field cap hit must not masquerade as snapshot truncation.
	if snap.Truncated {
		t.Error("snapshot truncated = true, want false (only the per-field cap was hit)")
	}
}

// TestExtractSourceMetadataExactlyCappedFieldIsNotTruncated is the other
// half of the cap contract: a field that retains exactly the cap's worth
// of distinct values, with nothing dropped, must stay untruncated. The
// old reader inferred "capped" from the retained length and degraded this
// snapshot to unknown.
func TestExtractSourceMetadataExactlyCappedFieldIsNotTruncated(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	// Exactly maxDistinctValuesPerField distinct titles: the last addValue
	// fills the list to the cap without dropping anything.
	for i := range maxDistinctValuesPerField {
		stem := fmt.Sprintf("%03d", i)
		writeProbeResponse(t, respDir, stem,
			fmt.Sprintf(`{"format": {"duration": "1", "tags": {"title": "T%02d"}}, "streams": []}`, i))
		if err := os.WriteFile(filepath.Join(srcDir, stem+".mp3"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if got := len(snap.Fields[FieldTitle].Values); got != maxDistinctValuesPerField {
		t.Fatalf("title values = %d, want the cap %d", got, maxDistinctValuesPerField)
	}
	if snap.Fields[FieldTitle].Truncated {
		t.Error("title field truncated = true, want false (nothing was dropped)")
	}
	if !snap.Complete() {
		t.Error("Complete() = false; an exactly-capped field is still a complete pass")
	}
}

// TestExtractSourceMetadataDiscoveryBoundTruncatesBeforeAudio pins the
// 1000-entry discovery bound: the scan walks in 64-entry batches and
// truncates once a batch pushes the scanned count past 1000, so a folder
// with more entries than the bound (but no audio in the scanned window)
// is marked truncated. A truncated scan cannot claim the unscanned entries
// held nothing, so empty fields report error, never missing.
//
// The folder carries 1029 non-audio files — just past 16 full batches —
// so the 17th read never happens: the bound trips on batch 16 (scanned
// 1028 >= 1000), independent of the filesystem's read order.
func TestExtractSourceMetadataDiscoveryBoundTruncatesBeforeAudio(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	for i := range 1029 {
		name := fmt.Sprintf("%04d.txt", i)
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	snap := runExtraction(t, ffprobe, srcDir)
	if !snap.Truncated {
		t.Fatal("truncated = false, want true (discovery bound hit)")
	}
	if snap.FilesTotal != 0 || snap.FilesProbed != 0 {
		t.Errorf("files_total/probed = %d/%d, want 0/0 (no audio in the scanned window)",
			snap.FilesTotal, snap.FilesProbed)
	}
	wantAllFieldsStatus(t, snap, StatusError, SourceEmbedded)
}

// TestExtractSourceMetadataSingleFileSource pins the direct file branch:
// a non-directory source probes the one file and reports source_kind
// "file", one probed file, its mapped fields, and its duration.
func TestExtractSourceMetadataSingleFileSource(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	writeProbeResponse(t, respDir, "book",
		`{"format": {"duration": "90.5", "tags": {"title": "Solo", "album_artist": "Sole Author"}}, "streams": []}`)

	src := filepath.Join(t.TempDir(), "book.m4b")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, src)
	if snap.SourceKind != "file" {
		t.Errorf("source_kind = %q, want file", snap.SourceKind)
	}
	if snap.FilesTotal != 1 || snap.FilesProbed != 1 {
		t.Errorf("files_total/probed = %d/%d, want 1/1", snap.FilesTotal, snap.FilesProbed)
	}
	if snap.DurationSeconds != 90.5 {
		t.Errorf("duration_seconds = %v, want 90.5", snap.DurationSeconds)
	}
	wantField(t, snap, FieldTitle, StatusPresent, "Solo")
	wantField(t, snap, FieldAuthor, StatusPresent, "Sole Author")
}

// TestExtractSourceMetadataEmbeddedASINOutranksFilename pins the ASIN
// precedence: a source whose filename carries an ASIN keeps the embedded
// tag's value (and its embedded provenance) when a probe supplies one;
// the filename ASIN is never recorded as the field value.
func TestExtractSourceMetadataEmbeddedASINOutranksFilename(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	name := "B0ZZZZZZZZ - book.m4b"
	writeProbeResponse(t, respDir, "B0ZZZZZZZZ - book",
		`{"format": {"duration": "10", "tags": {"audible_asin": "B0EMBEDDED"}}, "streams": []}`)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, ffprobe, srcDir)
	wantField(t, snap, FieldASIN, StatusPresent, "B0EMBEDDED")
	if got := snap.Fields[FieldASIN].Source; got != SourceEmbedded {
		t.Errorf("asin source = %q, want %q", got, SourceEmbedded)
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

// TestFoldProbeTagsResolvesCollisionsDeterministically pins the case-fold
// collision rules: a within-scope case collision (TITLE vs title) is decided
// by lexicographic key order, and a cross-scope collision (same folded key in
// format AND stream) is decided by scope (stream wins). Neither may depend on
// map iteration order, which is random per process.
func TestFoldProbeTagsResolvesCollisionsDeterministically(t *testing.T) {
	tests := []struct {
		name       string
		formatTags map[string]string
		streamTags map[string]string
		want       map[string]string
	}{
		{
			// Within-scope: pairs sort ascending by raw key ("TITLE" before
			// "title") and later insertions win, so the lexicographically
			// larger key ("title") decides the fold — a fixed rule, never
			// map iteration order.
			name:       "within-scope case collision is lexicographic",
			formatTags: map[string]string{"TITLE": "upper loses", "title": "Lower Wins", "artist": "A"},
			streamTags: nil,
			want:       map[string]string{"title": "Lower Wins", "artist": "A"},
		},
		{
			// Cross-scope: the stream (narrower) scope wins for "title".
			name:       "cross-scope collision stream wins",
			formatTags: map[string]string{"TITLE": "Format Title"},
			streamTags: map[string]string{"title": "Stream Title"},
			want:       map[string]string{"title": "Stream Title"},
		},
		{
			// Both scope AND within-scope ordering at once: format has TITLE
			// and title; stream has Title. Result must be the stream's.
			name:       "scope wins over within-scope ordering",
			formatTags: map[string]string{"TITLE": "fmt upper", "title": "fmt lower"},
			streamTags: map[string]string{"Title": "stream"},
			want:       map[string]string{"title": "stream"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Run the fold repeatedly: map iteration order is randomized, so
			// any non-deterministic winner would surface within a few passes.
			for range 50 {
				got := foldProbeTags(tc.formatTags, tc.streamTags)
				if len(got) != len(tc.want) {
					t.Fatalf("fold = %d keys, want %d (got %v)", len(got), len(tc.want), got)
				}
				for k, v := range tc.want {
					if got[k] != v {
						t.Fatalf("fold[%q] = %q, want %q (got %v)", k, got[k], v, got)
					}
				}
			}
		})
	}
}

// TestFileSnapshotFromFolderMatchesStandaloneProbe pins the de-dupe
// contract: a file snapshot derived from a complete folder pass must
// agree with a standalone probe of that same file on every field, the
// ASIN, and the duration — so the index never re-probes a child whose
// parent was probed in the same pass.
func TestFileSnapshotFromFolderMatchesStandaloneProbe(t *testing.T) {
	respDir := t.TempDir()
	marker := t.TempDir()
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	// Three files, each with a distinct embedded title/author and a
	// duration, plus one filename-ASIN file to exercise the fallback.
	// d.m4b carries the precedence keys in ORIGINAL case (TITLE,
	// AUDIBLE_ASIN) — a naive lowercase lookup in the derived snapshot
	// would lose that evidence. e.m4b has a same-lowercase-key
	// format-vs-stream collision: the stream scope must win, exactly as
	// the standalone fold decides it.
	files := map[string]string{
		"a.m4b": `{"format": {"duration": "10", "tags": {"title": "Alpha", "album_artist": "Author A"}}, "streams": []}`,
		"b.m4b": `{"format": {"duration": "20", "tags": {"title": "Beta", "album_artist": "Author B", "audible_asin": "B012345678"}}, "streams": []}`,
		"c.m4b": `{"format": {"duration": "30", "tags": {}}, "streams": []}`,
		"d.m4b": `{"format": {"duration": "40", "tags": {"TITLE": "Delta", "AUDIBLE_ASIN": "B0DELTA000"}}, "streams": []}`,
		"e.m4b": `{"format": {"duration": "50", "tags": {"title": "E-Fmt"}}, "streams": [{"codec_type": "audio", "tags": {"TITLE": "E-Stream"}}]}`,
	}
	for name, doc := range files {
		writeProbeResponse(t, respDir, strings.TrimSuffix(name, filepath.Ext(name)), doc)
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	folder := runExtraction(t, ffprobe, srcDir)
	if !folder.Complete() {
		t.Fatalf("folder snapshot not complete: %v", folder.Fields)
	}

	for name := range files {
		path := filepath.Join(srcDir, name)
		derived := FileSnapshotFromFolder(folder, path)
		standalone := runExtraction(t, ffprobe, path)
		if !derived.Complete() {
			t.Errorf("%s: derived snapshot not complete", name)
		}
		for _, key := range fieldKeys {
			d, s := derived.Fields[key], standalone.Fields[key]
			if d.Status != s.Status {
				t.Errorf("%s: field %s status = %q, standalone %q", name, key, d.Status, s.Status)
			}
			if len(d.Values) != len(s.Values) {
				t.Errorf("%s: field %s values = %v, standalone %v", name, key, d.Values, s.Values)
				continue
			}
			for i := range d.Values {
				if d.Values[i] != s.Values[i] {
					t.Errorf("%s: field %s values[%d] = %q, standalone %q", name, key, i, d.Values[i], s.Values[i])
				}
			}
		}
		if derived.DurationSeconds != standalone.DurationSeconds {
			t.Errorf("%s: duration = %v, standalone %v", name, derived.DurationSeconds, standalone.DurationSeconds)
		}
	}

	// The filename-ASIN file: the standalone pass finds the ASIN in the
	// embedded tag (audible_asin), so the derived one must match it.
	if got := FileSnapshotFromFolder(folder, filepath.Join(srcDir, "b.m4b")).Fields[FieldASIN]; len(got.Values) != 1 || got.Values[0] != "B012345678" {
		t.Errorf("b.m4b derived ASIN = %v, want [B012345678]", got.Values)
	}
	// Original-case keys: the derived snapshot must resolve TITLE /
	// AUDIBLE_ASIN exactly as the standalone fold does.
	if got := FileSnapshotFromFolder(folder, filepath.Join(srcDir, "d.m4b")).Fields[FieldTitle]; len(got.Values) != 1 || got.Values[0] != "Delta" {
		t.Errorf("d.m4b derived title = %v, want [Delta]", got.Values)
	}
	if got := FileSnapshotFromFolder(folder, filepath.Join(srcDir, "d.m4b")).Fields[FieldASIN]; len(got.Values) != 1 || got.Values[0] != "B0DELTA000" {
		t.Errorf("d.m4b derived ASIN = %v, want [B0DELTA000]", got.Values)
	}
	// Format-vs-stream same-lowercase-key collision: the stream scope wins.
	if got := FileSnapshotFromFolder(folder, filepath.Join(srcDir, "e.m4b")).Fields[FieldTitle]; len(got.Values) != 1 || got.Values[0] != "E-Stream" {
		t.Errorf("e.m4b derived title = %v, want [E-Stream]", got.Values)
	}
	// A file absent from the folder snapshot degrades (honest "not run").
	if got := FileSnapshotFromFolder(folder, filepath.Join(srcDir, "missing.m4b")); !got.Degraded {
		t.Errorf("missing.m4b derived snapshot = %v, want degraded", got)
	}
}
