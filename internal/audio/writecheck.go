package audio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteAccessError reports a directory the running server cannot create
// entries in. Path is the configured directory (not an ancestor), so a 400
// or job error names the setting the operator has to fix.
type WriteAccessError struct {
	Path string
	Err  error
}

func (e *WriteAccessError) Error() string {
	return fmt.Sprintf("%q is not writable: %v", e.Path, e.Err)
}

func (e *WriteAccessError) Unwrap() error { return e.Err }

// CheckDirWritable verifies the server can create entries under dir. It
// walks up to the nearest existing ancestor and creates (then removes) a
// temporary file there, so a missing directory whose parent is writable is
// accepted — destinations and completed directories are created at job time
// with os.MkdirAll — while a missing directory under an unwritable parent
// (the reported misconfiguration: a bind mount the container user cannot
// write) is rejected before any job exists. An empty dir is a no-op: the
// optional completed directory means "unset".
//
// The probe performs a real create rather than an access(2)-style bit check:
// on network filesystems (SMB, CephFS) bit checks disagree with the eventual
// write, and the failure this guards against is a create.
func CheckDirWritable(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return &WriteAccessError{Path: dir, Err: err}
	}

	// Nearest existing ancestor: os.MkdirAll would create the missing tail,
	// so an existing writable parent is sufficient. A non-directory in the
	// chain can never be created through.
	probe := abs
	for {
		info, statErr := os.Stat(probe)
		if statErr == nil {
			if !info.IsDir() {
				return &WriteAccessError{Path: abs, Err: fmt.Errorf("%s is not a directory", probe)}
			}
			break
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return &WriteAccessError{Path: abs, Err: fsCause(statErr)}
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return &WriteAccessError{Path: abs, Err: fsCause(statErr)}
		}
		probe = parent
	}

	f, err := os.CreateTemp(probe, ".bragibooks-write-probe-*")
	if err != nil {
		return &WriteAccessError{Path: abs, Err: fsCause(err)}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// fsCause strips the verb+path frame from a *fs.PathError so the message
// names the configured directory once (`"/out" is not writable: permission
// denied`) instead of also naming the ancestor and the probe file.
func fsCause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
