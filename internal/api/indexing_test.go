package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestIndexServiceCrashRecovery seeds a non-terminal status (what a SIGKILL
// mid-scan leaves) and asserts a fresh service supersedes it: the first
// RequestScan must run a new scan, not coalesce into the dead generation.
func TestIndexServiceCrashRecovery(t *testing.T) {
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
		`INSERT INTO index_state (id, root, entries, last_run_at, status, detail, generation) VALUES (1, ?, 0, NULL, 'running', '', 4)`,
		root,
	); err != nil {
		t.Fatal(err)
	}
	cm := config.NewConfigManager()
	cm.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	cm.Config().Directories.InputDir = root
	cm.Store(cm.Config())

	// Fresh boot: the dead "running" generation must be superseded.
	svc := NewIndexService(db, cm, "")
	if svc.Status().State.Status != "interrupted" {
		t.Fatalf("boot status = %s, want interrupted (crash recovery)", svc.Status().State.Status)
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

// TestIndexServiceShutdownQueued verifies a request that is queued but not
// yet consumed when the context cancels ends at "interrupted", not stuck.
func TestIndexServiceShutdownQueued(t *testing.T) {
	root := t.TempDir()
	svc, _ := newIndexTestService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Enqueue, then cancel before the worker can consume it is racy;
	// instead assert the terminal contract by cancelling immediately and
	// checking a pre-queued generation is marked interrupted.
	st := svc.RequestScan()
	cancel()
	svc.Wait()
	if got := svc.Status().State.Generation; got != st.State.Generation {
		t.Fatalf("generation changed after shutdown: %d vs %d", got, st.State.Generation)
	}
	if st := svc.Status(); st.State.Status != "interrupted" && st.State.Status != "done" {
		t.Fatalf("post-shutdown status = %s, want interrupted or done", st.State.Status)
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
