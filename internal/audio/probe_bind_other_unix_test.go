//go:build !windows && !linux

package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBindProbeOtherUnix pins the darwin/BSD half of the gated
// contract: devFDAvailable is true, so a verified descriptor is
// ALWAYS probed through /dev/fd/3 (the child is handed fd 3 and reads
// the inherited descriptor) and a nil descriptor probes by name. If
// the child cannot resolve /dev/fd/3 (no fdescfs), ffprobe fails to
// open it and the probe degrades the row; the verified descriptor
// never falls back to the mutable pathname.
func TestBindProbeOtherUnix(t *testing.T) {
	if !devFDAvailable() {
		t.Fatal("devFDAvailable = false on darwin/BSD; want true")
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

	if p, ef := bindProbe(name, f); p != "/dev/fd/3" || ef != f {
		t.Fatalf("bindProbe with descriptor = (%q, %v); want (/dev/fd/3, the descriptor)", p, ef)
	}
}
