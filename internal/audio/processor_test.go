package audio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bragibooks/bragibooks/internal/config"
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

// writeVersionFake writes an executable shell script that reports a fixed
// m4b-merge version, for the UpdateVersion contract tests.
func writeVersionFake(t *testing.T, dir, version string) string {
	t.Helper()
	p := filepath.Join(dir, "fake-version")
	script := "#!/bin/sh\necho m4b-merge " + version + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake version binary: %v", err)
	}
	return p
}

func TestUpdateVersionContract(t *testing.T) {
	t.Parallel()

	t.Run("parses reported version", func(t *testing.T) {
		t.Parallel()
		bin := writeVersionFake(t, t.TempDir(), "2.4.1")
		proc := NewProcessor(bin, "", "", 1, "{author}/{title}", "info")

		got := proc.UpdateVersion()
		if got != "2.4.1" {
			t.Errorf("UpdateVersion = %q, want %q", got, "2.4.1")
		}
		if r := proc.ReadVersion(); r != "2.4.1" {
			t.Errorf("ReadVersion = %q, want %q", r, "2.4.1")
		}
	})

	t.Run("missing binary reports unknown", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		proc := NewProcessor(missing, "", "", 1, "{author}/{title}", "info")

		got := proc.UpdateVersion()
		if got != "unknown" {
			t.Errorf("UpdateVersion = %q, want %q", got, "unknown")
		}
		if r := proc.ReadVersion(); r != "unknown" {
			t.Errorf("ReadVersion = %q, want %q", r, "unknown")
		}
	})

	t.Run("failing binary reports unknown", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "fail-version")
		script := "#!/bin/sh\necho oops >&2\nexit 1\n"
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatalf("write failing binary: %v", err)
		}
		proc := NewProcessor(p, "", "", 1, "{author}/{title}", "info")

		if got := proc.UpdateVersion(); got != "unknown" {
			t.Errorf("UpdateVersion = %q, want %q", got, "unknown")
		}
	})

	t.Run("refresh after binary change", func(t *testing.T) {
		t.Parallel()
		oldBin := writeVersionFake(t, t.TempDir(), "1.0.0")
		newBin := writeVersionFake(t, t.TempDir(), "2.0.0")

		proc := NewProcessor(oldBin, "", "", 1, "{author}/{title}", "info")
		cfg := &config.Config{}
		cfg.M4bMerge.Binary = oldBin
		proc.ApplyConfig(cfg)
		if got := proc.UpdateVersion(); got != "1.0.0" {
			t.Fatalf("UpdateVersion (old binary) = %q, want %q", got, "1.0.0")
		}

		// Reconfigure to the new binary, then refresh through the same
		// path the settings handler uses: ApplyConfig, UpdateVersion.
		cfg = &config.Config{}
		cfg.M4bMerge.Binary = newBin
		proc.ApplyConfig(cfg)
		got := proc.UpdateVersion()
		if got != "2.0.0" {
			t.Errorf("UpdateVersion (new binary) = %q, want %q", got, "2.0.0")
		}
		if r := proc.ReadVersion(); r != "2.0.0" {
			t.Errorf("ReadVersion after refresh = %q, want %q", r, "2.0.0")
		}
	})

	t.Run("recovers transient text-file busy on probe", func(t *testing.T) {
		t.Parallel()
		// Linux refuses to exec a file while ANY fd is open for writing
		// it, process-wide (golang/go#22315): in a multithreaded process
		// a sibling fork's inherited writer fd can outlive the writer's
		// own close and tax a freshly written binary with ETXTBSY. The
		// writer fd held here while the first probe attempt fires stands
		// in for that leaked fd; it is dropped midway through the probe's
		// retry window (3 attempts over ~150ms), so the first attempt
		// hits ETXTBSY and a later attempt must succeed.
		p := filepath.Join(t.TempDir(), "etxtbusy-version")
		script := "#!/bin/sh\necho m4b-merge 4.5.6\n"
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake version binary: %v", err)
		}
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open binary for writing: %v", err)
		}
		defer f.Close()
		// Drop the writer soon after the first attempt, well inside the
		// retry budget (first retry lands ~50ms later; last at ~150ms).
		go func() {
			time.Sleep(20 * time.Millisecond)
			f.Close()
		}()
		proc := NewProcessor(p, "", "", 1, "{author}/{title}", "info")
		got := proc.UpdateVersion()
		if got != "4.5.6" {
			t.Errorf("UpdateVersion = %q, want %q (transient ETXTBSY must be rescued by the probe retry)", got, "4.5.6")
		}
	})

	t.Run("storeVersion records value", func(t *testing.T) {
		t.Parallel()
		proc := NewProcessor("", "", "", 1, "{author}/{title}", "info")
		proc.StoreVersion("9.9.9")
		if r := proc.ReadVersion(); r != "9.9.9" {
			t.Errorf("ReadVersion after StoreVersion = %q, want %q", r, "9.9.9")
		}
	})
}

