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
// SourceMetadata document. Version 2 added the snapshot Degraded flag and
// the per-field Truncated flag; a version 1 payload cannot express either,
// so a reader must treat it as incomplete evidence (see
// api.sourceMetadataComplete) rather than trusting its derived defaults.
const SourceMetadataSchema = 2

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
	// Truncated marks a value list the per-field cap cut short: further
	// distinct values existed in the source and were dropped. It is
	// evidence about THIS field only — the snapshot-level Truncated flag
	// covers discovery, probe, and raw-tag bounds.
	Truncated bool `json:"truncated,omitempty"`
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
	Schema      int    `json:"schema"`
	SourceKind  string `json:"source_kind"` // "file" | "folder" | "unknown"
	FilesTotal  int    `json:"files_total"`
	FilesProbed int    `json:"files_probed"`
	Truncated   bool   `json:"truncated"`
	// Degraded marks a snapshot no file produced usable evidence for: the
	// source was unreadable (stat failure), discovery failed, or every
	// probe failed. Its surviving values (e.g. a filename ASIN) are not
	// complete evidence.
	Degraded        bool                   `json:"degraded"`
	DurationSeconds float64                `json:"duration_seconds"` // sum of probed, non-errored files
	Files           []SourceFile           `json:"files"`
	Fields          map[string]SourceField `json:"fields"`
	RawTags         []SourceFileTags       `json:"raw_tags"`
}

// Boundedness of the extraction pass.
const (
	sourceMetadataTimeout = 60 * time.Second
	maxDiscoveredEntries  = 1000
	maxProbedParts        = 100
	// maxDistinctValuesPerField caps the distinct values retained per
	// field in a snapshot's merged value lists. A cap hit is recorded on
	// the field itself (SourceField.Truncated), never inferred by a reader
	// from the retained length: a list of exactly the cap length is
	// indistinguishable from an exactly-capped one.
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
		Schema:     SourceMetadataSchema,
		SourceKind: "unknown",
		Fields:     make(map[string]SourceField, len(tagPrecedence)),
	}

	stat, err := os.Stat(srcPath)
	if err != nil {
		snap = degradedSnapshot(snap, err.Error())
		// Filename extraction needs no successful probe: a stat failure
		// (renamed source, vanished file) still leaves a scannable name.
		// Set the field only when a value is found so a clean degradation
		// stays all-error.
		if values := findASINs([]string{filepath.Base(srcPath)}); len(values) > 0 {
			snap.Fields[FieldASIN] = SourceField{Values: values, Source: SourceFilename, Status: StatusPresent}
		}
		return snap, nil
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

	merged, mergedTruncated, probedErrs := probeFiles(passCtx, ffprobe, paths, snap)
	if probedErrs == len(paths) {
		firstErr := ""
		if len(snap.Files) > 0 {
			firstErr = snap.Files[0].Error
		}
		// Filename extraction needs no successful probe; run it after the
		// degraded override so a found value is not written over by the
		// all-error marking.
		snap = degradedSnapshot(snap, fmt.Sprintf("all %d probed files failed: %s", len(paths), firstErr))
		applyASINFallback(snap, srcPath)
		return snap, nil
	}

	mergeFields(snap, merged, mergedTruncated)
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
	snap.Degraded = true
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
// files. A clean scan marks every field explicitly missing; a truncated
// scan (bound hit before the directory was exhausted) cannot claim the
// unscanned entries held nothing, so the fields are errored instead.
func finalizeEmpty(snap *SourceMetadata) *SourceMetadata {
	snap.Files = []SourceFile{}
	snap.RawTags = []SourceFileTags{}
	for _, f := range fieldKeys {
		if snap.Truncated {
			snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusError,
				Error: "extraction incomplete: discovery truncated before audio files were found"}
			continue
		}
		snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusMissing}
	}
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
// lists plus the set of fields whose list the per-field cap cut short.
// Returns the number of files whose probe failed.
func probeFiles(ctx context.Context, ffprobe string, paths []string, snap *SourceMetadata) (map[string][]string, map[string]bool, int) {
	merged := make(map[string][]string, len(tagPrecedence))
	seen := make(map[string]map[string]bool, len(tagPrecedence))
	truncated := make(map[string]bool, len(tagPrecedence))
	probedErrs := 0
	rawBytes := 0

	for _, path := range paths {
		if ctx.Err() != nil || rawBytes > maxRawTagBytes {
			snap.Truncated = true
			break
		}
		snap.FilesProbed++
		snap.Files = append(snap.Files, SourceFile{File: filepath.Base(path)})

		effective, lowered, duration, probeErr := probeOne(ctx, ffprobe, path)
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
		// spellings. `lowered` folds the effective overlay
		// case-insensitively with stream scope winning, so same-key,
		// different-case format/stream collisions are deterministic.
		mergeDoc(merged, seen, truncated, lowered)
	}
	return merged, truncated, probedErrs
}

