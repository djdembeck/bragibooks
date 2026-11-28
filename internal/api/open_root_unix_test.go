//go:build linux

package api

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestOpenInsideRootFileRowRefusesDirectoryTarget pins the open-time
// directory-target refusal for file rows: an in-root symlink to a
// directory is a SYMLINK to Lstat (so the walk keeps it as a file row),
// and the open must refuse the DIRECTORY TARGET before the extractor
// can enter folder discovery from a file row.
func TestOpenInsideRootFileRowRefusesDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "subdir")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "dirlink")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if f, err := openInsideRoot(link, root, false); err == nil {
		f.Close()
		t.Fatal("file row opened a directory target; want refusal")
	} else if !containsAll(err.Error(), "not a regular file") {
		t.Fatalf("refusal = %q, want the not-a-regular-file diagnosis", err)
	}
	// The same target is fine for a directory row (allowDir).
	if f, err := openInsideRoot(link, root, true); err != nil {
		t.Fatalf("allowDir open of %s: %v", link, err)
	} else {
		f.Close()
	}
}

// TestOpenInsideRootFIFOIsRefusedNonblocking pins the nonblocking open:
// a FIFO (a writerless special file a writer planted at the name) must
// be REFUSED by the descriptor's own type, not accepted and then left
// to block the scan worker on a read open no reader will satisfy. The
// O_NONBLOCK open succeeds on a FIFO, so the fstat of the opened
// descriptor is what degrades it.
func TestOpenInsideRootFIFOIsRefusedNonblocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	t.Cleanup(func() { os.Remove(fifo) })
	if f, err := openInsideRoot(fifo, root, false); err == nil {
		f.Close()
		t.Fatal("FIFO opened for a file row; want refusal")
	} else if !containsAll(err.Error(), "not a regular file") {
		t.Fatalf("refusal = %q, want the not-a-regular-file diagnosis", err)
	}
	// And the symlink-to-FIFO shape: the link is a file row, its target
	// is a FIFO.
	target := filepath.Join(root, "target-pipe")
	if err := syscall.Mkfifo(target, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	t.Cleanup(func() { os.Remove(target) })
	link := filepath.Join(root, "link-pipe")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if f, err := openInsideRoot(link, root, false); err == nil {
		f.Close()
		t.Fatal("symlink to FIFO opened for a file row; want refusal")
	}
}

// TestOpenInsideRootRegularFileStillOpens guards against the
// nonblocking/fstat changes rejecting what they must accept: an ordinary
// regular file (and a symlink to one inside the root) opens as before.
func TestOpenInsideRootRegularFileStillOpens(t *testing.T) {
	root := t.TempDir()
	reg := filepath.Join(root, "book.mp3")
	if err := os.WriteFile(reg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "booklink.mp3")
	if err := os.Symlink(reg, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if f, err := openInsideRoot(link, root, false); err != nil {
		t.Fatalf("in-root symlink to a regular file refused: %v", err)
	} else {
		f.Close()
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
