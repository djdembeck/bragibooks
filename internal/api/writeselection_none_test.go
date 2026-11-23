package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// TestQueueJobNoneSelectedFreezesNonNilEmptySelection pins the #518
// none-selected invariant through the FULL stored-row round trip: a queued
// job whose saved selection is the explicit empty list must store
// selection.fields as a non-nil empty slice (JSON []), NOT null. A nil
// fields slice deserializes as the default all-supported-available baseline
// in buildEngineJob, which would write every supported field — the exact
// failure mode append([]string(nil)) of an empty source produces. The
// length-only assertions in TestLoadSnapshotForQueueFreezesSelection cannot
// catch this (len(nil) == 0), so the JSON round trip is asserted directly.
func TestQueueJobNoneSelectedFreezesNonNilEmptySelection(t *testing.T) {
	h, svc := newTestJobsHandler(t)
	svc.proc = testProcessor(svc.proc)
	bookID := seedTestBook(t, h.svc.DB, 1, "Book One")
	seedSnapshot(t, h.svc.DB, bookID)
	srcPath := t.TempDir() + "/in.m4a"
	if err := os.WriteFile(srcPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	noneSel := []string{}
	svc.proc.ApplyWriteSelection(&noneSel, audio.ChaptersPolicyValuePreserve)
	jobID, err := svc.QueueJob(context.Background(), bookID, []string{srcPath}, queueDestination{}, "")
	if err != nil {
		t.Fatalf("QueueJob (none-selected): %v", err)
	}

	var raw string
	if err := h.svc.DB.QueryRow(`SELECT m4b_merge_args FROM processing_jobs WHERE id = ?`, jobID).Scan(&raw); err != nil {
		t.Fatalf("read job row: %v", err)
	}

	// The stored JSON must carry an explicit [] for fields, not null: the
	// nil-vs-empty distinction is what the worker keys the default off.
	var wire struct {
		Snapshot *struct {
			Selection *struct {
				Fields *[]string `json:"fields"`
			} `json:"selection"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("decode job args: %v (raw %s)", err, raw)
	}
	if wire.Snapshot == nil || wire.Snapshot.Selection == nil {
		t.Fatalf("stored selection = %s, want a frozen none-selected selection", raw)
	}
	if wire.Snapshot.Selection.Fields == nil {
		t.Fatalf("stored selection.fields is null (%s): a none-selected job must freeze a NON-NIL empty list, not the nil default", raw)
	}
	if len(*wire.Snapshot.Selection.Fields) != 0 {
		t.Fatalf("stored selection.fields = %v, want empty", *wire.Snapshot.Selection.Fields)
	}

	// And the worker-side read must see the same: a deserialized non-nil
	// empty selection, which buildEngineJob turns into all-preserve.
	sel, ok := storedSelection(t, h.svc.DB, jobID)
	if !ok || sel == nil {
		t.Fatalf("storedSelection = (%#v, %v), want a frozen selection", sel, ok)
	}
	if sel.Fields == nil {
		t.Fatalf("worker-visible selection.Fields is nil; want non-nil empty (would derive the all-supported default)")
	}
	if len(sel.Fields) != 0 {
		t.Fatalf("worker-visible selection.Fields = %v, want empty", sel.Fields)
	}
}
