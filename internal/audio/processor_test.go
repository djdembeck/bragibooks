package audio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckSemver(t *testing.T) {
	tests := []struct {
		got, want string
		fail      bool
	}{
		{"1.0.0", "1.0.0", false},
		{"1.0.1", "1.0.0", false},
		{"1.1.0", "1.0.0", false},
		{"2.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", true},
		{"0.1.0", "1.0.0", true},
		{"1.0.0", "1.0.1", true},
		{"0.0.1", "1.0.0", true},
		{"2.0.0", "1.9.9", false},
		{"1.1.0", "1.0.9", false},
		// Short / v-prefixed forms must not panic and parse as 0-padded.
		{"2", "1.0.0", false},
		{"2", "2.0.0", false},
		{"2", "2.1.0", true},
		{"v2", "1.0.0", false},
		{"v0.9", "1.0.0", true},
		{"1.0", "1.0.0", false},
		{"1.0.0", "2", true},
		{"0.0.0", "", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s vs %s", tt.got, tt.want), func(t *testing.T) {
			err := checkSemver(tt.got, tt.want)
			if tt.fail && err == nil {
				t.Errorf("expected error for %s < %s, got nil", tt.got, tt.want)
			}
			if !tt.fail && err != nil {
				t.Errorf("expected nil for %s >= %s, got %v", tt.got, tt.want, err)
			}
		})
	}
}

func TestFindNewestM4B(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "Author", "Old.m4b")
	if err := os.MkdirAll(filepath.Dir(older), 0o755); err != nil {
		t.Fatal(err)
	}
	olderTime := time.Now().Add(-10 * time.Minute)
	if err := os.WriteFile(older, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(older, olderTime, olderTime); err != nil {
		t.Fatal(err)
	}
	// A pre-existing file from before the cutoff must be ignored.
	stale := filepath.Join(dir, "Stale.m4b")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(dir, "Author", "New.m4b")
	if err := os.WriteFile(newest, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	since := time.Now().Add(-5 * time.Minute)
	got, err := findNewestM4B(dir, since)
	if err != nil {
		t.Fatalf("findNewestM4B: %v", err)
	}
	want, _ := filepath.Abs(newest)
	if got != want {
		t.Errorf("findNewestM4B = %q, want %q", got, want)
	}

	// Nothing newer than a recent cutoff: empty, no error.
	got, err = findNewestM4B(dir, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("findNewestM4B (no new): %v", err)
	}
	if got != "" {
		t.Errorf("findNewestM4B (no new) = %q, want empty", got)
	}
}

// writeFakeM4b writes an executable shell script that mimics m4b-merge: it
// honors -o <dir>, creates <dir>/Author/Title.m4b, and prints "merged".
func writeFakeM4b(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fake-m4b")
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift; fi
  shift
done
mkdir -p "$out/Author"
printf 'book' > "$out/Author/Title.m4b"
echo merged
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return p
}

// TestRunWritesToPerJobSubtree covers the row-13 discovery choice: with a
// jobID, m4b-merge runs against OutputDir/<jobID>.tmp and the result is
// moved back to its scheme path under OutputDir. A fresher .m4b sitting in
// the shared output tree (a concurrent job's output under the old
// unscoped walk) must not be picked up.
func TestRunWritesToPerJobSubtree(t *testing.T) {
	out := t.TempDir()
	bin := writeFakeM4b(t, t.TempDir())

	// A decoy .m4b with a future mtime: per-job discovery (scoped to the
	// workdir) skips it, but the old unscoped walk of OutputDir would
	// consider it the newest file.
	decoy := filepath.Join(out, "Other", "Decoy.m4b")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("decoy"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(decoy, future, future); err != nil {
		t.Fatal(err)
	}

	proc := NewProcessor(bin, out, "", 2, "{author}/{title}", "info")
	res, err := proc.Run(context.Background(), "j1", []string{"/tmp/x/a.m4b"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, _ := filepath.Abs(filepath.Join(out, "Author", "Title.m4b"))
	if res.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", res.OutputFile, want)
	}
	if !strings.Contains(res.Stdout, "merged") {
		t.Errorf("Stdout = %q, want the fake binary's \"merged\" line", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "j1.tmp")); !os.IsNotExist(err) {
		t.Errorf("per-job subtree j1.tmp not cleaned up (stat err = %v)", err)
	}
	// The decoy must remain untouched.
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("decoy missing after run: %v", err)
	}
}

// TestRunWithRelativeOutputDir is the regression test for the relative
// output_root fix: with a relative configured output dir, discovery (which
// yields absolute paths) and the final move must agree, OutputFile must be
// non-empty, and the produced .m4b must survive the per-job cleanup.
func TestRunWithRelativeOutputDir(t *testing.T) {
	base := t.TempDir()
	out := "out" // relative: resolved against the chdir'd parent
	bin := writeFakeM4b(t, t.TempDir())

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	proc := NewProcessor(bin, out, "", 2, "{author}/{title}", "info")
	res, err := proc.Run(context.Background(), "rel1", []string{"/tmp/x/a.m4b"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := filepath.Join(base, out, "Author", "Title.m4b")
	if res.OutputFile == "" {
		t.Fatalf("OutputFile is empty; the relative output root broke discovery/move")
	}
	if res.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", res.OutputFile, want)
	}
	if _, statErr := os.Stat(want); statErr != nil {
		t.Errorf("produced .m4b missing after run: %v", statErr)
	}
	if _, err := os.Stat(filepath.Join(base, out, "rel1.tmp")); !os.IsNotExist(err) {
		t.Errorf("per-job subtree rel1.tmp not cleaned up (stat err = %v)", err)
	}
}

// TestRunDrainsLargeStderr covers the pipe-drain fix: the child writes more
// than the 64KB pipe buffer to stderr before exiting. Sequential
// ReadAll(stdout) then ReadAll(stderr) would deadlock; concurrent drains
// must return with both streams captured.
func TestRunDrainsLargeStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "big-stderr")
	script := `#!/bin/sh
echo stdout-line
yes X | head -c 200000 >&2
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	proc := NewProcessor(bin, "", "", 1, "{title}", "info")
	res, err := proc.Run(context.Background(), "", []string{"/tmp/x/a.m4b"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "stdout-line") {
		t.Errorf("Stdout = %q, want \"stdout-line\"", res.Stdout)
	}
	if len(res.Stderr) < 100000 {
		t.Errorf("Stderr length = %d, want the captured ~200KB flood", len(res.Stderr))
	}
}
