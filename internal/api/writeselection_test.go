package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// TestBuildEngineJobSelectionTitleOnly guards the per-field write selection
// (#518): an explicit title-only selection must write the title and
// PRESERVE every other scalar field (the unselected subtitle in particular,
// per M1's independent title/subtitle component policies), preserve the
// cover (cover is not in the selection), and preserve chapters (no sidecar).
func TestBuildEngineJobSelectionTitleOnly(t *testing.T) {
	subtitle := "Sub"
	year := 2020
	snap := BookSnapshot{
		BookID:   7,
		Title:    "T",
		Subtitle: subtitle,
		Authors:  []string{"A"},
		Year:     &year,
	}
	sel := &WriteSelection{Fields: []string{"title"}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Selection: sel}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if jf.Policies.Title != audio.WritePolicyValueWrite {
		t.Fatalf("title policy = %q, want write", jf.Policies.Title)
	}
	for name, got := range map[string]string{
		"subtitle":    jf.Policies.Subtitle,
		"authors":     jf.Policies.Authors,
		"narrators":   jf.Policies.Narrators,
		"series":      jf.Policies.Series,
		"description": jf.Policies.Description,
		"genres":      jf.Policies.Genres,
		"year":        jf.Policies.Year,
	} {
		if got != audio.WritePolicyValuePreserve {
			t.Fatalf("unselected field %s policy = %q, want preserve", name, got)
		}
	}
	if string(jf.Cover) != `"preserve"` {
		t.Fatalf("cover = %s, want \"preserve\"", jf.Cover)
	}
	if string(jf.Chapters) != `"preserve"` {
		t.Fatalf("chapters = %s, want \"preserve\" (no sidecar)", jf.Chapters)
	}
}

// TestBuildEngineJobSelectionTrimsStoredFieldNames pins the normalization
// contract: a stored selection whose field names carry padding (startup
// validation trims, so a legacy/hand-edited value can) must be applied via
// the NORMALIZED list. Writing policies from the raw strings would silently
// skip the recognized field's write.
func TestBuildEngineJobSelectionTrimsStoredFieldNames(t *testing.T) {
	snap := BookSnapshot{BookID: 7, Title: "T", Authors: []string{"A"}}
	sel := &WriteSelection{Fields: []string{" title ", "authors"}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Selection: sel}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if jf.Policies.Title != audio.WritePolicyValueWrite {
		t.Fatalf("title policy = %q, want write (a padded stored name must still select the field)", jf.Policies.Title)
	}
	if jf.Policies.Authors != audio.WritePolicyValueWrite {
		t.Fatalf("authors policy = %q, want write", jf.Policies.Authors)
	}
	if jf.Policies.Subtitle != audio.WritePolicyValuePreserve {
		t.Fatalf("subtitle policy = %q, want preserve (unselected)", jf.Policies.Subtitle)
	}
}

// TestBuildEngineJobSelectionNoneSelected guards the none-selected contract:
// an EXPLICIT empty selection writes no embedded tag, cover, or chapter,
// and generates no chapter sidecar, while the effective metadata (the
// engine's input_supplied) is still complete for naming.
func TestBuildEngineJobSelectionNoneSelected(t *testing.T) {
	subtitle := "Sub"
	snap := BookSnapshot{BookID: 7, Title: "T", Subtitle: subtitle, Authors: []string{"A"}, CoverFile: "ignored.jpg"}
	sel := &WriteSelection{Fields: []string{}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	jf, staged, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Selection: sel}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if staged != "" {
		t.Fatalf("staged = %q, want empty (no cover mutation)", staged)
	}
	for name, got := range map[string]string{
		"title": jf.Policies.Title, "subtitle": jf.Policies.Subtitle, "authors": jf.Policies.Authors,
		"narrators": jf.Policies.Narrators, "series": jf.Policies.Series, "description": jf.Policies.Description,
		"genres": jf.Policies.Genres, "year": jf.Policies.Year,
	} {
		if got != audio.WritePolicyValuePreserve {
			t.Fatalf("none-selected %s policy = %q, want preserve", name, got)
		}
	}
	if string(jf.Cover) != `"preserve"` {
		t.Fatalf("cover = %s, want \"preserve\"", jf.Cover)
	}
	if string(jf.Chapters) != `"preserve"` {
		t.Fatalf("chapters = %s, want \"preserve\"", jf.Chapters)
	}
	// Naming still uses the full effective metadata.
	if jf.InputSupplied.Title != "T" || jf.InputSupplied.Subtitle == nil || *jf.InputSupplied.Subtitle != "Sub" {
		t.Fatalf("input_supplied = %+v, want the full effective metadata (naming is unaffected)", jf.InputSupplied)
	}
}

