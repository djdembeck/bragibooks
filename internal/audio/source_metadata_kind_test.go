package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractSourceMetadataFileKindDoesNotEnterFolderDiscovery pins the
// row-kind contract: with srcKind "file" the extractor must treat the
// source as a single file and probe probePath — it must NOT re-stat
// srcName and enter folder discovery (which is how a source swapped to a
// directory after the walk would reach ffprobe with unpinned child
// paths). The source name is a directory on disk; the kind says file.
func TestExtractSourceMetadataFileKindDoesNotEnterFolderDiscovery(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "part.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// probePath is what the stub ffprobe reads; name it so the stub's
	// <stem>.json sidecar resolves.
	probeFile := filepath.Join(t.TempDir(), "pinned.bin")
	writeProbeResponse(t, respDir, "pinned", `{"format":{"tags":{},"duration":"1.0"},"streams":[]}`)

	snap, err := ExtractSourceMetadata(t.Context(), ffprobe, srcDir, probeFile, "file", nil, nil)
	if err != nil {
		t.Fatalf("ExtractSourceMetadata: %v", err)
	}
	if snap.SourceKind != "file" {
		t.Fatalf("SourceKind = %q, want file (the row kind, not a re-stat of the name)", snap.SourceKind)
	}
	if snap.FilesTotal != 1 {
		t.Fatalf("FilesTotal = %d, want 1 (a file row probes ONE path, not folder discovery)", snap.FilesTotal)
	}
	if len(snap.Files) != 1 || snap.Files[0].File != filepath.Base(srcDir) {
		t.Fatalf("Files = %+v, want the source name as the single identity", snap.Files)
	}
	if snap.FilesProbed != 1 {
		t.Fatalf("FilesProbed = %d, want 1 (the pinned probePath is what ffprobe reads)", snap.FilesProbed)
	}
}

// TestExtractSourceMetadataFolderDiscoveryUsesPinnedPath pins the
// folder-discovery binding: for an indexed folder, srcName is the row's
// MUTABLE pathname and probePath is the pinned descriptor's path. A
// swap of srcName to a (different) directory after the open-time check
// must not be enumerated — discovery runs through the pinned path, and
// srcName survives only as the source's identity.
func TestExtractSourceMetadataFolderDiscoveryUsesPinnedPath(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	// The PINNED directory holds the part the stub can probe.
	pinnedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pinnedDir, "real.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProbeResponse(t, respDir, "real", `{"format":{"tags":{},"duration":"1.0"},"streams":[]}`)

	// srcName is a DIFFERENT directory holding a part the pinned path
	// cannot have. If discovery ran through srcName, "fromname.mp3"
	// would be recorded (and would fail its sidecar lookup).
	srcName := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcName, "fromname.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := ExtractSourceMetadata(t.Context(), ffprobe, srcName, pinnedDir, "folder", nil, nil)
	if err != nil {
		t.Fatalf("ExtractSourceMetadata: %v", err)
	}
	if snap.SourceKind != "folder" {
		t.Fatalf("SourceKind = %q, want folder", snap.SourceKind)
	}
	if snap.FilesTotal != 1 || snap.FilesProbed != 1 {
		t.Fatalf("files_total/probed = %d/%d, want 1/1 (the pinned directory's part)", snap.FilesTotal, snap.FilesProbed)
	}
	if len(snap.Files) != 1 || snap.Files[0].File != "real.mp3" {
		t.Fatalf("Files = %+v, want the pinned directory's part, not the mutable name's", snap.Files)
	}
}

// TestExtractSourceMetadataRefusedPartDoesNotCountProbed pins the
// probed-count contract: a part whose pin is refused (vanished, or
// swapped to an escaping/special file) is recorded as one errored file
// and NOT probed — it must not increment FilesProbed, or a folder with
// one probed + one refused part would report zero unprobed files,
// contradicting the per-file error.
func TestExtractSourceMetadataRefusedPartDoesNotCountProbed(t *testing.T) {
	respDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	ffprobe := stubFFprobe(t, respDir, marker)

	srcDir := t.TempDir()
	for _, name := range []string{"a.mp3", "b.mp3"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeProbeResponse(t, respDir, "a", `{"format":{"tags":{},"duration":"1.0"},"streams":[]}`)

	pinPart := func(part string) (string, *os.File, func(), bool) {
		if filepath.Base(part) == "a.mp3" {
			return part, nil, func() {}, true
		}
		return "", nil, nil, false // b.mp3: pin refused
	}

	snap, err := ExtractSourceMetadata(t.Context(), ffprobe, srcDir, srcDir, "folder", pinPart, nil)
	if err != nil {
		t.Fatalf("ExtractSourceMetadata: %v", err)
	}
	if snap.FilesTotal != 2 {
		t.Fatalf("FilesTotal = %d, want 2 (both parts are recorded)", snap.FilesTotal)
	}
	if snap.FilesProbed != 1 {
		t.Fatalf("FilesProbed = %d, want 1 (the refused part is not probed)", snap.FilesProbed)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("Files = %+v, want 2 rows", snap.Files)
	}
	refused := snap.Files[0]
	if refused.File == "a.mp3" {
		refused = snap.Files[1]
	}
	if refused.File != "b.mp3" || !strings.Contains(refused.Error, "not probed") {
		t.Fatalf("refused row = %+v, want b.mp3 marked not probed", refused)
	}
}