// mergeDoc folds one file's lowercased-key tags into the per-field value
// lists (distinct, first-seen order, capped per field).
func mergeDoc(merged map[string][]string, seen map[string]map[string]bool, truncated map[string]bool, doc map[string]string) {
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
			addValue(merged, seen, truncated, field, value)
			break
		}
	}
}

// addValue appends one distinct value to a field's list and records a cap
// hit on that field. The cap is silent evidence-wise unless it is
// recorded here: a retained list of exactly maxDistinctValuesPerField
// entries is otherwise indistinguishable from an exactly-capped one, and
// a reader cannot tell a dropped differing value from a duplicate.
func addValue(merged map[string][]string, seen map[string]map[string]bool, truncated map[string]bool, field, value string) {
	if s, ok := seen[field]; ok {
		if s[value] {
			return
		}
		if len(merged[field]) >= maxDistinctValuesPerField {
			truncated[field] = true
			return
		}
		s[value] = true
	} else {
		seen[field] = map[string]bool{value: true}
	}
	merged[field] = append(merged[field], value)
}

// probeOne runs a single bounded ffprobe and returns the original-case
// effective tag map, the same tags lowercased, the parsed duration, and
// an error message ("" on success).
func probeOne(ctx context.Context, ffprobe, path string) (map[string]string, map[string]string, float64, string) {
	cmd := exec.CommandContext(ctx, ffprobe,
		// ffprobe infers the format from file content, so a crafted
		// playlist or container can reference remote resources; restrict
		// it to local protocols.
		"-protocol_whitelist", "file,pipe,data",
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	stderr := &limitedWriter{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, 0, fmt.Sprintf("probe pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, 0, fmt.Sprintf("start probe: %v", err)
	}

	// reap kills the child and reaps it after closing stdout, so an early
	// return leaves no process blocked on the pipe.
	reap := func() {
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}

	var out []byte
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := io.ReadFull(stdout, tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if len(out) > maxProbeOutputBytes {
			reap()
			return nil, nil, 0, "probe output exceeds 1 MiB"
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			reap()
			return nil, nil, 0, fmt.Sprintf("read probe output: %v; stderr: %s", rerr, string(stderr.b))
		}
	}
	runErr := cmd.Wait()
	if runErr != nil {
		return nil, nil, 0, fmt.Sprintf("ffprobe: %v; stderr: %s", runErr, string(stderr.b))
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
		return nil, nil, 0, fmt.Sprintf("parse probe output: %v", err)
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

	lowered := foldProbeTags(doc.Format.Tags, firstAudioStreamTags(doc.Streams))

	duration, _ := strconv.ParseFloat(doc.Format.Duration, 64)
	return effective, lowered, duration, ""
}

// firstAudioStreamTags returns the tags of the first audio stream, or nil.
func firstAudioStreamTags(streams []struct {
	CodecType string            `json:"codec_type"`
	Tags      map[string]string `json:"tags"`
}) map[string]string {
	for _, s := range streams {
		if s.CodecType == "audio" {
			return s.Tags
		}
	}
	return nil
}

// foldProbeTags case-folds the format/stream tag overlay into a lowercase
// key map. A same-key/different-case collision (TITLE vs title) is decided
// by scope (the narrower stream scope wins), and a collision WITHIN one
// scope is decided by lexicographic key order — never map iteration order
// (finding: nondeterministic extracted values). Collecting the source pairs
// first and inserting them in sorted (scope, key) order makes the winner of
// any collision deterministic. Values are capped at maxTagValueBytes,
// matching the effective-tag pass.
func foldProbeTags(formatTags, streamTags map[string]string) map[string]string {
	type tagPair struct {
		scope int // 0 = format, 1 = stream
		key   string
		val   string
	}
	var pairs []tagPair
	for k, v := range formatTags {
		pairs = append(pairs, tagPair{scope: 0, key: k, val: v})
	}
	for k, v := range streamTags {
		pairs = append(pairs, tagPair{scope: 1, key: k, val: v})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].scope != pairs[j].scope {
			return pairs[i].scope < pairs[j].scope
		}
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return false
	})
	lowered := make(map[string]string, len(pairs))
	for _, p := range pairs {
		lowered[strings.ToLower(p.key)] = p.val
	}
	for k, v := range lowered {
		if len(v) > maxTagValueBytes {
			lowered[k] = v[:maxTagValueBytes] + "…"
		}
	}
	return lowered
}

// mergeFields turns the accumulated value lists into per-field states.
// truncated carries the fields whose list the per-field cap cut short; the
// flag rides on the field so a reader never has to infer it from the
// retained length.
func mergeFields(snap *SourceMetadata, merged map[string][]string, truncated map[string]bool) {
	for _, f := range fieldKeys {
		values := merged[f]
		if len(values) > 0 {
			snap.Fields[f] = SourceField{Values: values, Source: SourceEmbedded, Status: StatusPresent,
				Truncated: truncated[f]}
			continue
		}
		if !snap.incomplete() {
			snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusMissing}
			continue
		}
		snap.Fields[f] = SourceField{Values: []string{}, Source: SourceEmbedded, Status: StatusError, Error: snap.incompleteMsg()}
	}
}

