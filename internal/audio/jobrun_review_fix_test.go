package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidatePublishedArtifactRejectsSymlinkedIntermediateDirectory guards
// the B2 fix: a symlinked directory component that resolves outside the root
// must be rejected even though the final file is a regular file under its
// resolved location.
func TestValidatePublishedArtifactRejectsSymlinkedIntermediateDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outFile := filepath.Join(outside, "book.m4b")
	if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// root/alias -> outside; the artifact claims root/alias/book.m4b.
	if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	_, err := validatePublishedArtifact(mArtifact{Kind: "audio", RelativePath: "alias/book.m4b"}, root)
	if err == nil {
		t.Fatal("symlinked intermediate directory escaping the root was accepted")
	}
	if !strings.Contains(err.Error(), "escapes output root") {
		t.Fatalf("error %q, want escape diagnosis", err.Error())
	}
}

// TestValidatePublishedArtifactRejectsFinalSymlinkEscape covers the final
// component: a file-level symlink pointing outside the root.
func TestValidatePublishedArtifactRejectsFinalSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outFile := filepath.Join(outside, "book.m4b")
	if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outFile, filepath.Join(root, "final.m4b")); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePublishedArtifact(mArtifact{Kind: "audio", RelativePath: "final.m4b"}, root); err == nil {
		t.Fatal("final-file symlink escaping the root was accepted")
	}
}

// TestValidatePublishedArtifactAcceptsInnerSymlink: a symlink whose target is
// INSIDE the root is not an escape; containment is the contract.
func TestValidatePublishedArtifactAcceptsInnerSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "sub", "book.m4b")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/book.m4b", filepath.Join(root, "link.m4b")); err != nil {
		t.Fatal(err)
	}
	final, err := validatePublishedArtifact(mArtifact{Kind: "audio", RelativePath: "link.m4b"}, root)
	if err != nil {
		t.Fatalf("inner symlink rejected: %v", err)
	}
	if final != filepath.Join(root, "link.m4b") {
		t.Fatalf("final = %q, want the claimed path", final)
	}
}

// TestRemoveStagingSiblingsScopedToEnginePID guards the B3 fix: cleanup
// removes only this run's staging (matched by engine pid), never a
// concurrent run's in-flight staging in the same root.
func TestRemoveStagingSiblingsScopedToEnginePID(t *testing.T) {
	root := t.TempDir()
	mine := filepath.Join(root, ".m4b-merge-staging-4242-1-1.m4b")
	theirs := filepath.Join(root, ".m4b-merge-staging-9999-1-1.m4b")
	userFile := filepath.Join(root, "book.m4b")
	for _, f := range []string{mine, theirs, userFile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeStagingSiblings(root, 4242); err != nil {
		t.Fatalf("removeStagingSiblings: %v", err)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatal("this run's staging sibling was not removed")
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Fatal("a concurrent run's staging sibling was removed")
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatal("a user output file was removed")
	}
}
