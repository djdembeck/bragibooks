package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SourceMetadataSchema is the payload schema version carried in every
// SourceMetadata document.
const SourceMetadataSchema = 1

// Field keys used in SourceMetadata.Fields.
const (
	FieldTitle                = "title"
	FieldAuthor               = "author"
	FieldNarrator             = "narrator"
	FieldSeries               = "series"
	FieldSeriesPart           = "series_part"
	FieldDate                 = "date"
	FieldPublisher            = "publisher"
	FieldLanguage             = "language"
	FieldASIN                 = "asin"
	FieldAudiobookdbBookID    = "audiobookdb_book_id"
	FieldAudiobookdbReleaseID = "audiobookdb_release_id"
)

// Source value constants: where a field's value came from.
const (
	SourceEmbedded = "embedded"
	SourceFilename = "filename"
	// SourceDegraded is the empty source marker for degraded rows.
	SourceDegraded = ""
)

// Status value constants: the per-field extraction outcome.
const (
	StatusPresent = "present"
	StatusMissing = "missing"
	StatusError   = "error"
)

// SourceField is one normalized metadata field with its provenance and
// per-field outcome.
type SourceField struct {
	Values []string `json:"values"` // distinct, first-seen order; >1 = preserved contradiction
	Source string   `json:"source"` // "embedded" | "filename" | ""
	Status string   `json:"status"` // "present" | "missing" | "error"
	Error  string   `json:"error,omitempty"`
}

// SourceFile is one probed file's measured outcome.
type SourceFile struct {
	File            string  `json:"file"` // basename
	DurationSeconds float64 `json:"duration_seconds"`
	Error           string  `json:"error,omitempty"`
}

// SourceFileTags is one probed file's effective (format + first audio
// stream) tag map, kept verbatim for recoverability.
type SourceFileTags struct {
	File string            `json:"file"` // basename
	Tags map[string]string `json:"tags"`
}

// SourceMetadata is the per-book snapshot of what the source itself says:
// embedded tags, embedded/filename IDs, and measured durations. It is
// independent of catalog data: a Save-match never touches it.
type SourceMetadata struct {
	Schema          int                    `json:"schema"`
	SourceKind      string                 `json:"source_kind"` // "file" | "folder" | "unknown"
	FilesTotal      int                    `json:"files_total"`
	FilesProbed     int                    `json:"files_probed"`
	Truncated       bool                   `json:"truncated"`
	DurationSeconds float64                `json:"duration_seconds"` // sum of probed, non-errored files
	Files           []SourceFile           `json:"files"`
	Fields          map[string]SourceField `json:"fields"`
	RawTags         []SourceFileTags       `json:"raw_tags"`
}

// Boundedness of the extraction pass.
const (
	sourceMetadataTimeout     = 60 * time.Second
	maxDiscoveredEntries      = 1000
	maxProbedParts            = 100
	maxDistinctValuesPerField = 20
	maxProbeOutputBytes       = 1 << 20 // 1 MiB
	maxProbeStderrBytes       = 512
	maxTagValueBytes          = 2048
	maxRawTagBytes            = 64 << 10 // 64 KiB
)

// asinPattern matches Audible ASINs (case-insensitive; matches are
// normalized to uppercase).
var asinPattern = regexp.MustCompile(`(?i)\bB[A-Z0-9]{9}\b`)

// tagPrecedence maps each normalized field to its candidate tag keys in
// priority order (first present wins; keys compared case-insensitively).
var tagPrecedence = map[string][]string{
	FieldTitle:                {"title"},
	FieldAuthor:               {"album_artist", "author", "artist"},
	FieldNarrator:             {"narratedby", "composer", "artist"},
	FieldSeries:               {"series", "album", "tvshow"},
	FieldSeriesPart:           {"track", "part"},
	FieldDate:                 {"date", "year"},
	FieldPublisher:            {"publisher"},
	FieldLanguage:             {"language"},
	FieldASIN:                 {"audible_asin", "asin"},
	FieldAudiobookdbBookID:    {"audiobookdb_book_id", "adb_book_id"},
	FieldAudiobookdbReleaseID: {"audiobookdb_release_id", "adb_release_id"},
}

