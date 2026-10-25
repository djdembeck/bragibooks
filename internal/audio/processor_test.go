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

// TestRunRelocationFailureKeepsOutput is the regression test for the
// relocation-failure path: when the final move of the discovered .m4b
// fails, the run must return a real error (not a silent success with no
// output file) AND the per-job subtree must be preserved so the operator
// can recover the completed artifact.
func TestRunRelocationFailureKeepsOutput(t *testing.T) {
	out := t.TempDir()
	bin := writeFakeM4b(t, t.TempDir())

	// Block writing the final location: point the destination at a file,
	// so MkdirAll's parent would collide.
	blockedParent := filepath.Join(out, "Author")
	if err := os.WriteFile(blockedParent, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed blocked parent: %v", err)
	}

	proc := NewProcessor(bin, out, "", 1, "{author}/{title}", "info")
	_, err := proc.Run(context.Background(), "rel2", []string{"/tmp/x/a.m4b"}, "")
	if err == nil {
		t.Fatalf("Run: succeeded, want a relocation failure")
	}
	if !strings.Contains(err.Error(), "output") {
		t.Errorf("Run error = %q, want a relocation/output-path error", err)
	}
	// The per-job subtree that holds the only completed .m4b must
	// survive for recovery (NOT be RemoveAll'd on failure).
	workDir := filepath.Join(out, "rel2.tmp")
	if _, serr := os.Stat(workDir); serr != nil {
		t.Errorf("workDir %s missing after relocation failure: %v", workDir, serr)
	}
	found := ""
	_ = filepath.WalkDir(workDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".m4b") {
			found = p
		}
		return nil
	})
	if !strings.HasSuffix(found, ".m4b") {
		t.Errorf("completed .m4b not present under preserved workDir: %q", found)
	}
}

// TestRunRenameCopyFailurePreservesM4B covers the other relocation
// failure shape: MkdirAll succeeds because the final destination
// directory exists, but the rename itself fails (read-only destination
// dir — the copy-then-delete fallback is used precisely because the
// directory came up writable as far as MkdirAll is concerned). The run
// must still report a real error AND keep the per-job subtree, since the
// .m4b there is the only completed artifact.
func TestRunRenameCopyFailurePreservesM4B(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits; cannot make the destination read-only")
	}
	out := t.TempDir()
	bin := writeFakeM4b(t, t.TempDir())

	// Final destination dir exists, so MkdirAll passes; its read-only
	// mode makes the rename (and the copy fallback) fail.
	dst := filepath.Join(out, "Author")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o755) })

	proc := NewProcessor(bin, out, "", 1, "{author}/{title}", "info")
	_, err := proc.Run(context.Background(), "ren1", []string{"/tmp/x/a.m4b"}, "")
	if err == nil {
		t.Fatalf("Run: succeeded, want a move failure")
	}
	if !strings.Contains(err.Error(), "move output") {
		t.Errorf("Run error = %q, want the move-to-final-location error", err)
	}
	// The per-job subtree holding the completed .m4b must be preserved
	// (NOT RemoveAll'd on failure).
	workDir := filepath.Join(out, "ren1.tmp")
	found := ""
	_ = filepath.WalkDir(workDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".m4b") {
			found = p
		}
		return nil
	})
	if !strings.HasSuffix(found, ".m4b") {
		t.Errorf("completed .m4b not present under preserved workDir: %q", found)
	}
}

// TestRunMergeFailureCleansJobSubtree covers the opposite failure side:
// when the m4b-merge binary itself exits nonzero there is no output to
// recover, so the per-job subtree must be deleted.
func TestRunMergeFailureCleansJobSubtree(t *testing.T) {
	out := t.TempDir()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fail-merge")
	script := "#!/bin/sh\necho merge-failed >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	proc := NewProcessor(bin, out, "", 1, "{author}/{title}", "info")
	_, err := proc.Run(context.Background(), "mer1", []string{"/tmp/x/a.m4b"}, "")
	if err == nil {
		t.Fatalf("Run: succeeded, want a merge failure")
	}
	if !strings.Contains(err.Error(), "m4b-merge") {
		t.Errorf("Run error = %q, want an m4b-merge failure", err)
	}
	workDir := filepath.Join(out, "mer1.tmp")
	if _, serr := os.Stat(workDir); !os.IsNotExist(serr) {
		t.Errorf("per-job subtree mer1.tmp not cleaned up (stat err = %v)", serr)
	}
}