// TestBuildEngineJobSelectionSubtitleOnly guards the independent subtitle
// write: a subtitle-only selection writes the subtitle while preserving the
// title (M1's independent component policies), and preserves the cover.
func TestBuildEngineJobSelectionSubtitleOnly(t *testing.T) {
	subtitle := "Sub"
	snap := BookSnapshot{BookID: 7, Title: "T", Subtitle: subtitle, Authors: []string{"A"}}
	sel := &WriteSelection{Fields: []string{"subtitle"}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{Selection: sel}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob: %v", err)
	}
	if jf.Policies.Subtitle != audio.WritePolicyValueWrite {
		t.Fatalf("subtitle policy = %q, want write", jf.Policies.Subtitle)
	}
	if jf.Policies.Title != audio.WritePolicyValuePreserve {
		t.Fatalf("title policy = %q, want preserve (subtitle-only write must not touch the title)", jf.Policies.Title)
	}
}

// TestBuildEngineJobSelectionNoneHasNoDefaultBehavior guards the
// default-vs-explicit distinction: a NIL selection (never saved) is the
// all-supported-available baseline (every field with a value writes), while
// an explicit empty selection writes nothing.
func TestBuildEngineJobSelectionNoneHasNoDefaultBehavior(t *testing.T) {
	subtitle := "Sub"
	snap := BookSnapshot{BookID: 7, Title: "T", Subtitle: subtitle, Authors: []string{"A"}}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: &snapshotJSON{}}, "job-7", snap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob (default): %v", err)
	}
	if jf.Policies.Title != audio.WritePolicyValueWrite || jf.Policies.Subtitle != audio.WritePolicyValueWrite || jf.Policies.Authors != audio.WritePolicyValueWrite {
		t.Fatalf("default selection policies = %+v, want all-supported-available writes", jf.Policies)
	}
}

