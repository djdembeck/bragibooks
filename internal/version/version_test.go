package version

import "testing"

// TestVersionDefaultIsDev pins the unstamped-build fallback: a normal
// `go test` (no -ldflags) build must report "dev", not a released version.
// The stamped path is exercised via -X at link time (see Makefile/CI), so
// the observable contract in a regular build is the default value.
func TestVersionDefaultIsDev(t *testing.T) {
	got := Version
	want := "dev"
	if got != want {
		t.Errorf("Version = %q in unstamped build, want %q", got, want)
	}
}