// fieldKeys is the fixed set of normalized fields, in stable order.
var fieldKeys = []string{
	FieldTitle, FieldAuthor, FieldNarrator, FieldSeries, FieldSeriesPart,
	FieldDate, FieldPublisher, FieldLanguage, FieldASIN,
	FieldAudiobookdbBookID, FieldAudiobookdbReleaseID,
}

// limitedWriter retains the first maxProbeStderrBytes of a probe's stderr
// and silently discards the rest. It always reports success so the child
// never sees a broken pipe.
type limitedWriter struct{ b []byte }

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := maxProbeStderrBytes - len(w.b); room > 0 {
		keep := min(len(p), room)
		w.b = append(w.b, p[:keep]...)
	}
	return len(p), nil
}

// ExtractSourceMetadata reads the source's embedded tags, IDs, and
// measured durations with ffprobe and returns a complete snapshot.
// Every boundedness outcome — unreadable source, probe failures,
// deadline expiry, empty source — is expressed inside the returned
// snapshot; the returned error is non-nil only for programming-level
// faults.
func ExtractSourceMetadata(ctx context.Context, ffprobe, srcPath string) (*SourceMetadata, error) {
	passCtx, cancel := context.WithTimeout(ctx, sourceMetadataTimeout)
	defer cancel()

	snap := &SourceMetadata{
		Schema: SourceMetadataSchema,
		Fields: make(map[string]SourceField, len(tagPrecedence)),
	}

	stat, err := os.Stat(srcPath)
	if err != nil {
		return degradedSnapshot(snap, err.Error()), nil
	}

	var paths []string
	if stat.IsDir() {
		snap.SourceKind = "folder"
		paths, err = discoverAudioFiles(passCtx, srcPath, snap)
		if err != nil {
			return degradedSnapshot(snap, err.Error()), nil
		}
	} else {
		snap.SourceKind = "file"
		paths = []string{srcPath}
	}
	snap.FilesTotal = len(paths)

	if len(paths) == 0 {
		return finalizeEmpty(snap), nil
	}

	merged, probedErrs := probeFiles(passCtx, ffprobe, paths, snap)
	if probedErrs == len(paths) {
		firstErr := ""
		if len(snap.Files) > 0 {
			firstErr = snap.Files[0].Error
		}
		return degradedSnapshot(snap, fmt.Sprintf("all %d probed files failed: %s", len(paths), firstErr)), nil
	}

	mergeFields(snap, merged)
	applyASINFallback(snap, srcPath)
	return finalize(snap), nil
}

// DegradedSourceMetadata builds the all-error snapshot written when the
// pass cannot run at all (missing ffprobe, failed extraction). It keeps
// the "every source has a row" contract in one place.
func DegradedSourceMetadata(cause string) *SourceMetadata {
	snap := &SourceMetadata{
		Schema:     SourceMetadataSchema,
		SourceKind: "unknown",
		Fields:     make(map[string]SourceField, len(tagPrecedence)),
	}
	return degradedSnapshot(snap, cause)
}

// degradedSnapshot marks every field as errored with cause, leaving a
// structurally complete document. Per-file data (including per-file probe
// errors) is retained: the degraded field message quotes the first file's
// error, and consumers can see which probes failed.
func degradedSnapshot(snap *SourceMetadata, cause string) *SourceMetadata {
	for _, f := range fieldKeys {
		snap.Fields[f] = SourceField{Values: []string{}, Source: SourceDegraded, Status: StatusError, Error: cause}
	}
	if snap.Files == nil {
		snap.Files = []SourceFile{}
	}
	if snap.RawTags == nil {
		snap.RawTags = []SourceFileTags{}
	}
	snap.DurationSeconds = 0
	return snap
}

// finalizeEmpty writes a complete snapshot for a source with zero audio
// files: every field is explicitly missing, nothing was probed.
func finalizeEmpty(snap *SourceMetadata) *SourceMetadata {
	for _, f := range fieldKeys {
		snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusMissing}
	}
	snap.Files = []SourceFile{}
	snap.RawTags = []SourceFileTags{}
	return snap
}

