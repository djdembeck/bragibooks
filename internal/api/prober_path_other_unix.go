//go:build !windows && !linux

package api

import "os"

// proberPath has no procfs on this platform: the descriptor cannot be
// named by path, so the probe receives the ORIGINAL pathname. The
// pinned-descriptor security property is BEST-EFFORT here — the
// binding is the walk-time containment check plus the open-time check
// (O_NOFOLLOW of the verified target with the descriptor's own type
// fstat'd), which remain in force on every platform. The caller must
// keep f open for the probe's duration.
func proberPath(f *os.File, original string) string { return original }