// TestValidateWriteSelectionRejectsCorrupted guards the worker-side defense:
// a stored selection with an unknown field, a blank entry, or an out-of
// vocabulary cover/chapter policy must fail before the job runs.
func TestValidateWriteSelectionRejectsCorrupted(t *testing.T) {
	valid := &WriteSelection{Fields: []string{"title"}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	if err := validateWriteSelection(nil); err != nil {
		t.Fatalf("nil selection: %v", err)
	}
	if err := validateWriteSelection(valid); err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
	badField := &WriteSelection{Fields: []string{"isbn"}, Cover: audio.WritePolicyValuePreserve, Chapters: audio.ChaptersPolicyValuePreserve}
	if err := validateWriteSelection(badField); err == nil {
		t.Fatal("unknown field accepted")
	}
	badCover := &WriteSelection{Fields: []string{"title"}, Cover: "clear", Chapters: audio.ChaptersPolicyValuePreserve}
	if err := validateWriteSelection(badCover); err == nil {
		t.Fatal("out-of-vocabulary cover policy accepted (clear is a per-book editor operation, never a frozen job value)")
	}
	badChapters := &WriteSelection{Fields: []string{"title"}, Cover: audio.WritePolicyValuePreserve, Chapters: "bogus"}
	if err := validateWriteSelection(badChapters); err == nil {
		t.Fatal("out-of-vocabulary chapter policy accepted")
	}
}

// TestLoadSnapshotForQueueFreezesSelection guards the queue-time freeze
// (#518): the saved selection at queue time becomes part of the job's
// immutable snapshot, and a later settings change cannot alter it.
func TestLoadSnapshotForQueueFreezesSelection(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	seedSnapshot(t, h.svc.DB, bookID)

	// No saved selection: the queue-time snapshot carries NO selection
	// (byte-identical to the pre-selection shape) and the worker derives
	// the all-supported-available baseline.
	snap, err := svc.loadSnapshotForQueue(bookID)
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (default): %v", err)
	}
	if snap == nil || snap.Selection != nil {
		t.Fatalf("default queue snapshot selection = %#v, want nil (no explicit selection)", snap)
	}

	// An explicit selection freezes into the snapshot: title-only writes
	// the title, cover is not selected so it preserves, chapters follow
	// the saved chapter policy.
	titleOnly := []string{"title"}
	svc.proc.ApplyWriteSelection(&titleOnly, audio.ChaptersPolicyValuePreserve)
	snap, err = svc.loadSnapshotForQueue(bookID)
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (title-only): %v", err)
	}
	if snap == nil || snap.Selection == nil {
		t.Fatalf("explicit queue snapshot = %#v, want a frozen selection", snap)
	}
	if len(snap.Selection.Fields) != 1 || snap.Selection.Fields[0] != "title" {
		t.Fatalf("frozen fields = %v, want [title]", snap.Selection.Fields)
	}
	if snap.Selection.Cover != audio.WritePolicyValuePreserve {
		t.Fatalf("frozen cover = %q, want preserve (cover not selected)", snap.Selection.Cover)
	}
	if snap.Selection.Chapters != audio.ChaptersPolicyValuePreserve {
		t.Fatalf("frozen chapters = %q, want preserve", snap.Selection.Chapters)
	}

	// A later settings change does NOT alter the already-frozen snapshot:
	// the next queue captures the NEW selection, but the prior value is
	// unchanged (a fresh load, not a mutation).
	noneSel := []string{}
	svc.proc.ApplyWriteSelection(&noneSel, audio.ChaptersPolicyValuePreserve)
	again, err := svc.loadSnapshotForQueue(bookID)
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (none): %v", err)
	}
	if again == nil || again.Selection == nil {
		t.Fatalf("none-selected queue snapshot = %#v, want a frozen empty selection", again)
	}
	if len(again.Selection.Fields) != 0 {
		t.Fatalf("none-selected frozen fields = %v, want empty", again.Selection.Fields)
	}
	// None-selected freezes cover AND chapters to preserve: no embedded
	// mutation at all.
	if again.Selection.Cover != audio.WritePolicyValuePreserve {
		t.Fatalf("none-selected cover = %q, want preserve", again.Selection.Cover)
	}
	if again.Selection.Chapters != audio.ChaptersPolicyValuePreserve {
		t.Fatalf("none-selected chapters = %q, want preserve", again.Selection.Chapters)
	}
}

// TestQueueChaptersPreserveWithDefaultFields guards the independent
// chapter setting (#518): a non-default chapter policy saved alongside the
// DEFAULT (nil) scalar selection must freeze into the queue-time snapshot
// — and the built job must honor it — even though no field selection was
// ever saved. The scalar baseline stays the all-supported-available
// default and the cover keeps its default write policy.
func TestQueueChaptersPreserveWithDefaultFields(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	seedSnapshot(t, h.svc.DB, bookID)

	// Only the chapter policy is saved; the scalar selection stays nil.
	svc.proc.ApplyWriteSelection(nil, audio.ChaptersPolicyValuePreserve)
	snap, err := svc.loadSnapshotForQueue(bookID)
	if err != nil {
		t.Fatalf("loadSnapshotForQueue (chapters-only): %v", err)
	}
	if snap == nil || snap.Selection == nil {
		t.Fatalf("chapters-only queue snapshot = %#v, want a frozen selection", snap)
	}
	if snap.Selection.Fields != nil {
		t.Fatalf("chapters-only frozen fields = %v, want nil (default scalar baseline)", snap.Selection.Fields)
	}
	if snap.Selection.Cover != audio.WritePolicyValueWrite {
		t.Fatalf("chapters-only frozen cover = %q, want write (no field selection pins cover)", snap.Selection.Cover)
	}
	if snap.Selection.Chapters != audio.ChaptersPolicyValuePreserve {
		t.Fatalf("chapters-only frozen chapters = %q, want preserve", snap.Selection.Chapters)
	}

	// The built job must honor the frozen chapter policy: chapters
	// preserve (no sidecar), scalar policies at the all-supported
	// default, cover at the default write.
	year := 2020
	bookSnap := BookSnapshot{BookID: bookID, Title: "Book One", Subtitle: "The Sub", Authors: []string{"A"}, Year: &year, Chapters: []SnapshotChapter{{Title: "Ch 1", StartMillis: 0, DurationMillis: 1000}}}
	jf, _, err := buildEngineJob(jobArgs{SrcPaths: []string{"/tmp/in.m4a"}, Snapshot: snap}, "job-1", bookSnap, t.TempDir(), "{title}")
	if err != nil {
		t.Fatalf("buildEngineJob (chapters-only): %v", err)
	}
	if string(jf.Chapters) != `"preserve"` {
		t.Fatalf("chapters-only job chapters = %s, want \"preserve\"", jf.Chapters)
	}
	if jf.Policies.Title != audio.WritePolicyValueWrite || jf.Policies.Subtitle != audio.WritePolicyValueWrite || jf.Policies.Authors != audio.WritePolicyValueWrite {
		t.Fatalf("chapters-only job scalar policies deviated from the all-supported default: %+v", jf.Policies)
	}
	if string(jf.Cover) != `"clear"` {
		t.Fatalf("chapters-only job cover = %s, want \"clear\" (default write, no staged cover)", jf.Cover)
	}
}

