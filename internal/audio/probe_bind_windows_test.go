//go:build windows

package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindProbeWindowsNoHandoff pins the windows half of the gated
// contract: devFDAvailable is false, so the probe path (the handle's
// NT device path) already names the descriptor and the child probes
// it directly. bindProbe returns the path as-is and never hands a
// descriptor over.
func TestBindProbeWindowsNoHandoff(t *testing.T) {
	if devFDAvailable() {
		t.Fatal("devFDAvailable = true on windows; want false")
	}
	tmp := t.TempDir()
	name := filepath.Join(tmp, "book.m4b")
	if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if p, f := bindProbe(name, nil); p != name || f != nil {
		t.Fatalf("bindProbe(name, nil) = (%q, %v); want (name, nil)", p, f)
	}

	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if p, ef := bindProbe(name, f); p != name || ef != nil {
		t.Fatalf("bindProbe with descriptor = (%q, %v); want (name, nil)", p, ef)
	}
}
