package audio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeDrainHolderJobBinary writes a job-protocol engine that writes its
// machine document, spawns a background descendant that inherits the
// named pipe's write side (holderPipe: "stdout" or "stderr"), and exits
// WITHOUT killing that descendant. The holder keeps the pipe's write
// side open after the direct child exits: an unbounded drain would stall
// the run on the inherited descriptor. The other pipe is released
// (DEVNULL), so the drain under test is the named pipe's reader, not the
// other's.
func writeDrainHolderJobBinary(t *testing.T, dir string, newSession bool, holderPipe string) string {
	t.Helper()
	p := filepath.Join(dir, "drain-holder-bin")
	// The holder inherits ONLY the pipe under test; the other is DEVNULL
	// so its reader drains immediately and cannot mask a stall on the
	// pipe being measured.
	otherDev := "stderr=subprocess.DEVNULL"
	if holderPipe == "stderr" {
		otherDev = "stdout=subprocess.DEVNULL"
	}
	if newSession {
		// Its own session: the wrapper's process-group kill cannot reach
		// the holder, so only the drain-timeout read-side close can
		// release the reader.
		otherDev += ", start_new_session=True"
	}
	script := `#!/usr/bin/env python3
import json, os, subprocess, sys

if "--version" in sys.argv:
    print("m4b-merge 2.2.0")
    sys.exit(0)
if "--capabilities" in sys.argv:
    print(json.dumps({"schema_version": 1, "resolver_version": "1",
        "default_path_format": "{author}/{title}",
        "path_tokens": [{"name": "title", "value": "t", "example": "T"}],
        "field_policies": {"values": ["write", "preserve"],
            "scalar_fields": ["title"]},
        "cover_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]},
        "chapter_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]}}))
    sys.exit(0)
job = None
for i, a in enumerate(sys.argv):
    if a == "--job" and i + 1 < len(sys.argv):
        job = sys.argv[i + 1]
with open(job) as f:
    jf = json.load(f)
root = jf["output_root"]
rel = "Author/Title.m4b"
os.makedirs(os.path.join(root, "Author"), exist_ok=True)
with open(os.path.join(root, rel), "w") as f:
    f.write("book")
# Background holder of the inherited pipe under test: it survives the
# wrapper's exit (the wrapper does not kill its group), keeping that
# pipe's write side open until the bounded drain kills the process group
# (or, for a session-escaped holder, the drain-timeout read-side close).
# The other pipe is released (DEVNULL) so the drain under test is the
# Bragi-owned reader of the named pipe and its bounded drain (no
# os/exec copier is involved: both readers are Bragi's own goroutines).
subprocess.Popen(["sleep", "60"], __HOLDER_DEV__)
doc = {
    "schema_version": 1,
    "resolver_version": "1",
    "status": "succeeded",
    "groups": [{
        "group": "main",
        "outcome": "succeeded",
        "artifacts": [{"relative_path": rel, "kind": "audio", "exists": True}],
        "files_moved": True
    }]
}
print(json.dumps(doc))
sys.exit(0)
`
	script = strings.Replace(script, "__HOLDER_DEV__", otherDev, 1)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write drain-holder binary: %v", err)
	}
	return p
}

// TestRunJobBoundedDrainDoesNotStallOnInheritedStdout pins the bounded
// drain: a run whose direct child exits while a background descendant
// still holds the inherited stdout descriptor must return within the
// drain bound plus margin (the group kill releases the holder), not
// stall the worker on the inherited fd.
func TestRunJobBoundedDrainDoesNotStallOnInheritedStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group drain bound is a unix concept")
	}
	dir := t.TempDir()
	bin := writeDrainHolderJobBinary(t, dir, false, "stdout")
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	start := time.Now()
	res, err := proc.RunJob(context.Background(), "drainholder", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.PrimaryOutput == "" {
		t.Fatal("RunJob succeeded but enumerated no audio artifact")
	}
	// drainTimeout (2s) plus a generous scheduling margin: an unbounded
	// drain would sit here until the holder's sleep 60 expires.
	if elapsed > drainTimeout+13*time.Second {
		t.Fatalf("RunJob took %v; the drain past the direct child's exit is not bounded (want %v + margin)",
			elapsed, drainTimeout)
	}
}

// TestRunJobBoundedDrainBoundsSessionEscapedStdoutHolder pins the reader
// release for a holder the group kill CANNOT reach: a descendant that
// starts its own session inherits the stdout write side but survives the
// drain-timeout group SIGKILL. The drain must still end in the bound —
// the read-side close is what errors out the reader's copy here, since
// the write side stays open until the holder's sleep expires.
func TestRunJobBoundedDrainBoundsSessionEscapedStdoutHolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group drain bound is a unix concept")
	}
	dir := t.TempDir()
	bin := writeDrainHolderJobBinary(t, dir, true, "stdout")
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	start := time.Now()
	res, err := proc.RunJob(context.Background(), "drainsession", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.PrimaryOutput == "" {
		t.Fatal("RunJob succeeded but enumerated no audio artifact")
	}
	// The holder outlives the group kill: without the read-side close the
	// drain would sit here until the holder's sleep 60 expires.
	if elapsed > drainTimeout+13*time.Second {
		t.Fatalf("RunJob took %v; the session-escaped stdout holder is not released at the drain bound (want %v + margin)",
			elapsed, drainTimeout)
	}
}

// TestRunJobBoundedDrainBoundsInheritedStderrHolder pins the bounded drain
// on the stderr side: a run whose direct child exits while a background
// descendant still holds the inherited stderr descriptor must return
// within the drain bound plus margin (the group kill releases the holder),
// not stall the worker on the inherited fd. The stdout-holder cases exercise
// the stdout reader only; a regression in the stderr reader's timeout-close
// path would pass with this case missing.
func TestRunJobBoundedDrainBoundsInheritedStderrHolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group drain bound is a unix concept")
	}
	dir := t.TempDir()
	bin := writeDrainHolderJobBinary(t, dir, false, "stderr")
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	start := time.Now()
	res, err := proc.RunJob(context.Background(), "drainholderstderr", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.PrimaryOutput == "" {
		t.Fatal("RunJob succeeded but enumerated no audio artifact")
	}
	// drainTimeout (2s) plus a generous scheduling margin: an unbounded
	// drain would sit here until the holder's sleep 60 expires.
	if elapsed > drainTimeout+13*time.Second {
		t.Fatalf("RunJob took %v; the inherited stderr holder is not released at the drain bound (want %v + margin)",
			elapsed, drainTimeout)
	}
}
