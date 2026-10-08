package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckSemver(t *testing.T) {
	tests := []struct {
		got, want string
		fail      bool
	}{
		{"1.0.0", "1.0.0", false},
		{"1.0.1", "1.0.0", false},
		{"1.1.0", "1.0.0", false},
		{"2.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", true},
		{"0.1.0", "1.0.0", true},
		{"1.0.0", "1.0.1", true},
		{"0.0.1", "1.0.0", true},
		{"2.0.0", "1.9.9", false},
		{"1.1.0", "1.0.9", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s vs %s", tt.got, tt.want), func(t *testing.T) {
			err := checkSemver(tt.got, tt.want)
			if tt.fail && err == nil {
				t.Errorf("expected error for %s < %s, got nil", tt.got, tt.want)
			}
			if !tt.fail && err != nil {
				t.Errorf("expected nil for %s >= %s, got %v", tt.got, tt.want, err)
			}
		})
	}
}

func TestFindNewestM4B(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "Author", "Old.m4b")
	if err := os.MkdirAll(filepath.Dir(older), 0o755); err != nil {
		t.Fatal(err)
	}
	olderTime := time.Now().Add(-10 * time.Minute)
	if err := os.WriteFile(older, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(older, olderTime, olderTime); err != nil {
		t.Fatal(err)
	}
	// A pre-existing file from before the cutoff must be ignored.
	stale := filepath.Join(dir, "Stale.m4b")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(dir, "Author", "New.m4b")
	if err := os.WriteFile(newest, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	since := time.Now().Add(-5 * time.Minute)
	got, err := findNewestM4B(dir, since)
	if err != nil {
		t.Fatalf("findNewestM4B: %v", err)
	}
	want, _ := filepath.Abs(newest)
	if got != want {
		t.Errorf("findNewestM4B = %q, want %q", got, want)
	}

	// Nothing newer than a recent cutoff: empty, no error.
	got, err = findNewestM4B(dir, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("findNewestM4B (no new): %v", err)
	}
	if got != "" {
		t.Errorf("findNewestM4B (no new) = %q, want empty", got)
	}
}
