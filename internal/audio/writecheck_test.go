package audio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfRoot: root bypasses the permission bits the probe exercises, so a
// 0555 directory is still writable to it.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits")
	}
}

// readOnlyDir creates a directory without write/search permission and
// restores it for cleanup.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	return dir
}

func TestCheckDirWritable(t *testing.T) {
	skipIfRoot(t)

	parent := readOnlyDir(t)
	writable := t.TempDir()
	file := filepath.Join(writable, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cases := []struct {
		name    string
		dir     string
		wantErr []string // empty means writable
		// probe must not create the directory: creating a destination is
		// the job's side effect, not the check's.
		mustStayMissing bool
	}{
		{name: "empty is unset"},
		{name: "existing writable dir", dir: writable},
		{name: "missing dir under writable parent", dir: filepath.Join(writable, "new", "nested"), mustStayMissing: true},
		{name: "existing read-only dir", dir: parent, wantErr: []string{parent + `" is not writable`, "permission denied"}},
		{name: "missing dir under read-only parent", dir: filepath.Join(parent, "completed"), wantErr: []string{filepath.Join(parent, "completed") + `" is not writable`, "permission denied"}, mustStayMissing: true},
		{name: "regular file as dir", dir: file, wantErr: []string{file + `" is not writable`, file + " is not a directory"}},
		{name: "regular file in chain", dir: filepath.Join(file, "sub"), wantErr: []string{"is not writable", "not a directory"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDirWritable(tc.dir)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("CheckDirWritable(%q) = %v; want nil", tc.dir, err)
				}
			} else {
				if err == nil {
					t.Fatalf("CheckDirWritable(%q) = nil; want error", tc.dir)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error = %q; want it to contain %q", err, want)
					}
				}
				var wa *WriteAccessError
				if !errors.As(err, &wa) {
					t.Fatalf("error %v is not a *WriteAccessError", err)
				}
				if wa.Path == "" {
					t.Error("WriteAccessError.Path is empty")
				}
			}
			if tc.mustStayMissing && tc.dir != "" {
				if _, statErr := os.Stat(tc.dir); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("probe created %q (stat err = %v)", tc.dir, statErr)
				}
			}
		})
	}
}

// TestCheckDirWritableLeavesReadOnlyDirIntact pins that the probe cleans up
// after itself in the ancestor it created a temp entry in.
func TestCheckDirWritableLeavesReadOnlyDirIntact(t *testing.T) {
	skipIfRoot(t)

	writable := t.TempDir()
	if err := CheckDirWritable(filepath.Join(writable, "completed")); err != nil {
		t.Fatalf("CheckDirWritable = %v; want nil", err)
	}
	entries, err := os.ReadDir(writable)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("writable parent has %d entries after probe; want 0", len(entries))
	}
}
