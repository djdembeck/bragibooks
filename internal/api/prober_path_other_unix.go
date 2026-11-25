//go:build !windows && !linux

package api

import "os"

// proberPath has no procfs on this platform: the descriptor cannot be
// named by path, so the probe receives the ORIGINAL pathname. The
// pinned-descriptor security property is BEST-EFFORT here — the
// binding is the walk-time containment check plus the open-time check
// (O_NOFOLLOW of the verified target with the descriptor's own type
// fstat'd), which remain in force on every platform. The best-effort
// contract: the caller keeps f open for the probe's duration, and the
// open-time check (openInsideRoot) already proved the open stayed inside
// the root, so the original pathname is the only name the probe can be
// handed — unlike Windows, where an un-approved pathname is a refusal,
// not a fallback. ok is true: with no procfs there is no second lookup
// that can fail.
func proberPath(f *os.File, original string) (string, bool) { return original, true }
