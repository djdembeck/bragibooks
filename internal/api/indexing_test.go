package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/config"
	dbpkg "github.com/bragibooks/bragibooks/internal/db"
	_ "modernc.org/sqlite"
)

// newIndexTestService builds an IndexService over a migrated temp DB with a
// temp input root. ffprobe is left empty so no subprocess is needed.
func newIndexTestService(t *testing.T, inputDir string) (*IndexService, *config.ConfigManager) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = inputDir
	cm.Store(cm.Config())
	return NewIndexService(db, cm, ""), cm
}

// TestIndexServiceGenerationStableAcrossCoalesce fires two rapid refresh
// requests before the scan runs and asserts both observe the same
// generation and the scan completes for it.
func TestIndexServiceGenerationStableAcrossCoalesce(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"a/x", "b/y"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := newIndexTestService(t, root)
	// No worker: the cap-1 channel never drains, so the first request stays
	// "queued" and the second must coalesce into it (same generation).
	st1 := svc.RequestScan()
	if st1.State.Status != "queued" {
		t.Fatalf("first request status = %s, want queued", st1.State.Status)
	}
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation {
		t.Fatalf("coalesced generations differ: %d vs %d", st1.State.Generation, st2.State.Generation)
	}
	if st2.State.Status != "queued" {
		t.Fatalf("coalesced status = %s, want queued", st2.State.Status)
	}

	// With the worker running, that accepted generation must reach a
	// terminal state.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	gen := st1.State.Generation
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := svc.Status()
		if st.State.Generation == gen && st.State.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not finish: %+v", st.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// root/a, root/a/x, root/b, root/b/y = 4 paths.
	if st := svc.Status(); st.State.Entries != 4 {
		t.Errorf("entries = %d, want 4", st.State.Entries)
	}
}