// writeWrapperVersionFake writes an executable shell script that spawns a
// background sleeper (which inherits stdout/stderr and outlives the
// wrapper by sleepSeconds), records the sleeper's PID in pidFile, and
// then waits wrapperSeconds before exiting — mimicking the wrapper
// binaries that can be configured via the settings API. wrapperSeconds
// must exceed versionProbeTimeout: os/exec fires Cmd.Cancel when the
// context deadline fires, not when the direct child exits, so the
// wrapper must still be alive at the deadline for the group kill to be
// exercised (a dead wrapper leaves a dead process group, and the piped
// grandchild only outlives the probe when a WaitDelay overrun kicks in).
func writeWrapperVersionFake(t *testing.T, dir, pidFile, sleepSeconds, wrapperSeconds string) string {
	t.Helper()
	p := filepath.Join(dir, "wrapper-version")
	script := "#!/bin/sh\nsleep " + sleepSeconds + " & echo $! > \"" +
		pidFile + "\"\nsleep " + wrapperSeconds + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write wrapper version binary: %v", err)
	}
	return p
}

// TestUpdateVersionKillsWrapperProcessGroup covers the process-group fix:
// the probe's context deadline fires with the wrapper — and its sleeper
// grandchild holding the stdout pipe — still alive. The cancel must
// SIGKILL the whole process group, and the probe must return bounded by
// versionProbeTimeout (+WaitDelay), not the sleeper's 30s lifetime, with
// no surviving test processes.
func TestUpdateVersionKillsWrapperProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("probe cancel uses POSIX process groups via Setpgid; runtime is %s", runtime.GOOS)
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleeper.pid")
	bin := writeWrapperVersionFake(t, dir, pidFile, "30", "10")
	proc := NewProcessor(bin, "", "", 1, "{author}/{title}", "info")

	// Leak insurance: a failed or aborted run must not leave the 30s
	// sleeper behind. Signal-0 probe first, so a dead/already-reaped
	// PID is a no-op and cleanup never blocks.
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return // sleeper PID unknown/unrecorded
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			return
		}
		if p, _ := os.FindProcess(pid); p != nil && p.Signal(syscall.Signal(0)) == nil {
			p.Kill()
		}
	})

	start := time.Now()
	if got := proc.UpdateVersion(); got != "unknown" {
		t.Errorf("UpdateVersion = %q, want %q (killed wrapper produces no version)", got, "unknown")
	}
	elapsed := time.Since(start)

	// Bounded by versionProbeTimeout (+WaitDelay), well under the
	// sleeper's 30s lifetime.
	if elapsed > 10*time.Second {
		t.Fatalf("UpdateVersion took %v; probe stalled past versionProbeTimeout + WaitDelay", elapsed)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("sleeper pid file missing: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad sleeper pid %q: %v", raw, err)
	}

	// Bounded check: poll briefly for the SIGKILL to land, treating a
	// missing /proc/<pid> (reaped) or a Z (zombie, awaiting reaper)
	// state as terminated. Do not rely on PID 1 promptly reaping the
	// orphaned sleeper — a zombie is already dead.
	if !processTerminated(pid, 2*time.Second) {
		t.Errorf("process leak: wrapper sleeper %d survived the probe's process-group kill", pid)
	}
}

// processTerminated polls /proc/<pid>/stat until the process is gone
// (no entry) or a zombie (state Z), up to timeout.
func processTerminated(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		state, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err == nil {
			// stat: pid (comm) state ... — comm can contain
			// spaces/parens, so state is the field after the last ')'.
			if i := strings.LastIndex(string(state), ") "); i != -1 {
				if strings.TrimSpace(string(state[i+2:])) == "Z" {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
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