// TestStripEscapeSequences covers the job-log fix: m4b-merge's tracing
// output carries ANSI escapes even when stdout is a pipe, and those bytes
// must not reach processing_jobs.output (rendered verbatim in the UI).
func TestStripEscapeSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "tracing SGR line",
			in:   "\x1b[2m2026-10-24T22:06:07.897356Z\x1b[0m \x1b[32mINFO\x1b[0m \x1b[2mm4b_merge: merged\x1b[0m\n",
			want: "2026-10-24T22:06:07.897356Z INFO m4b_merge: merged\n",
		},
		{
			name: "cursor visibility",
			in:   "\x1b[?25lprogress\x1b[?25h",
			want: "progress",
		},
		{
			name: "OSC hyperlink with ST",
			in:   "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
			want: "link",
		},
		{
			name: "OSC title with BEL",
			in:   "\x1b]0;title\x07text",
			want: "text",
		},
		{
			name: "DCS payload",
			in:   "\x1bP1;2|data\x1b\\visible",
			want: "visible",
		},
		{
			name: "two-byte escape",
			in:   "before\x1bMafter",
			want: "beforeafter",
		},
		{
			name: "VT100 charset designation",
			in:   "before\x1b(Bafter",
			want: "beforeafter",
		},
		{
			name: "VT100 alternate charset designation",
			in:   "before\x1b)0after",
			want: "beforeafter",
		},
		{
			name: "escape with intermediate byte",
			in:   "a\x1b#8b",
			want: "ab",
		},
		{
			name: "truncated intermediate sequence",
			in:   "abc\x1b(",
			want: "abc",
		},
		{
			name: "literal bracket text is not a sequence",
			in:   "[32m INFO [0m\n",
			want: "[32m INFO [0m\n",
		},
		{
			name: "truncated CSI",
			in:   "abc\x1b[2",
			want: "abc",
		},
		{
			name: "plain text untouched",
			in:   "Author/Title.m4b\tmerged ✓\n",
			want: "Author/Title.m4b\tmerged ✓\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripEscapeSequences(tt.in); got != tt.want {
				t.Errorf("stripEscapeSequences(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRunStripsEscapeSequences is the end-to-end regression test for #439:
// the fake binary emits colored lines on both streams, and what Run returns
// (which processJob persists) must be plain text. The failing-binary case
// also covers the stderr text embedded in the persisted error message.
func TestRunStripsEscapeSequences(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "ansi-m4b")
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift; fi
  shift
done
mkdir -p "$out/Author"
printf 'book' > "$out/Author/Title.m4b"
printf '\033[2m2026-10-24T22:06:07.897356Z\033[0m \033[32mINFO\033[0m \033[2mm4b_merge: merged\033[0m\n'
printf '\033[33mWARN\033[0m \033[2mcover missing\033[0m\n' >&2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	res, err := proc.Run(context.Background(), "ansi1", []string{"/tmp/x/a.m4b"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	const wantStdout = "2026-10-24T22:06:07.897356Z INFO m4b_merge: merged\n"
	if res.Stdout != wantStdout {
		t.Errorf("Stdout = %q, want %q", res.Stdout, wantStdout)
	}
	const wantStderr = "WARN cover missing\n"
	if res.Stderr != wantStderr {
		t.Errorf("Stderr = %q, want %q", res.Stderr, wantStderr)
	}

	failBin := filepath.Join(dir, "ansi-fail-m4b")
	failScript := "#!/bin/sh\nprintf '\\033[31mmerge failed\\033[0m\\n' >&2\nexit 1\n"
	if err := os.WriteFile(failBin, []byte(failScript), 0o755); err != nil {
		t.Fatalf("write failing fake binary: %v", err)
	}
	proc = NewProcessor(failBin, "", "", 1, "{title}", "info")
	_, err = proc.Run(context.Background(), "", []string{"/tmp/x/a.m4b"}, "")
	if err == nil {
		t.Fatal("Run: succeeded, want a merge failure")
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("error still carries escape sequences: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "merge failed") {
		t.Errorf("error = %q, want the child's stderr text preserved", err.Error())
	}
}
