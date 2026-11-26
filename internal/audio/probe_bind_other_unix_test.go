//go:build !windows && !linux

package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindProbeRealProbe pins the real availability probe against the
// decision: whatever the host reports, bindProbe with a descriptor
// follows it. The hand-off is enabled when the probe passes; the
// verified pathname is probed by name when it fails.
func TestBindProbeRealProbe(t *testing.T) {
	tmp := t.TempDir()
	name := filepath.Join(tmp, "book.m4b")
	if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	real := probeDevFD()
	old := devFDAvailable
	t.Cleanup(func() { devFDAvailable = old })
	devFDAvailable = func() bool { return real }
	p, ef := bindProbe(name, f)
	if real {
		if p != "/dev/fd/3" || ef != f {
			t.Fatalf("bindProbe with real probe = (%q, %v); want (/dev/fd/3, the descriptor)", p, ef)
		}
	} else if p != name || ef != nil {
		t.Fatalf("bindProbe with real probe = (%q, %v); want (name, nil)", p, ef)
	}
}
