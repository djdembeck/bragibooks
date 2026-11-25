//go:build !windows && !linux

package api

import "os"

// proberPath has no procfs on this platform: the descriptor cannot be
// named by path, so it returns the ORIGINAL pathname only as a harmless
// payload (the (string, bool) contract) with ok FALSE — fail-closed,
// like the Windows refusal. An unpinned name must not be a successful
// result: the extractor reopens the NAME for the ffprobe probes and
// folder discovery, so a pathname swapped after the open-time
// containment check would let ffprobe read outside the root. The
// callers (extractOne, pinPart) take the refusal path and degrade the
// row/part. The walk-time containment check plus the open-time check
// (O_NOFOLLOW of the verified target with the descriptor's own type
// fstat'd) remain in force on every platform; without procfs they
// cannot be upgraded to a descriptor binding, so the probe is refused
// rather than handed an un-pinned name.
func proberPath(f *os.File, original string) (string, bool) { return original, false }
