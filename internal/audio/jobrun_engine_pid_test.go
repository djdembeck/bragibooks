package audio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// writeForeignPIDJobBinary writes a job-protocol engine that reports a
// CONFIGURED pid in its machine document (BRAGI_TEST_ENGINE_PID) and plants
// a staging sibling under that pid, modeling an engine whose document
// claims a process the ownership check must resolve.
func writeForeignPIDJobBinary(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "foreign-pid-bin")
	script := `#!/usr/bin/env python3
import json, os, sys

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
# Plant a staging sibling under the pid the document will report: if
# cleanup trusts the reported pid, THIS file (created by the run, so
# absent from the pre-run baseline) is what it removes.
foreign = os.environ.get("BRAGI_TEST_ENGINE_PID")
if foreign:
    with open(os.path.join(root, ".m4b-merge-staging-" + foreign + "-3-3.m4b"), "w") as f:
        f.write("staging")
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
if foreign:
    doc["pid"] = int(foreign)
print(json.dumps(doc))
sys.exit(0)
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write foreign-pid binary: %v", err)
	}
	return p
}

// spawnForeignPIDHelper starts a long-lived sibling process (a direct child
// of the test process, NOT of the run's wrapper) and waits until it is
// live. Its pid is a true "unrelated PID" for verifyEnginePID: the
// /proc ppid chain from it reaches init without passing through the
// wrapper, so the verification must reject it.
func spawnForeignPIDHelper(t *testing.T, dir string) int {
	t.Helper()
	p := filepath.Join(dir, "helper-bin")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write helper binary: %v", err)
	}
	helper := exec.Command(p)
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	// Wait for the pid to be live (a dead pid would fall back to the
	// wrapper too, masking a regression).
	for range 50 {
		if _, err := os.Stat("/proc/" + strconv.Itoa(helper.Process.Pid)); err == nil {
			return helper.Process.Pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper pid %d never appeared in /proc", helper.Process.Pid)
	return 0
}

// TestRunJobUnverifiableEnginePIDsFallBackToWrapper guards the
// ownership-verification fallback in RunJob: a machine document that
// reports a pid that is neither the spawned child nor a verified
// descendant of it must not authorize staging cleanup — cleanup must run
// under the wrapper's pid, leaving the foreign pid's staging siblings
// untouched. The foreign pid is a live helper process spawned by the test
// itself: it is a SIBLING of the run's wrapper (both are children of the
// test process), so the /proc ppid chain provably never reaches the
// wrapper.
func TestRunJobUnverifiableEnginePIDsFallBackToWrapper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pids are only ownership-verified via /proc on linux")
	}
	dir := t.TempDir()
	bin := writeForeignPIDJobBinary(t, dir)
	foreign := spawnForeignPIDHelper(t, dir)
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()
	t.Setenv("BRAGI_TEST_ENGINE_PID", strconv.Itoa(foreign))

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	res, err := proc.RunJob(context.Background(), "foreignpid", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.PrimaryOutput == "" {
		t.Fatal("RunJob succeeded but enumerated no audio artifact")
	}

	// The engine planted a staging sibling under the REPORTED (foreign)
	// pid. A cleanup that trusted the report would have removed it; the
	// wrapper-pid fallback must leave it untouched.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read output root: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == ".m4b-merge-staging-"+strconv.Itoa(foreign)+"-3-3.m4b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("output root = %v; want the foreign-pid staging sibling preserved (cleanup trusted a pid it did not own)", entries)
	}
}
