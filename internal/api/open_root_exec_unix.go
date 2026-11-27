//go:build !windows && !linux && (darwin || freebsd || solaris || illumos || aix)

package api

import "golang.org/x/sys/unix"

// openRootSearchMode is the access mode for openRootAnchoredAttempt's
// INTERMEDIATE component opens: the execute-only flag, so traversing an
// ancestor needs only search (x) permission, not read (r). x/sys/unix
// defines O_EXEC on exactly these non-Linux Unix targets; the platforms
// it does not (openbsd, netbsd, dragonfly, zos, wasm) fall back to the
// open() default in open_root_exec_other_unix.go.
const openRootSearchMode = unix.O_EXEC
