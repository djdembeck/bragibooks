//go:build !windows && !linux

package api

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestProberPathOtherUnixIdentityCheck pins the identity contract:
// proberPath returns the original pathname with ok=true only while it
// names the same filesystem object (device + inode, via os.SameFile) as
// the open descriptor, and ok=false the moment that is no longer true.
// The test does not run on Linux CI (Linux resolves the descriptor
// through /proc) — it protects the macOS/BSD build.
func TestProberPathOtherUnixIdentityCheck(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "media", "book.m4b")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Unchanged: the pathname and the descriptor are the same object.
	got, ok := proberPath(f, p)
	if !ok {
		t.Fatalf("proberPath ok = false for the unchanged pathname; want true")
	}
	if got != p {
		t.Fatalf("proberPath = %q; want the original pathname %q", got, p)
	}

	// Replaced with a different-inode file: the pathname no longer names
	// the opened object, so the name is refused.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := proberPath(f, p); ok {
		t.Fatalf("proberPath ok = true after the pathname was replaced by a different-inode file; want false")
	}
	f.Close()

	// Missing pathname: the pathname stat fails, so the name is refused.
	f2, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := proberPath(f2, p); ok {
		t.Fatalf("proberPath ok = true for a missing pathname; want false")
	}

	// A hardlink of the SAME inode keeps the name bound to the verified
	// object: the pathname resolves to the inode the open bound.
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f3, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	link := filepath.Join(dir, "media", "book-link.m4b")
	if err := syscall.Link(p, link); err != nil {
		t.Skipf("hardlink unsupported: %v", err)
	}
	if got, ok := proberPath(f3, link); !ok {
		t.Fatalf("proberPath ok = false for a hardlink of the same inode; want true")
	} else if got != link {
		t.Fatalf("proberPath = %q; want the hardlink pathname %q", got, link)
	}

	// Pathname replaced by a symlink to a regular file elsewhere: lstat
	// sees the symlink, so the name is refused even when the target
	// happens to share an inode with the opened file.
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "book.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f4, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f4.Close()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "book.m4b"), p); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, ok := proberPath(f4, p); ok {
		t.Fatalf("proberPath ok = true after the pathname was replaced by a symlink; want false")
	}

	// Parent directory replaced by a symlink to a directory containing a
	// same-named file: the pathname resolves through the symlinked parent
	// to a different-inode file, so the name is refused.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f5, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f5.Close()
	if err := os.Rename(filepath.Dir(p), filepath.Join(dir, "media-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Dir(p)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, ok := proberPath(f5, p); ok {
		t.Fatalf("proberPath ok = true after the parent directory was replaced by a symlink to an outside directory; want false")
	}
}