// discoverAudioFiles walks a source folder incrementally (64-entry
// batches) keeping regular audio files, bounded by maxDiscoveredEntries
// scanned entries, maxProbedParts matched files, and the pass deadline.
// The kept paths are sorted so probe and merge order is deterministic.
func discoverAudioFiles(ctx context.Context, dir string, snap *SourceMetadata) ([]string, error) {
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()

	var kept []string
	scanned := 0
	for {
		batch, readErr := d.ReadDir(64)
		for _, e := range batch {
			scanned++
			if e.Type().IsRegular() && IsAudioFile(e.Name()) {
				kept = append(kept, filepath.Join(dir, e.Name()))
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
		if ctx.Err() != nil || scanned >= maxDiscoveredEntries || len(kept) >= maxProbedParts {
			snap.Truncated = true
			break
		}
	}
	sort.Strings(kept)
	if len(kept) > maxProbedParts {
		kept = kept[:maxProbedParts]
	}
	return kept, nil
}

// probeFiles runs ffprobe over every path in order, recording per-file
// duration and effective tags, and returns the merged per-field value
// lists. Returns the number of files whose probe failed.
func probeFiles(ctx context.Context, ffprobe string, paths []string, snap *SourceMetadata) (map[string][]string, int) {
	merged := make(map[string][]string, len(tagPrecedence))
	seen := make(map[string]map[string]bool, len(tagPrecedence))
	probedErrs := 0
	rawBytes := 0

	for _, path := range paths {
		if ctx.Err() != nil || rawBytes > maxRawTagBytes {
			snap.Truncated = true
			break
		}
		snap.FilesProbed++
		snap.Files = append(snap.Files, SourceFile{File: filepath.Base(path)})

		effective, duration, probeErr := probeOne(ctx, ffprobe, path)
		if probeErr != "" {
			snap.Files[len(snap.Files)-1].Error = probeErr
			probedErrs++
			continue
		}
		snap.Files[len(snap.Files)-1].DurationSeconds = duration
		snap.DurationSeconds += duration

		// Retention cap: stop adding raw entries, keep merged values.
		approx := len(filepath.Base(path))
		for k, v := range effective {
			approx += len(k) + len(v) + 8
		}
		if rawBytes+approx <= maxRawTagBytes {
			snap.RawTags = append(snap.RawTags, SourceFileTags{File: filepath.Base(path), Tags: effective})
			rawBytes += approx
		} else {
			snap.Truncated = true
		}

		// Tag keys are matched case-insensitively (ID3 and M4B spell
		// the same tag differently); RawTags above keeps the original
		// spellings.
		normalized := make(map[string]string, len(effective))
		for k, v := range effective {
			normalized[strings.ToLower(k)] = v
		}
		mergeDoc(merged, seen, normalized)
	}
	return merged, probedErrs
}

// mergeDoc folds one file's lowercased-key tags into the per-field value
// lists (distinct, first-seen order, capped per field).
func mergeDoc(merged map[string][]string, seen map[string]map[string]bool, doc map[string]string) {
	for field, keys := range tagPrecedence {
		for _, key := range keys {
			value, ok := doc[key]
			if !ok {
				continue
			}
			if field == FieldSeriesPart {
				if slash := strings.IndexByte(value, '/'); slash >= 0 {
					value = value[:slash]
				}
			}
			if value == "" {
				continue
			}
			addValue(merged, seen, field, value)
			break
		}
	}
}

func addValue(merged map[string][]string, seen map[string]map[string]bool, field, value string) {
	if s, ok := seen[field]; ok {
		if s[value] || len(merged[field]) >= maxDistinctValuesPerField {
			return
		}
		s[value] = true
	} else {
		seen[field] = map[string]bool{value: true}
	}
	merged[field] = append(merged[field], value)
}

// probeOne runs a single bounded ffprobe and returns the effective tag
// map, the parsed duration, and an error message ("" on success).
func probeOne(ctx context.Context, ffprobe, path string) (map[string]string, float64, string) {
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	stderr := &limitedWriter{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, fmt.Sprintf("probe pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Sprintf("start probe: %v", err)
	}

	var out []byte
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := io.ReadFull(stdout, tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return nil, 0, fmt.Sprintf("read probe output: %v; stderr: %s", rerr, string(stderr.b))
		}
		if len(out) > maxProbeOutputBytes {
			return nil, 0, "probe output exceeds 1 MiB"
		}
	}
	runErr := cmd.Wait()
	if runErr != nil {
		return nil, 0, fmt.Sprintf("ffprobe: %v; stderr: %s", runErr, string(stderr.b))
	}

	var doc struct {
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			CodecType string            `json:"codec_type"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&doc); err != nil {
		return nil, 0, fmt.Sprintf("parse probe output: %v", err)
	}

	// Effective tags: format-level tags overlaid by the first audio
	// stream's tags (stream-level keys win: the narrower scope is the
	// more specific claim).
	effective := make(map[string]string, len(doc.Format.Tags))
	for k, v := range doc.Format.Tags {
		effective[k] = v
	}
	for _, s := range doc.Streams {
		if s.CodecType == "audio" {
			for k, v := range s.Tags {
				effective[k] = v
			}
			break
		}
	}
	for k, v := range effective {
		if len(v) > maxTagValueBytes {
			effective[k] = v[:maxTagValueBytes] + "…"
		}
	}

	duration, _ := strconv.ParseFloat(doc.Format.Duration, 64)
	return effective, duration, ""
}

// mergeFields turns the accumulated value lists into per-field states.
func mergeFields(snap *SourceMetadata, merged map[string][]string) {
	for _, f := range fieldKeys {
		values := merged[f]
		if len(values) > 0 {
			snap.Fields[f] = SourceField{Values: values, Source: SourceEmbedded, Status: StatusPresent}
			continue
		}
		if !snap.incomplete() {
			snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusMissing}
			continue
		}
		snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusError, Error: snap.incompleteMsg()}
	}
}

// incomplete reports whether the pass ended without probing every file.
func (s *SourceMetadata) incomplete() bool {
	return s.Truncated || s.FilesProbed < s.FilesTotal
}

// incompleteMsg is the shared message for fields that came up empty under
// an incomplete pass.
func (s *SourceMetadata) incompleteMsg() string {
	errored := 0
	for _, file := range s.Files {
		if file.Error != "" {
			errored++
		}
	}
	return fmt.Sprintf("extraction incomplete: %d errored, %d unprobed of %d files",
		errored, s.FilesTotal-s.FilesProbed, s.FilesTotal)
}

// applyASINFallback scans basenames for an ASIN when no embedded value
// exists.
func applyASINFallback(snap *SourceMetadata, srcPath string) {
	if f, ok := snap.Fields[FieldASIN]; ok && len(f.Values) > 0 {
		return
	}

	names := []string{filepath.Base(srcPath)}
	for _, file := range snap.Files {
		names = append(names, file.File)
	}
	seen := map[string]bool{}
	var values []string
	for _, name := range names {
		stripped := name
		if ext := filepath.Ext(stripped); ext != "" {
			stripped = stripped[:len(stripped)-len(ext)]
		}
		for _, m := range asinPattern.FindAllString(stripped, -1) {
			upper := strings.ToUpper(m)
			if !seen[upper] {
				seen[upper] = true
				values = append(values, upper)
			}
		}
	}
	if len(values) > 0 {
		snap.Fields[FieldASIN] = SourceField{Values: values, Source: SourceFilename, Status: StatusPresent}
		return
	}
	if !snap.incomplete() {
		snap.Fields[FieldASIN] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusMissing}
		return
	}
	snap.Fields[FieldASIN] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusError, Error: snap.incompleteMsg()}
}

// finalize guarantees JSON-serializable non-nil slices on a successful
// (non-degraded, non-empty) snapshot.
func finalize(snap *SourceMetadata) *SourceMetadata {
	if snap.Files == nil {
		snap.Files = []SourceFile{}
	}
	if snap.RawTags == nil {
		snap.RawTags = []SourceFileTags{}
	}
	for _, f := range fieldKeys {
		if sf, ok := snap.Fields[f]; ok && sf.Values == nil {
			sf.Values = []string{}
			snap.Fields[f] = sf
		}
	}
	return snap
}