// incomplete reports whether the pass ended without a clean probe of
// every file: a degraded pass (unreadable source, discovery failure, or
// all probes failed), unprobed files, truncated discovery, or any probe
// error — in each case a field that came up empty cannot claim the
// source "has no such tag", and a surviving value is not complete
// evidence.
func (s *SourceMetadata) incomplete() bool {
	if s.Degraded || s.Truncated || s.FilesProbed < s.FilesTotal {
		return true
	}
	for _, file := range s.Files {
		if file.Error != "" {
			return true
		}
	}
	return false
}

// Complete reports whether the pass ended with a clean probe of every
// discovered file: not degraded, no truncation, no unprobed files, and
// no per-file probe errors. A field value that survived a partial pass
// is not complete evidence.
func (s *SourceMetadata) Complete() bool {
	return !s.incomplete()
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

// findASINs scans basenames (extension stripped) for ASINs, returning the
// distinct matches normalized to uppercase in first-seen order.
func findASINs(names []string) []string {
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
	return values
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
	values := findASINs(names)
	if len(values) > 0 {
		snap.Fields[FieldASIN] = SourceField{Values: values, Source: SourceFilename, Status: StatusPresent}
		return
	}
	// A degraded all-error pass already wrote the ASIN field with its own
	// cause; without a filename value there is nothing to improve, so keep
	// the degraded field rather than re-deriving a narrower error message.
	if f, ok := snap.Fields[FieldASIN]; ok && f.Source == SourceDegraded {
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
