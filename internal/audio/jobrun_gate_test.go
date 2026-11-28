package audio

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeJobProtocolBinary writes an executable that speaks the structured
// job protocol just well enough for RunJob: --version, --capabilities, and
// --job --machine (which records its argv and emits a succeeded machine doc
// with one artifact under the job's output_root). The reported resolver
// version is configurable so a test can model a re-pointed engine whose
// identity no longer matches a captured job. exitCode (default 0) models the
// process exit status AFTER the machine document is emitted, so a test can
// model a contradictory "succeeded doc + nonzero exit" engine.
func writeJobProtocolBinary(t *testing.T, dir, resolverVersion string, exitCode ...int) string {
	t.Helper()
	code := 0
	if len(exitCode) > 0 {
		code = exitCode[0]
	}
	p := filepath.Join(dir, "job-protocol-bin")
	script := `#!/usr/bin/env python3
import json, os, sys

if "--version" in sys.argv:
    print("m4b-merge 2.2.0")
    sys.exit(0)
if "--capabilities" in sys.argv:
    print(json.dumps({"schema_version": 1, "resolver_version": "` + resolverVersion + `",
        "default_path_format": "{author}/{title}",
        "path_tokens": [{"name": "book_id", "value": "b", "example": "B"},
            {"name": "authors", "value": "a", "example": "A"},
            {"name": "narrators", "value": "n", "example": "N"},
            {"name": "title", "value": "t", "example": "T"}],
        "field_policies": {"values": ["write", "preserve", "clear"],
            "scalar_fields": ["title", "subtitle", "authors", "narrators",
                "series", "description", "genres", "year"]},
        "cover_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]},
        "chapter_policies": {"constraint": "str", "variants": ["write", "preserve", "clear"]}}))
    sys.exit(0)
job = None
for i, a in enumerate(sys.argv):
    if a == "--job" and i + 1 < len(sys.argv):
        job = sys.argv[i + 1]
record = os.environ.get("BRAGI_TEST_ARGV")
if record:
    with open(record, "w") as f:
        f.write(" ".join(sys.argv[1:]))
with open(job) as f:
    jf = json.load(f)
root = jf["output_root"]
rel = "Author/Title.m4b"
os.makedirs(os.path.join(root, "Author"), exist_ok=True)
with open(os.path.join(root, rel), "w") as f:
    f.write("book")
print(json.dumps({
    "schema_version": 1,
    "resolver_version": "` + resolverVersion + `",
    "status": "succeeded",
    "groups": [{
        "group": "main",
        "outcome": "succeeded",
        "artifacts": [{"relative_path": rel, "kind": "audio", "exists": True}],
        "files_moved": True
    }]
}))
sys.exit(` + strconv.Itoa(code) + `)
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write job-protocol binary: %v", err)
	}
	return p
}

func jobFileForRunTest() JobFile {
	return JobFile{
		SchemaVersion: JobSchemaVersion,
		Inputs:        []string{"/tmp/x/a.m4b"},
		InputSupplied: SuppliedMetadata{Title: "Title", Authors: []string{"Author"}},
		OutputRoot:    "/tmp/unused", // overridden by opts.OutputRoot
		PathFormat:    "{author}/{title}",
		Policies:      AllSupportedWritePolicyFor(SuppliedMetadata{Title: "Title", Authors: []string{"Author"}}),
	}
}

// TestRunJobRefusesIdentityMismatchBeforeLaunch: the pre-launch gate must
// probe the EXACT binary's capabilities and refuse a job captured under a
// different schema/resolver pair BEFORE the mutating command ever runs.
func TestRunJobRefusesIdentityMismatchBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	bin := writeJobProtocolBinary(t, dir, "2") // engine reports resolver 2
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	argv := filepath.Join(dir, "argv.txt")
	t.Setenv("BRAGI_TEST_ARGV", argv)

	res, err := proc.RunJob(context.Background(), "gate1", jobFileForRunTest(), JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	if res != nil {
		t.Fatalf("RunJob returned result %v on identity mismatch", res)
	}
	if err == nil || !strings.Contains(err.Error(), "pre-launch engine gate") {
		t.Fatalf("RunJob = %v; want pre-launch identity mismatch error", err)
	}
	// The mutating command must never have launched: no argv record and no
	// artifact tree under the root.
	if _, statErr := os.Stat(argv); !os.IsNotExist(statErr) {
		t.Fatalf("engine argv was recorded (%v) — the mutating command launched despite the identity mismatch", statErr)
	}
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		t.Fatalf("engine mutated the output root despite the identity mismatch: %v", entries)
	}
}

// TestRunJobForwardsNumCPUs: the configured CPU limit must reach the engine
// as --num-cpus on the execution command (non-default count asserted), not
// be silently discarded by the structured-job cutover.
func TestRunJobForwardsNumCPUs(t *testing.T) {
	dir := t.TempDir()
	bin := writeJobProtocolBinary(t, dir, "1")
	proc := NewProcessor(bin, t.TempDir(), "", 4, "{author}/{title}", "info")
	root := t.TempDir()

	argv := filepath.Join(dir, "argv.txt")
	t.Setenv("BRAGI_TEST_ARGV", argv)

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	res, err := proc.RunJob(context.Background(), "cpus1", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", res.Status)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("read recorded argv: %v", err)
	}
	if !strings.Contains(string(data), "--num-cpus 4") {
		t.Fatalf("engine argv = %q; want --num-cpus 4 forwarded", string(data))
	}
}

// TestRunJobOmitsZeroNumCPUs: a 0 (unset) limit must not be forwarded, so
// the engine applies its own available-parallelism default.
func TestRunJobOmitsZeroNumCPUs(t *testing.T) {
	dir := t.TempDir()
	bin := writeJobProtocolBinary(t, dir, "1")
	proc := NewProcessor(bin, t.TempDir(), "", 0, "{author}/{title}", "info")
	root := t.TempDir()

	argv := filepath.Join(dir, "argv.txt")
	t.Setenv("BRAGI_TEST_ARGV", argv)

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	if _, err := proc.RunJob(context.Background(), "cpus0", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	}); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("read recorded argv: %v", err)
	}
	if strings.Contains(string(data), "--num-cpus") {
		t.Fatalf("engine argv = %q; a 0 CPU limit must not be forwarded", string(data))
	}
}

// TestProbeCapabilitiesAppliesOwnTimeout: a hanging --capabilities probe must
// die on its documented 5s clock, not on the caller's (here, absent) context
// deadline.
func TestProbeCapabilitiesAppliesOwnTimeout(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hang-bin")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write hanging binary: %v", err)
	}
	proc := NewProcessor(p, "", "", 1, "{author}/{title}", "info")
	// No deadline on the caller's context: only the probe's own timeout can
	// bound this.
	if _, err := proc.ProbeCapabilitiesFor(context.Background(), p); err == nil {
		t.Fatal("ProbeCapabilitiesFor succeeded; want the probe timeout to kill the hanging binary")
	}
}

// TestRunJobRefusesPreexistingJobFileSymlink guards the O_EXCL create: a
// pre-existing SYMLINK at the predictable job-file path must make the open
// fail (the link is left untouched, and no write follows it to a target
// outside the job dir) instead of the job payload being written through the
// link. The manifest lives in the job-scoped subdirectory, so the link is
// planted there (pre-created, as a principal with directory write access
// could).
func TestRunJobRefusesPreexistingJobFileSymlink(t *testing.T) {
	dir := t.TempDir()
	bin := writeJobProtocolBinary(t, dir, "1")
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	// A pre-existing job dir holding a SYMLINK at the job-file path: the
	// dir is non-empty, so RunJob's residue handling (os.Remove of a
	// non-empty dir) fails fail-closed before the O_EXCL create, the link
	// is left untouched, and no write follows it to a target outside the
	// job dir. The manifest dir is under the system temp root (out of the
	// shared claim root), so the link is planted THERE, as a principal
	// with write access to that root could.
	jobDir := filepath.Join(os.TempDir(), "bragi-job-sym1")
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(jobDir) })
	outside := t.TempDir()
	target := filepath.Join(outside, "jobfile-target.json")
	link := filepath.Join(jobDir, ".bragi-job-sym1.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	res, err := proc.RunJob(context.Background(), "sym1", jobFileForRunTest(), JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	if res != nil {
		t.Fatalf("RunJob returned result %v on a pre-existing job-file symlink", res)
	}
	if err == nil {
		t.Fatal("RunJob succeeded despite the job-file path being a symlink")
	}
	// The write must not have followed the link: the target stays absent.
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("job payload written through the symlink into %s", target)
	}
	// The link itself is preserved (cleanup removes only files Bragi
	// created).
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Fatalf("pre-existing job-file symlink was removed: %v", statErr)
	}
}

// TestRunJobSucceededDocNonzeroExitIsFailure pins the CF-24 exit-code gate:
// a machine document that says "succeeded" while the process exits nonzero
// is a contradiction the document alone cannot settle. The run must return
// an error (not a success) and preserve the staging output so the artifact
// stays recoverable, instead of validating and publishing it.
func TestRunJobSucceededDocNonzeroExitIsFailure(t *testing.T) {
	dir := t.TempDir()
	bin := writeJobProtocolBinary(t, dir, "1", 3) // succeeded doc, exit 3
	proc := NewProcessor(bin, t.TempDir(), "", 1, "{author}/{title}", "info")
	root := t.TempDir()

	jf := jobFileForRunTest()
	jf.OutputRoot = root
	res, err := proc.RunJob(context.Background(), "exit3", jf, JobRunOptions{
		OutputRoot: root,
		Identity:   EngineIdentity{SchemaVersion: 1, ResolverVersion: "1"},
	})
	if err == nil {
		t.Fatal("RunJob = nil error; want a failure for a succeeded document with a nonzero exit")
	}
	if !strings.Contains(err.Error(), "succeeded document") {
		t.Fatalf("error = %v; want the contradiction diagnosis", err)
	}
	if res == nil {
		t.Fatal("RunJob returned a nil result; want the result with the exit code and preserved staging")
	}
	if res.Status != "succeeded" {
		t.Errorf("status = %q; want the document's succeeded (the run reports what the engine said)", res.Status)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d; want 3", res.ExitCode)
	}
	if res.StagingPreserved != root {
		t.Errorf("staging preserved = %q; want %q (the artifact must stay recoverable)", res.StagingPreserved, root)
	}
	if res.PrimaryOutput != "" {
		t.Errorf("primary output = %q; want empty (a contradictory run publishes nothing)", res.PrimaryOutput)
	}
	if len(res.OutputFiles) != 0 {
		t.Errorf("output files = %v; want none (the artifact is unvalidated staging)", res.OutputFiles)
	}
	// The artifact the engine published is still on disk under the root:
	// preservation means the engine's output was not cleaned up as residue.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read output root: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "Author" {
			found = true
		}
	}
	if !found {
		t.Fatalf("output root = %v; want the engine's published tree preserved", entries)
	}
}