// TestIndexServiceCrashRecovery seeds a persisted non-terminal status (what
// a SIGKILL mid-scan leaves — the token is consumed for a "running" scan,
// the rescan token is still queued for "queued") and asserts a fresh
// service supersedes it: boot marks the dead generation "interrupted",
// and the first RequestScan runs a NEW scan (a higher generation), not a
// coalesce into the dead one.
func TestIndexServiceCrashRecovery(t *testing.T) {
	for _, status := range []string{"running", "queued"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(t.TempDir(), "index.db")
			db, err := dbpkg.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := dbpkg.RunMigrations(db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(
				`INSERT INTO index_state (id, root, entries, last_run_at, status, detail, generation) VALUES (1, ?, 0, NULL, ?, '', 4)`,
				root, status,
			); err != nil {
				t.Fatal(err)
			}
			cm := config.NewConfigManager()
			cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
			cm.Config().Directories.InputDir = root
			cm.Store(cm.Config())

			// Fresh boot: the dead non-terminal generation must be
			// marked interrupted (not left pollable as non-terminal).
			svc := NewIndexService(db, cm, "")
			if svc.Status().State.Status != "interrupted" {
				t.Fatalf("boot status = %s, want interrupted (crash recovery from %q)", svc.Status().State.Status, status)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go svc.Run(ctx)

			st := svc.RequestScan()
			if st.State.Generation <= 4 {
				t.Fatalf("generation %d not superseding dead generation 4", st.State.Generation)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				cur := svc.Status()
				if cur.State.Status == "done" && cur.State.Generation == st.State.Generation {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("scan after crash recovery did not finish: %+v", cur.State)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// TestIndexServiceTruncatedWalkDoesNotPrune pins the no-prune invariant:
// a walk cut short by the depth cap must not delete rows for paths the
// truncated walk never reached.
func TestIndexServiceTruncatedWalkDoesNotPrune(t *testing.T) {
	root := t.TempDir()
	// "zzz" is protected: indexed by a complete scan, then placed AFTER the
	// truncation point in walk order (lexical: aaa < zzz).
	prot := filepath.Join(root, "zzz")
	if err := os.MkdirAll(prot, 0o755); err != nil {
		t.Fatal(err)
	}
	svc, cm := newIndexTestService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	run := func() IndexState {
		st := svc.RequestScan()
		deadline := time.Now().Add(30 * time.Second)
		for {
			cur := svc.Status()
			if cur.State.Generation == st.State.Generation &&
				cur.State.Status != "queued" && cur.State.Status != "running" {
				return cur.State
			}
			if time.Now().After(deadline) {
				t.Fatalf("scan did not finish: %+v", cur.State)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	st := run()
	if st.Status != "done" {
		t.Fatalf("complete scan status = %s, want done", st.Status)
	}
	if st.Entries != 1 {
		t.Fatalf("complete scan entries = %d, want 1", st.Entries)
	}

	// Now add a tree that trips the depth cap before the walk reaches zzz.
	deep := filepath.Join(root, "aaa")
	for range 35 {
		deep = filepath.Join(deep, "n")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = cm

	st = run()
	if st.Status != "truncated" {
		t.Fatalf("deep scan status = %s, want truncated (%s)", st.Status, st.Detail)
	}
	db := svc.db
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_index WHERE path = ?`, prot).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("protected path pruned by truncated walk: %d rows, want 1", n)
	}
}

// TestIndexServiceShutdownQueued pins the shutdown contract for a request
// that is QUEUED (token in the req channel, scan never started) when the
// context cancels: it must end at "interrupted", not stuck (and not at a
// "done" from the worker racing ahead and consuming the token first). The
// request is queued BEFORE Run starts, with an ALREADY-cancelled context:
// the worker's ctx.Done() select arm fires before it can drain the token,
// so the cancellation path is exercised deterministically.
func TestIndexServiceShutdownQueued(t *testing.T) {
	root := t.TempDir()
	svc, _ := newIndexTestService(t, root)

	// Queue the request first: no worker is running, so the token sits
	// unconsumed in the req channel.
	st := svc.RequestScan()
	if st.State.Status != "queued" {
		t.Fatalf("pre-queue status = %s, want queued", st.State.Status)
	}

	// Then start the worker with an already-cancelled context: the only
	// reachable arm is ctx.Done(), so the queued token dies un-consumed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	go svc.Run(ctx)
	svc.Wait()
	if got := svc.Status().State.Generation; got != st.State.Generation {
		t.Fatalf("generation changed after shutdown: %d vs %d", got, st.State.Generation)
	}
	if st := svc.Status(); st.State.Status != "interrupted" {
		t.Fatalf("post-shutdown status = %s, want interrupted (the token was never consumed)", st.State.Status)
	}
	// The persisted row (what a fresh boot's loadState would see) is
	// terminal too.
	var status string
	if err := svc.db.QueryRow(`SELECT status FROM index_state WHERE id = 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "interrupted" {
		t.Fatalf("persisted status = %s, want interrupted", status)
	}
}

// TestIndexServiceShutdownQueuedRescanBothTerminal pins the shutdown
// contract for a mid-scan root change: the running scan's terminal
// regresses the mirror to its OWN generation, leaving the queued rescan
// generation without a terminal. Cancelling while that rescan token is
// still queued must leave BOTH generations terminal — the poller for the
// queued (accepted) generation must never hang on a non-terminal.
func TestIndexServiceShutdownQueuedRescanBothTerminal(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	// A deep-ish tree so the walk outlasts the test's root change.
	for i := range 3000 {
		if err := os.MkdirAll(filepath.Join(root1, "d", fmt.Sprintf("%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root2, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, cm := newIndexTestService(t, root1)
	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)

	st1 := svc.RequestScan()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if svc.Status().State.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not start: %+v", svc.Status().State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Root change mid-scan queues exactly one rescan generation.
	cfg := cm.Config()
	cfg.Directories.InputDir = root2
	cm.Store(cfg)
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation+1 {
		t.Fatalf("rescan generation = %d, want %d", st2.State.Generation, st1.State.Generation+1)
	}
	// Cancel immediately: the old scan's terminal (or its interruption)
	// regresses the mirror below the rescan's generation while its token
	// is still queued, and Run must interrupt the rescan generation too.
	cancel()
	svc.Wait()

	// The persisted row (what a fresh boot's loadState would see) must be
	// terminal for the HIGHEST accepted generation — non-terminal there
	// means a poller for the rescan's generation hangs until a boot.
	var status string
	var gen int64
	if err := svc.db.QueryRow(`SELECT status, generation FROM index_state WHERE id = 1`).Scan(&status, &gen); err != nil {
		t.Fatal(err)
	}
	if gen != st2.State.Generation {
		t.Fatalf("persisted generation = %d, want the rescan's %d", gen, st2.State.Generation)
	}
	if status != "interrupted" {
		t.Fatalf("persisted status = %s, want interrupted for the rescan generation", status)
	}
	// In-memory mirror: terminal for the rescan generation.
	st := svc.Status().State
	if st.Generation != st2.State.Generation {
		t.Fatalf("final generation = %d, want the rescan's %d", st.Generation, st2.State.Generation)
	}
	switch st.Status {
	case "interrupted", "done", "truncated":
	default:
		t.Fatalf("final status = %s, want a terminal state for the rescan generation", st.Status)
	}
}

// TestIndexHelpersWildcardAndRoot pins likeEscape/indexImmediateChild
// against the containment and pruning paths most likely to regress
// silently: LIKE wildcards in a literal prefix and the filesystem-root
// special case.
func TestIndexHelpersWildcardAndRoot(t *testing.T) {
	sep := string(os.PathSeparator)
	like := []struct {
		in, want string
	}{
		{"/a", "/a"},
		{`a\b`, `a\\b`},
		{`a\b%`, `a\\b\%`},
		{"a%b", `a\%b`},
		{"a_b", `a\_b`},
		{"a_%\\b", `a\_\%\\b`},
	}
	for _, tc := range like {
		if got := likeEscape(tc.in); got != tc.want {
			t.Errorf("likeEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	child := []struct {
		dir, cand string
		want      bool
	}{
		{"/", "/a", true},
		{"/", "/a/b", false},
		{"/a%", "/a%/x", true},
		{"/a%", "/a%x", false},
		{"/a_b", "/a_b/c", true},
		{"/a_b", "/a_b_c/d", false},
		{"/a" + sep + "b", "/a" + sep + "b" + sep + "c", true},
		{"/a" + sep + "b", "/a" + sep + "bc", false},
	}
	for _, tc := range child {
		if got := indexImmediateChild(tc.dir, tc.cand); got != tc.want {
			t.Errorf("indexImmediateChild(%q, %q) = %v, want %v", tc.dir, tc.cand, got, tc.want)
		}
	}
}

// TestRequestScanRootChangeQueuedCoalesces pins the generation contract
// when the input root changes while a rescan is queued: the second
// request must coalesce into the accepted generation, not bump a new one
// that no scan will ever terminate (activeRoot must be cleared at every
// terminal state).
func TestRequestScanRootChangeQueuedCoalesces(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	svc, cm := newIndexTestService(t, root1)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)

	// Complete a scan on root1, then stop the worker.
	st0 := svc.RequestScan()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := svc.Status()
		if st.State.Generation == st0.State.Generation && st.State.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first scan did not finish: %+v", st.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	svc.Wait()

	// Re-point the root: the rescan request is queued (no worker yet).
	cfg := cm.Config()
	cfg.Directories.InputDir = root2
	cm.Store(cfg)
	st1 := svc.RequestScan()
	if st1.State.Status != "queued" {
		t.Fatalf("root-change request status = %s, want queued", st1.State.Status)
	}
	// A repeated request before the worker consumes must coalesce.
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation {
		t.Fatalf("second request generation %d != queued generation %d (coalescing broken by stale active root)", st2.State.Generation, st1.State.Generation)
	}
}

// runToTerminal drives a RequestScan to its terminal state (or fails the
// test on timeout), returning the terminal state.
func runToTerminal(t *testing.T, svc *IndexService) IndexState {
	t.Helper()
	st := svc.RequestScan()
	deadline := time.Now().Add(30 * time.Second)
	for {
		cur := svc.Status()
		if cur.State.Generation == st.State.Generation &&
			cur.State.Status != "queued" && cur.State.Status != "running" {
			return cur.State
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not finish: %+v", cur.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mustWalk runs one walk and fails on truncation/cancellation.
func mustWalk(t *testing.T, svc *IndexService, root string) []indexRow {
	t.Helper()
	rows, truncated, detail, cancelled := svc.walk(context.Background(), root)
	if truncated || cancelled {
		t.Fatalf("walk truncated=%v cancelled=%v detail=%q", truncated, cancelled, detail)
	}
	return rows
}

// completeSnapPayload is a stored snapshot that sourceMetadataComplete
// accepts, so classify treats the row as stable.
func completeSnapPayload() string {
	return `{"schema":2,"source_kind":"file","files_total":1,"files_probed":1,"degraded":false,"files":[{"file":"x.mp3","duration_seconds":1}],"fields":{"asin":{"values":["B000000000"],"source":"embedded","status":"present"}}}`
}

// storeIndexRow seeds a stored index row with the given signals.
func storeIndexRow(t *testing.T, db *sql.DB, path string, size int64, modtime, payload string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		path, filepath.Base(path), "file", size, modtime, payload)
	if err != nil {
		t.Fatalf("seed row %s: %v", path, err)
	}
}

// TestClassifyTerminatesOnVanishedStoredRow pins the cursor fix: a stored
// row that sorts BEFORE a later walked path (with no walked row matching
// it — the row vanished) must be consumed by the merge, not pinned, or the
// loop spins forever on the walked path.
func TestClassifyTerminatesOnVanishedStoredRow(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stored row "dir/a.mp3" is gone from the filesystem; "dir/z.txt"
	// exists. "dir/a.mp3" < "dir/z.txt", so the vanished row sorts before
	// the walked path that would expose a stale cursor.
	storeIndexRow(t, db, filepath.Join(root, "dir", "a.mp3"), 10, "2026-01-01T00:00:00.000000000Z", completeSnapPayload())
	if err := os.WriteFile(filepath.Join(root, "dir", "z.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")

	done := make(chan struct{})
	go func() {
		_, extract := svc.classify(mustWalk(t, svc, root), root)
		_ = extract
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("classify did not terminate on vanished stored row (stale cursor)")
	}
}

// TestClassifyTerminatesOnVanishedLastStoredRow is the same hazard with the
// vanished row as the LAST stored row.
func TestClassifyTerminatesOnVanishedLastStoredRow(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	// The only stored row is gone; the only walked path sorts after it.
	storeIndexRow(t, db, filepath.Join(root, "a.mp3"), 10, "2026-01-01T00:00:00.000000000Z", completeSnapPayload())
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")

	done := make(chan struct{})
	go func() {
		_, _ = svc.classify(mustWalk(t, svc, root), root)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("classify did not terminate on vanished last stored row (stale cursor)")
	}
}

// TestClassifyDirectoryReextractOnChildChange pins directory snapshot
// invalidation: a folder with a complete snapshot must land in the extract
// set when a new file appears under it (its own mtime is not consulted).
func TestClassifyDirectoryReextractOnChildChange(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "folder")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	// Stored: the folder row (complete snapshot) and its one child file.
	storeIndexRow(t, db, folder, 0, "2026-01-01T00:00:00.000000000Z", completeSnapPayload())
	child := filepath.Join(folder, "c.mp3")
	storeIndexRow(t, db, child, 10, "2026-01-01T00:00:00.000000000Z", completeSnapPayload())
	if err := os.WriteFile(child, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the folder's on-disk mtime to exactly the stored signal so the
	// test isolates the child-change rule (a real entry-add would bump it).
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(folder, ts, ts)

	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")

	// Add a second file under the folder; the folder's stored snapshot no
	// longer covers the subtree, so it must be re-extracted.
	if err := os.WriteFile(filepath.Join(folder, "d.mp3"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore the folder mtime to the stored signal again (the add bumped
	// it; the rule under test is the child change, not the mtime bump).
	_ = os.Chtimes(folder, ts, ts)

	_, extract := svc.classify(mustWalk(t, svc, root), root)
	found := false
	for _, r := range extract {
		if r.Path == folder {
			found = true
		}
	}
	if !found {
		t.Errorf("folder with changed children not in extract set: %+v", extract)
	}
}

// TestClassifyNonAudioFileSkipsExtraction pins the non-audio extract gate:
// a non-audio file gets a complete (empty) snapshot and never re-enters the
// extract set on a later refresh.
func TestClassifyNonAudioFileSkipsExtraction(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")

	// First pass: the non-audio file is new, so it is extracted once.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, extract1 := svc.classify(mustWalk(t, svc, root), root)
	if len(extract1) != 1 || extract1[0].Path != filepath.Join(root, "notes.txt") {
		t.Fatalf("first pass extract = %+v, want the non-audio file", extract1)
	}
	if extract1[0].Payload == "" {
		t.Fatal("non-audio file must carry a complete (empty) snapshot, not a live probe")
	}

	// Simulate the row landing (upsertAll is not run here: seed the
	// walked signals with the snapshot classify built), then a second
	// refresh: the file must be unchanged (size/modtime match AND
	// complete stored payload), absent from the extract set.
	info, err := os.Stat(extract1[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_index (path, name, type, size, modtime, payload, indexed_at, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		extract1[0].Path, "notes.txt", "file", info.Size(),
		info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"),
		extract1[0].Payload); err != nil {
		t.Fatal(err)
	}
	_, extract2 := svc.classify(mustWalk(t, svc, root), root)
	for _, r := range extract2 {
		if r.Path == filepath.Join(root, "notes.txt") {
			t.Errorf("non-audio file re-extracted on second refresh: %+v", extract2)
		}
	}
}

// TestIndexServiceRootChangeMidScanStableGeneration pins the generation
// contract for a root change during a running scan: the rescan's generation
// is stable under repeated requests, and the running scan publishes only
// its OWN generation's terminal.
func TestIndexServiceRootChangeMidScanStableGeneration(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root1, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, cm := newIndexTestService(t, root1)

	// No worker: first request stays queued, second coalesces.
	st1 := svc.RequestScan()
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation {
		t.Fatalf("queued generations differ: %d vs %d", st2.State.Generation, st1.State.Generation)
	}
	gen1 := st1.State.Generation

	// Root changes while "queued" (worker not started): the accepted scan
	// has not run yet, so the rescan COALESCES into its generation (the
	// scan reads the root at start and will scan root2) — the generation
	// must stay stable under repeated requests in the window.
	cfg := cm.Config()
	cfg.Directories.InputDir = root2
	cm.Store(cfg)
	st3 := svc.RequestScan()
	if st3.State.Generation != gen1 {
		t.Fatalf("root-change generation = %d, want stable %d (queued scan retargets)", st3.State.Generation, gen1)
	}
	st4 := svc.RequestScan()
	if st4.State.Generation != gen1 {
		t.Fatalf("repeat request generation = %d, want stable %d", st4.State.Generation, gen1)
	}

	// The worker must run ONE scan for generation gen1 and publish its
	// terminal for the NEW root — never a terminal claiming the old root.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	deadline := time.Now().Add(30 * time.Second)
	for {
		cur := svc.Status()
		if cur.State.Status == "done" || cur.State.Status == "truncated" ||
			cur.State.Status == "error" || cur.State.Status == "interrupted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not finish: %+v", cur.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := svc.Status()
	if st.State.Generation != gen1 {
		t.Fatalf("terminal generation = %d, want %d", st.State.Generation, gen1)
	}
	if st.State.Status != "done" {
		t.Fatalf("terminal status = %s, want done (detail %q)", st.State.Status, st.State.Detail)
	}
	if st.State.Root != canonicalRoot(root2) {
		t.Fatalf("terminal root = %s, want %s", st.State.Root, canonicalRoot(root2))
	}
}

// TestIndexServiceEmptyRootIsError pins the empty-configured-dir guard: a
// missing/empty input dir must end in an "error" terminal naming the
// problem — never a zero-entry scan of the process cwd.
func TestIndexServiceEmptyRootIsError(t *testing.T) {
	svc, _ := newIndexTestService(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if st.Root != "" {
		t.Fatalf("root = %q, want empty (no authoritative root published)", st.Root)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM source_index`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("source_index has %d rows, want 0", n)
	}
}

// TestIndexServiceMissingRootPreservesPriorRoot pins the stat-failure rule:
// when a configured root disappears, the terminal must NOT publish the
// attempted root (the listing guard would serve stale rows for it) — the
// prior indexed root is preserved.
func TestIndexServiceMissingRootPreservesPriorRoot(t *testing.T) {
	root := t.TempDir()
	svc, cm := newIndexTestService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("initial scan status = %s, want done", st.Status)
	}
	priorRoot := st.Root

	// Re-point at a non-existent directory.
	cfg := cm.Config()
	cfg.Directories.InputDir = filepath.Join(root, "gone")
	cm.Store(cfg)
	st = runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if st.Root != priorRoot {
		t.Fatalf("root = %q, want prior root %q preserved", st.Root, priorRoot)
	}
}

// TestIndexServiceSymlinkedRootScanned pins symlinked-root resolution: the
// scan must see the target's contents (WalkDir never follows a symlinked
// root) and store the resolved path as the root.
func TestIndexServiceSymlinkedRootScanned(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realRoot, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	svc, _ := newIndexTestService(t, link)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("status = %s, want done (detail %q)", st.Status, st.Detail)
	}
	// The symlinked root itself is not indexed as a directory row; the
	// target's children are (a single directory "a" here).
	if st.Entries != 1 {
		t.Fatalf("entries = %d, want 1 (a)", st.Entries)
	}
	if st.Root != realRoot {
		t.Fatalf("root = %q, want resolved %q", st.Root, realRoot)
	}
}

// TestDeletePrefixRemovesDescendants pins the LIKE-escape order: rows
// UNDER the old root (not just the root path itself) must be deleted,
// including a descendant whose name contains LIKE metacharacters.
func TestDeletePrefixRemovesDescendants(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")

	oldRoot := "/data/old root"
	paths := []string{
		oldRoot,
		oldRoot + "/x.mp3",
		oldRoot + "/a%b/c.mp3", // metacharacters in a descendant name
	}
	for _, p := range paths {
		storeIndexRow(t, db, p, 1, "2026-01-01T00:00:00.000000000Z", completeSnapPayload())
	}
	if err := svc.deletePrefix(oldRoot); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM source_index WHERE path = ?`, p).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("row %s survived deletePrefix", p)
		}
	}
}

// TestIndexServiceModtimePrecision pins subsecond change detection: a
// same-size rewrite within the same second must be classified as changed.
func TestIndexServiceModtimePrecision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.mp3")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, _ := newIndexTestService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("initial scan status = %s, want done", st.Status)
	}
	// Same size, rewrite (mtime moves; same second on coarse clocks).
	if err := os.WriteFile(path, []byte("1111111111"), 0o644); err != nil {
		t.Fatal(err)
	}
	st = runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("rescan status = %s, want done", st.Status)
	}
	// The rewritten row must have been re-extracted: with ffprobe empty it
	// lands as a degraded payload — proof it was NOT classified unchanged.
	var payload string
	if err := svc.db.QueryRow(`SELECT payload FROM source_index WHERE path = ?`, path).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var snap audio.SourceMetadata
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		t.Fatalf("payload not json: %v", err)
	}
	if !snap.Degraded {
		t.Fatalf("rewritten row not re-extracted (no degraded payload): %s", payload)
	}
}

// TestIndexServiceRootChangeMidRunningScan pins the in-flight rescan
// contract: a root change while a scan is RUNNING queues exactly one
// rescan generation, stable under repeated requests; the old scan's
// terminal publishes for its OWN generation/root, and the rescan's
// terminal lands for the new root.
func TestIndexServiceRootChangeMidRunningScan(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	// A deep-ish tree so the walk outlasts the test's root change.
	for i := 0; i < 3000; i++ {
		if err := os.MkdirAll(filepath.Join(root1, "d", fmt.Sprintf("%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root2, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, cm := newIndexTestService(t, root1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	st1 := svc.RequestScan()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if svc.Status().State.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not start: %+v", svc.Status().State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Root change while the walk is in flight.
	cfg := cm.Config()
	cfg.Directories.InputDir = root2
	cm.Store(cfg)
	st2 := svc.RequestScan()
	if st2.State.Generation != st1.State.Generation+1 {
		t.Fatalf("rescan generation = %d, want %d", st2.State.Generation, st1.State.Generation+1)
	}
	// Repeated refreshes in the window must coalesce into the rescan.
	for i := 0; i < 5; i++ {
		if st := svc.RequestScan(); st.State.Generation != st2.State.Generation {
			t.Fatalf("repeat %d generation = %d, want stable %d", i, st.State.Generation, st2.State.Generation)
		}
	}
	// The old scan's terminal (its own generation) is transient — the
	// rescan supersedes it at once — so assert the invariant instead: no
	// terminal ever claims the RESCAN's generation for the OLD root (the
	// original bug), and the final terminal is the rescan's, for the new
	// root.
	deadline = time.Now().Add(30 * time.Second)
	oldRoot, newRoot := canonicalRoot(root1), canonicalRoot(root2)
	for {
		st := svc.Status().State
		if (st.Status == "done" || st.Status == "truncated") &&
			st.Generation == st2.State.Generation && st.Root == oldRoot {
			t.Fatalf("old scan published a terminal for the rescan's generation %d with the old root %s", st2.State.Generation, oldRoot)
		}
		if st.Status == "done" && st.Generation == st2.State.Generation && st.Root == newRoot {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rescan terminal not observed: %+v", st)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestIndexServicePersistenceFailureIsTerminal pins the persistence
// contract: an upsert failure must end the scan in "error" WITHOUT
// publishing the attempted root (nothing was stored, so claiming done
// would assert an index the table does not have).
func TestIndexServicePersistenceFailureIsTerminal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, _ := newIndexTestService(t, root)
	// Deterministic upsert failure: every INSERT into source_index aborts.
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON source_index
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if st.Root != "" {
		t.Fatalf("root = %q, want empty (failed scan must not publish its attempted root)", st.Root)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM source_index`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("source_index has %d rows, want 0", n)
	}
}

// seedPriorRootScan drives one clean scan to "done" so a prior root is the
// authoritative stored root, then re-points the configured input dir.
func seedPriorRootScan(t *testing.T, svc *IndexService, cm *config.ConfigManager, priorRoot, newRoot string) {
	t.Helper()
	cm.Config().Directories.InputDir = priorRoot
	cm.Store(cm.Config())
	if st := runToTerminal(t, svc); st.Status != "done" {
		t.Fatalf("prior-root scan = %s (detail %q), want done", st.Status, st.Detail)
	}
	cm.Config().Directories.InputDir = newRoot
	cm.Store(cm.Config())
}

// TestIndexServiceDeletePrefixFailureIsTerminal pins the root-change
// persistence contract: when clearing the prior root's rows fails, the
// scan must end in "error" WITHOUT publishing the attempted new root —
// the mixed old/new rows would be served as the index, so the prior
// authoritative root is preserved.
func TestIndexServiceDeletePrefixFailureIsTerminal(t *testing.T) {
	priorRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(priorRoot, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorRoot, "d", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(newRoot, "n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "n", "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, cm := newIndexTestService(t, priorRoot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	seedPriorRootScan(t, svc, cm, priorRoot, newRoot)
	// Deterministic persistence failure: every DELETE from source_index
	// aborts. On this root change the first DELETE the scan issues is
	// deletePrefix's (clearing the prior root's rows); even if the
	// delete had not run, the same trigger fires in pruneVanished, so
	// the contract under test (terminal error, prior root preserved)
	// is the same at either persistence step.
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON source_index
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	st := runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("deletePrefix failure: status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if st.Root != priorRoot {
		t.Fatalf("deletePrefix failure: root = %q, want the prior root %q (a failed root-change must not publish the attempted root)", st.Root, priorRoot)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM source_index`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("deletePrefix failure: source_index is empty, want the prior root's rows preserved")
	}
}

// TestIndexServicePruneFailureIsTerminal pins the prune persistence
// contract: when deleting vanished rows fails, the scan must end in
// "error" WITHOUT publishing the attempted root — the vanished rows
// would be served as if still present, so the prior authoritative root
// is preserved.
func TestIndexServicePruneFailureIsTerminal(t *testing.T) {
	priorRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(priorRoot, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.txt", "gone.txt"} {
		if err := os.WriteFile(filepath.Join(priorRoot, "d", name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := newIndexTestService(t, priorRoot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	// Index the full tree, then remove one file: the next scan's walk
	// sees a vanished row, so pruneVanished must delete it.
	if st := runToTerminal(t, svc); st.Status != "done" {
		t.Fatalf("prior-root scan = %s (detail %q), want done", st.Status, st.Detail)
	}
	if err := os.Remove(filepath.Join(priorRoot, "d", "gone.txt")); err != nil {
		t.Fatal(err)
	}
	// Deterministic pruneVanished failure: every DELETE from
	// source_index aborts (upsertAll writes via INSERT ... ON CONFLICT,
	// which is an INSERT, not a DELETE, so it is unaffected).
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON source_index
		BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	st := runToTerminal(t, svc)
	if st.Status != "error" {
		t.Fatalf("status = %s, want error (detail %q)", st.Status, st.Detail)
	}
	if st.Root != priorRoot {
		t.Fatalf("root = %q, want the prior root %q (a failed prune must not publish the attempted root)", st.Root, priorRoot)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM source_index`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// The vanished row must still be present (its delete was aborted).
	if n < 2 {
		t.Fatalf("source_index has %d rows, want >= 2 (the vanished row's delete was aborted)", n)
	}
}

// newIndexHandler builds a Handler wired to the index service's DB and
// config, so handler endpoints (refresh, directories index mode) run
// against a real migrated index.
func newIndexHandler(t *testing.T, svc *IndexService, cm *config.ConfigManager, db *sql.DB) *Handler {
	t.Helper()
	return &Handler{svc: &Services{DB: db, Config: cm, IndexSvc: svc}}
}

// TestRefreshIndexHandler pins the POST /api/index/refresh contract:
// enabled → 202 with the queued status the caller polls (the scan is
// scheduled by the handler, not run inline); disabled → its 400 response.
func TestRefreshIndexHandler(t *testing.T) {
	t.Run("enabled-queues-scan", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(t.TempDir(), "index.db")
		db, err := dbpkg.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := dbpkg.RunMigrations(db); err != nil {
			t.Fatal(err)
		}
		cm := config.NewConfigManager()
		cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
		cm.Config().Directories.InputDir = root
		cm.Config().Directories.IndexingEnabled = true
		cm.Store(cm.Config())
		svc := NewIndexService(db, cm, "")
		h := newIndexHandler(t, svc, cm, db)

		rec := httptest.NewRecorder()
		h.RefreshIndex(rec, httptest.NewRequest(http.MethodPost, "/api/index/refresh", nil))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("POST refresh: status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		var got IndexStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !got.Enabled {
			t.Fatal("response enabled = false, want true")
		}
		if got.State.Status != "queued" {
			t.Fatalf("state.status = %s, want queued (handler must queue the scan, not run it)", got.State.Status)
		}
		if got.State.Generation != 1 {
			t.Fatalf("state.generation = %d, want 1 (the generation the caller polls)", got.State.Generation)
		}
	})

	t.Run("disabled-refused", func(t *testing.T) {
		root := t.TempDir()
		dbPath := filepath.Join(t.TempDir(), "index.db")
		db, err := dbpkg.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := dbpkg.RunMigrations(db); err != nil {
			t.Fatal(err)
		}
		cm := config.NewConfigManager()
		cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
		cm.Config().Directories.InputDir = root
		cm.Config().Directories.IndexingEnabled = false
		cm.Store(cm.Config())
		svc := NewIndexService(db, cm, "")
		h := newIndexHandler(t, svc, cm, db)

		rec := httptest.NewRecorder()
		h.RefreshIndex(rec, httptest.NewRequest(http.MethodPost, "/api/index/refresh", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST refresh (disabled): status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		var got map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got["error"] == "" {
			t.Fatalf("disabled response has no error: %s", rec.Body.String())
		}
		if st := svc.Status().State; st.Status != "never" || st.Generation != 0 {
			t.Fatalf("disabled request must not schedule a scan: %+v", st)
		}
	})
}

// TestListDirectoriesIndexMode pins GET /api/directories?index=1: when the
// index is enabled AND the stored rows belong to the current root, the
// listing is served from the index (IndexedDirectoriesResponse: per-entry
// evidence + the index state); when the stored rows are from a PREVIOUS
// root (re-pointed input, rescan not landed), the filesystem fallback
// serves the plain listing — a stale index never masquerades as the
// current directory.
func TestListDirectoriesIndexMode(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"a", "a/x.mp3", "b"} {
		p := filepath.Join(root, sub)
		if sub == "a/x.mp3" {
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	dbPath := filepath.Join(t.TempDir(), "index.db")
	db, err := dbpkg.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dbpkg.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Config().Directories.IndexingEnabled = true
	cm.Store(cm.Config())
	svc := NewIndexService(db, cm, "")
	h := newIndexHandler(t, svc, cm, db)

	// Land a complete scan so the stored index matches the current root.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	st := runToTerminal(t, svc)
	if st.Status != "done" {
		t.Fatalf("scan = %s (detail %q), want done", st.Status, st.Detail)
	}

	t.Run("indexed-response-shape", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/directories?path="+url.QueryEscape(root)+"&index=1", nil)
		rec := httptest.NewRecorder()
		h.ListDirectories(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET index mode: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["path"] != root {
			t.Fatalf("path = %v, want %s", body["path"], root)
		}
		// Index state is present (the indexed response shape).
		idx, ok := body["index"].(map[string]any)
		if !ok {
			t.Fatalf("response has no index state (not the indexed shape): %s", rec.Body.String())
		}
		if idx["status"] != "done" {
			t.Fatalf("index.status = %v, want done", idx["status"])
		}
		entries, ok := body["entries"].([]any)
		if !ok {
			t.Fatalf("entries = %T, want array", body["entries"])
		}
		// Dirs first, name ascending: a, b, then the file under a is NOT
		// an immediate child of the root.
		if len(entries) != 2 {
			t.Fatalf("entries = %d, want 2 (a, b)", len(entries))
		}
		first, _ := entries[0].(map[string]any)
		if first["name"] != "a" || first["type"] != "dir" {
			t.Fatalf("first entry = %v, want dir a", first)
		}
		// Per-entry evidence is derived from the stored payload.
		ev, ok := first["evidence"].(map[string]any)
		if !ok {
			t.Fatalf("entry a has no evidence: %v", first)
		}
		if ev["title"] == "" {
			t.Fatalf("evidence.title empty: %v", ev)
		}
	})

	t.Run("root-mismatch-falls-back-to-filesystem", func(t *testing.T) {
		// Re-point the input root: the stored rows belong to the PREVIOUS
		// root, so the index must not answer for the new one.
		newRoot := t.TempDir()
		if err := os.MkdirAll(filepath.Join(newRoot, "fresh"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := cm.Config()
		cfg.Directories.InputDir = newRoot
		cm.Store(cfg)

		req := httptest.NewRequest(http.MethodGet, "/api/directories?path="+url.QueryEscape(newRoot)+"&index=1", nil)
		rec := httptest.NewRecorder()
		h.ListDirectories(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET fallback: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, present := body["index"]; present {
			t.Fatalf("stale-root response carries the index state (the index answered for a root it does not own): %s", rec.Body.String())
		}
		entries, _ := body["entries"].([]any)
		if len(entries) != 1 {
			t.Fatalf("entries = %v, want 1 (fresh, from the filesystem)", body["entries"])
		}
		first, _ := entries[0].(map[string]any)
		if first["name"] != "fresh" {
			t.Fatalf("first entry = %v, want dir fresh (filesystem fallback)", first)
		}
	})
}
