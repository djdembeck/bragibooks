//go:build !windows && !linux && !(darwin || freebsd || solaris || illumos || aix)

package api

// openRootSearchMode is the access mode for openRootAnchoredAttempt's
// INTERMEDIATE component opens. This platform has no distinct execute-
// only open flag in x/sys/unix (O_EXEC is undefined here), so the mode
// is the open() default — O_RDONLY (0): an intermediate ancestor
// RESIDUALLY needs read (r) as well as search (x) permission, and no
// flag combination does better. See openRootAnchoredAttempt.
const openRootSearchMode = 0
