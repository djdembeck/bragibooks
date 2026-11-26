package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindProbeDecision pins the fd-handoff decision: with no
// descriptor there is nothing to hand over (probe by name); with a
// descriptor and /dev/fd available the child is handed the descriptor
// (fd 3) and probes /dev/fd/3; with /dev/fd unavailable the verified
// pathname is probed by name (the documented fallback). The full
// ffprobe behavior of /dev/fd/3 is darwin-only; this test pins the
// selection logic, which runs on every platform.
func TestBindProbeDecision(t *testing.T) {
	tmp := t.TempDir()
	name := filepath.Join(tmp, "book.m4b")
	if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No descriptor: probe by name, nothing handed over.
	if p, f := bindProbe(name, nil); p != name || f != nil {
		t.Fatalf("bindProbe(name, nil) = (%q, %v); want (name, nil)", p, f)
	}

	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	old := devFDAvailable
	t.Cleanup(func() { devFDAvailable = old })

	// Descriptor present, /dev/fd available: the child probes
	// /dev/fd/3 and receives the descriptor via cmd.ExtraFiles.
	devFDAvailable = func() bool { return true }
	if p, ef := bindProbe(name, f); p != "/dev/fd/3" || ef != f {
		t.Fatalf("bindProbe with /dev/fd = (%q, %v); want (/dev/fd/3, the descriptor)", p, ef)
	}

	// Descriptor present, /dev/fd unavailable: fall back to the verified
	// pathname with nothing handed over.
	devFDAvailable = func() bool { return false }
	if p, ef := bindProbe(name, f); p != name || ef != nil {
		t.Fatalf("bindProbe without /dev/fd = (%q, %v); want (name, nil)", p, ef)
	}
}