// seedTestBook inserts a minimal books row so the queue path's hard
// prerequisite (a book row) is met. The title is the snapshot's title.
func seedTestBook(t *testing.T, db *sql.DB, id int64, title string) int64 {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO books (id, title, status) VALUES (?, ?, 'matched')`, id, title); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	return id
}

// TestQueueJobFreezesSelectionInArgs end-to-end: QueueJob with an explicit
// selection produces a stored job row whose m4b_merge_args snapshot carries
// the frozen selection, and a settings change after queue leaves the stored
// row untouched.
func TestQueueJobFreezesSelectionInArgs(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	seedSnapshot(t, h.svc.DB, bookID)
	srcPath := t.TempDir() + "/in.m4a"
	if err := os.WriteFile(srcPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default (no saved selection): the stored row carries no selection.
	jobID, err := svc.QueueJob(context.Background(), bookID, []string{srcPath}, queueDestination{})
	if err != nil {
		t.Fatalf("QueueJob (default): %v", err)
	}
	sel, ok := storedSelection(t, h.svc.DB, jobID)
	if !ok || sel != nil {
		t.Fatalf("default stored selection = %#v, want absent", sel)
	}
	// The active-job guard refuses a second queue for the same book:
	// terminate the first job so the next queue captures the new selection.
	if _, err := h.svc.DB.Exec(`UPDATE processing_jobs SET status = 'error', completed_at = datetime('now') WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}

	// Explicit title+cover selection freezes into the stored row.
	titleCover := []string{"title", "cover"}
	svc.proc.ApplyWriteSelection(&titleCover, audio.ChaptersPolicyValueWrite)
	jobID, err = svc.QueueJob(context.Background(), bookID, []string{srcPath}, queueDestination{})
	if err != nil {
		t.Fatalf("QueueJob (title+cover): %v", err)
	}
	sel, ok = storedSelection(t, h.svc.DB, jobID)
	if !ok || sel == nil {
		t.Fatalf("explicit stored selection = %#v, want frozen", sel)
	}
	if len(sel.Fields) != 2 || sel.Fields[0] != "title" || sel.Fields[1] != "cover" {
		t.Fatalf("frozen fields = %v, want [title cover]", sel.Fields)
	}
	if sel.Cover != audio.WritePolicyValueWrite {
		t.Fatalf("frozen cover = %q, want write (cover selected)", sel.Cover)
	}
	if sel.Chapters != audio.ChaptersPolicyValueWrite {
		t.Fatalf("frozen chapters = %q, want write", sel.Chapters)
	}

	// A settings change after queue must NOT mutate the stored row.
	noneAfter := []string{}
	svc.proc.ApplyWriteSelection(&noneAfter, audio.ChaptersPolicyValuePreserve)
	sel, _ = storedSelection(t, h.svc.DB, jobID)
	if sel.Cover != audio.WritePolicyValueWrite || sel.Chapters != audio.ChaptersPolicyValueWrite {
		t.Fatalf("stored selection mutated by a later settings change: %+v", sel)
	}
}

// storedSelection reads a job row's m4b_merge_args and returns its frozen
// write selection (nil when absent). ok=false means the row is unreadable.
func storedSelection(t *testing.T, db *sql.DB, jobID string) (*WriteSelection, bool) {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT m4b_merge_args FROM processing_jobs WHERE id = ?`, jobID).Scan(&raw); err != nil {
		return nil, false
	}
	var args struct {
		Snapshot *snapshotJSON `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil || args.Snapshot == nil {
		return nil, false
	}
	return args.Snapshot.Selection, true
}
