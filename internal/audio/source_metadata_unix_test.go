//go:build !windows

package audio

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestExtractSourceMetadataOverLimitProbeIsKilledAndReaped pins the reap
// fix: when the probe output exceeds the 1 MiB cap the child is killed
// AND reaped, so after the extraction returns nothing is left alive (or
// as a zombie) from the probe.
func TestExtractSourceMetadataOverLimitProbeIsKilledAndReaped(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "probe.pid")
	bin := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
echo $$ > ` + pidFile + `
head -c 1100000 /dev/zero | tr '\0' 'x'
sleep 300
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ffprobe: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "1.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	snap := runExtraction(t, bin, srcDir)
	if len(snap.Files) != 1 || !strings.Contains(snap.Files[0].Error, "probe output exceeds 1 MiB") {
		t.Fatalf("files = %+v, want the capped probe errored", snap.Files)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read probe pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse probe pid: %v", err)
	}
	// Bounded settle loop, not a sleep: kill -0 also reports zombies, so
	// a live process here means the reap (kill + wait) did not happen.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe pid %d still alive after extraction returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
