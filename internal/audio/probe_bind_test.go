package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindProbeDecision pins the fd-handoff decision on every
// platform: with no descriptor there is nothing to hand over (probe
// by name), and with a descriptor the outcome follows the platform's
// devFDAvailable predicate.
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

	p, ef := bindProbe(name, f)
	if devFDAvailable() {
		// darwin/BSD: the child probes /dev/fd/3 and receives the
		// descriptor via cmd.ExtraFiles.
		if p != "/dev/fd/3" || ef != f {
			t.Fatalf("bindProbe with descriptor = (%q, %v); want (/dev/fd/3, the descriptor)", p, ef)
		}
		return
	}
	// linux/windows: the probe path already names the descriptor, so
	// the path is returned as-is and nothing is handed over.
	if p != name || ef != nil {
		t.Fatalf("bindProbe with descriptor = (%q, %v); want (name, nil)", p, ef)
	}
}
