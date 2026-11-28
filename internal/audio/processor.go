package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bragibooks/bragibooks/internal/config"
)

// Processor invokes the m4b-merge Rust CLI binary for audiobook processing.
// The CLI fields are mutable at runtime: ApplyConfig replaces them from the
// live config after a settings update, and every run snapshots them under
// the read lock so a single job observes one consistent set.
type Processor struct {
	mu           sync.RWMutex
	Binary       string
	OutputDir    string
	CompletedDir string
	NumCPUs      int
	PathFormat   string
	LogLevel     string
	version      string
	// capabilities is the engine's --capabilities document, verified at
	// startup and refreshed after any settings change that swaps the binary.
	// Every token/policy decision the API makes (preview validation, the
	// Settings picker, save-time format checks) derives from this — Bragi owns
	// no token registry of its own.
	capabilities *Capabilities
	// metadataFields is the saved per-field metadata write selection (#518),
	// refreshed by ApplyConfig after a settings update and snapshotted under
	// the same lock so every run observes one consistent value. Nil is the
	// default all-supported-available behavior; a non-nil (possibly empty)
	// slice is the explicit selection.
	metadataFields *[]string
	// metadataChapters is the saved chapter write policy ("write" default,
	// "preserve").
	metadataChapters string
}

// Capabilities returns the verified engine capabilities, or nil before the
// startup probe has run. Callers on the request path should treat nil as "not
// verified" and refuse, never as a fallback to a local token table.
func (p *Processor) Capabilities() *Capabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities
}

// StoreCapabilities records the verified capabilities for the current binary.
func (p *Processor) StoreCapabilities(c *Capabilities) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.capabilities = c
}

// snapshot returns a consistent copy of the mutable CLI settings (see the
// type comment for the locking contract).
func (p *Processor) snapshot() (binary, outputDir, completedDir, pathFormat, logLevel string, numCPUs int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Binary, p.OutputDir, p.CompletedDir, p.PathFormat, p.LogLevel, p.NumCPUs
}

// WriteSelectionSnapshot returns a consistent copy of the saved per-field
// metadata write selection (nil = default all-supported-available; non-nil
// = the explicit selection, possibly empty) and the chapter write policy.
func (p *Processor) WriteSelectionSnapshot() (fields *[]string, chapters string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	f := p.metadataFields
	if f != nil {
		cp := make([]string, len(*f))
		copy(cp, *f)
		f = &cp
	}
	c := p.metadataChapters
	if c == "" {
		c = ChaptersPolicyValueWrite // blank is the documented default
	}
	return f, c
}

// SettingsSnapshot returns a consistent copy of the effective destination
// and path-format settings for the worker: the output root the job's
// artifacts are published under, the completed directory (empty when
// disabled), and the global relative path format. A blank override in the
// caller's queue-time destination pair resolves to these live values.
func (p *Processor) SettingsSnapshot() (outputDir, completedDir, pathFormat string) {
	_, outputDir, completedDir, pathFormat, _, _ = p.snapshot()
	return
}

// QueueSnapshot is the ONE atomic read the queue freezes a job from: the
// per-field write selection AND the destination/path-format globals from a
// single locked read. Taking the two halves from separate reads would let a
// settings save change one between the reads and freeze a mixed generation
// into the job args; a job must observe one consistent settings instant.
type QueueSnapshot struct {
	// Fields is the write selection at that instant (nil = the default
	// all-supported-available behavior; non-nil, possibly empty, = the
	// explicit selection). A copy, never a live reference.
	Fields *[]string
	// Chapters is the chapter write policy ("" normalized to the
	// documented "write" default).
	Chapters string
	// OutputDir/CompletedDir/PathFormat are the effective destination
	// globals (a blank override resolves against these).
	OutputDir    string
	CompletedDir string
	PathFormat   string
}

// QueueSnapshot takes the single atomic read described above.
func (p *Processor) QueueSnapshot() QueueSnapshot {
	// One RLock for every field (no re-entrant snapshot() call: a
	// re-acquired read lock would deadlock behind a pending writer).
	p.mu.RLock()
	defer p.mu.RUnlock()
	f := p.metadataFields
	if f != nil {
		cp := make([]string, len(*f))
		copy(cp, *f)
		f = &cp
	}
	c := p.metadataChapters
	if c == "" {
		c = ChaptersPolicyValueWrite // blank is the documented default
	}
	return QueueSnapshot{
		Fields:       f,
		Chapters:     c,
		OutputDir:    p.OutputDir,
		CompletedDir: p.CompletedDir,
		PathFormat:   p.PathFormat,
	}
}

// ApplyConfig refreshes the mutable m4b-merge settings (binary, output and
// completed directories, CPU count, output scheme, log level) from the live
// config so the next job run uses the new values without a restart. Called
// by the settings handler after a successful update.
func (p *Processor) ApplyConfig(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Binary = cfg.M4bMerge.Binary
	p.OutputDir = cfg.Directories.OutputDir
	p.CompletedDir = cfg.Directories.CompletedDir
	p.NumCPUs = cfg.Processing.NumCPUs
	p.PathFormat = cfg.Processing.PathFormat
	p.LogLevel = cfg.Processing.LogLevel
}

// NewProcessor creates a Processor with the given configuration.
func NewProcessor(binary, outputDir, completedDir string, numCPUs int, pathFormat string, logLevel string) *Processor {
	return &Processor{
		Binary:       binary,
		OutputDir:    outputDir,
		CompletedDir: completedDir,
		NumCPUs:      numCPUs,
		PathFormat:   pathFormat,
		LogLevel:     logLevel,
	}
}

// ApplyWriteSelection refreshes the saved per-field metadata write selection
// (nil = default all-supported-available) and chapter write policy from the
// loaded config. Like ApplyConfig it runs under the write lock; main calls it
// after the database settings load, and the settings handler calls it after
// each successful save.
func (p *Processor) ApplyWriteSelection(fields *[]string, chapters string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metadataFields = fields
	p.metadataChapters = chapters
}

// ApplyConfigAndSelection refreshes the mutable settings AND the write
// selection under ONE lock: a settings save changes both, and a queue
// snapshot taken between two separate applies would freeze a mixed
// generation into the job args (the new selection with the old
// destination, or vice versa). QueueSnapshot must observe complete old
// or complete new state, never the in-between.
func (p *Processor) ApplyConfigAndSelection(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Binary = cfg.M4bMerge.Binary
	p.OutputDir = cfg.Directories.OutputDir
	p.CompletedDir = cfg.Directories.CompletedDir
	p.NumCPUs = cfg.Processing.NumCPUs
	p.PathFormat = cfg.Processing.PathFormat
	p.LogLevel = cfg.Processing.LogLevel
	p.metadataFields = cfg.Processing.MetadataFields
	p.metadataChapters = cfg.Processing.MetadataChapters
}

// ApplyConfigAndSelectionWithCapabilities is ApplyConfigAndSelection plus
// the verified capabilities, all under ONE lock. A binary swap changes the
// engine identity (the capabilities' schema/resolver, which loadSnapshotForQueue
// freezes into the job) AND the binary (which RunJob reads at run time);
// publishing those in two separate locked writes leaves an interval where a
// queue freezes the NEW identity with the OLD binary, and a worker that runs
// before the binary half lands trips the pre-launch identity gate. Setting
// both together makes every reader observe a matching (identity, binary)
// pair. caps is nil when no binary was swapped (the capabilities are left
// untouched, so a non-swap save cannot disturb a verified identity).
func (p *Processor) ApplyConfigAndSelectionWithCapabilities(cfg *config.Config, caps *Capabilities) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Binary = cfg.M4bMerge.Binary
	p.OutputDir = cfg.Directories.OutputDir
	p.CompletedDir = cfg.Directories.CompletedDir
	p.NumCPUs = cfg.Processing.NumCPUs
	p.PathFormat = cfg.Processing.PathFormat
	p.LogLevel = cfg.Processing.LogLevel
	p.metadataFields = cfg.Processing.MetadataFields
	p.metadataChapters = cfg.Processing.MetadataChapters
	if caps != nil {
		p.capabilities = caps
	}
}

// stripEscapeSequences removes ANSI/VT100 escape sequences from s so the
// captured m4b-merge output persists and renders as plain text. Only escape
// sequences are removed: newlines, tabs, and all ordinary text — including a
// literal "[32m" that is not preceded by ESC — are preserved.
func stripEscapeSequences(s string) string {
	const esc = 0x1b
	if !strings.ContainsRune(s, esc) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != esc {
			b.WriteByte(s[i])
			continue
		}
		i = escapeSequenceEnd(s, i)
	}
	return b.String()
}

// escapeSequenceEnd returns the index of the last byte of the escape
// sequence starting at s[i] (an ESC byte), so the caller's loop resumes just
// past it. A truncated sequence consumes the remainder of the input.
func escapeSequenceEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return i
	}
	switch s[j] {
	case '[': // CSI: parameter/intermediate bytes, then one final byte.
		for j++; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j
			}
		}
		return len(s) - 1
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC: BEL- or ST-terminated.
		for j++; j < len(s); j++ {
			if s[j] == 0x07 {
				return j
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 1
			}
		}
		return len(s) - 1
	default: // Zero or more intermediate bytes, then one final byte.
		// Non-CSI escapes may carry intermediates: VT100 charset
		// designations such as ESC ( B and ESC ) 0, or ESC # 8.
		for ; j < len(s); j++ {
			if s[j] < 0x20 || s[j] > 0x2f {
				return j
			}
		}
		return len(s) - 1
	}
}

// lineStreamWriter buffers stdout until a complete line is available, strips
// escape sequences, and hands each line to onLine. Buffering by line is safe
// for escape sequences: none contain a newline, so a sequence split across
// writes stays in the buffer until the line ends.
type lineStreamWriter struct {
	buf    []byte
	onLine func(string)
}

func (w *lineStreamWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits a final line that arrived without a trailing newline.
func (w *lineStreamWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	w.emit(w.buf)
	w.buf = nil
}

func (w *lineStreamWriter) emit(line []byte) {
	text := strings.TrimSuffix(stripEscapeSequences(string(line)), "\r")
	if text != "" {
		w.onLine(text)
	}
}

// MinimumM4bMergeVersion is the minimum required m4b-merge version. 2.2.0 is
// the first release with the full protocol Bragi's authoritative snapshot
// cutover runs on: schema_version 1 jobs with the plural people tokens AND
// a supplied-mode canonical book id that feeds the {book_id} naming token
// (#480). The capabilities probe is the authoritative protocol gate and
// this is the coarse floor reported in messages.
const MinimumM4bMergeVersion = "2.2.0"

// Version runs the m4b-merge binary with --version and returns the last
// whitespace-separated token of its output as the reported version.
func (p *Processor) Version() (string, error) {
	binary, _, _, _, _, _ := p.snapshot()
	cmd := exec.Command(binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		if ex, ok := err.(*exec.Error); ok {
			return "", fmt.Errorf("m4b-merge binary not found (%s): %w", ex.Name, ex.Err)
		}
		return "", fmt.Errorf("m4b-merge --version failed: %w", err)
	}

	version := strings.Fields(strings.TrimSpace(string(out)))
	if len(version) == 0 {
		return "", fmt.Errorf("m4b-merge --version produced empty output")
	}
	return version[len(version)-1], nil // last token is the version string
}

// CheckVersion runs the m4b-merge binary with --version and ensures it is
// at least MinimumM4bMergeVersion. Returns the reported version and an
// error if the binary cannot be found, fails to report a version, or is
// too old.
func (p *Processor) CheckVersion() (string, error) {
	reported, err := p.Version()
	if err != nil {
		return "", err
	}
	if err := checkSemver(reported, MinimumM4bMergeVersion); err != nil {
		return "", fmt.Errorf("m4b-merge version %s is too old, need >= %s", reported, MinimumM4bMergeVersion)
	}
	return reported, nil
}

// StoreVersion records the last successfully reported m4b-merge version
// for display in the settings page.
func (p *Processor) StoreVersion(v string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version = v
}

// versionProbeTimeout bounds a single "--version" probe: a hanging binary
// must not stall the caller.
const versionProbeTimeout = 5 * time.Second

// UpdateVersion re-snapshots and stores the m4b-merge version. The binary
// is probed with "--version" OUTSIDE the write lock (a subprocess running
// under p.mu would block every run, ApplyConfig, and version reader for
// the whole probe; the timeout above guarantees it always finishes). The
// store is a compare-and-update under the write lock: it only applies the
// result if Binary still matches the binary that was executed, so an
// interleaved reconfiguration can never pair a stale version with a new
// binary. It returns the version that resulted from the probe
// ("unknown" when the binary cannot report one), which the caller pairs
// with its own binary snapshot.
func (p *Processor) UpdateVersion() string {
	p.mu.RLock()
	binary := p.Binary
	p.mu.RUnlock()
	return p.probeVersion(context.Background(), binary)
}

// CheckVersionFor reports the version of the EXACT binary named and errors
// when it is missing, unreportable, or below MinimumM4bMergeVersion. Unlike
// CheckVersion it does not read p.Binary, so a caller that captured a
// binary snapshot can gate on the precise executable it is about to launch
// (the per-run pre-launch gate). The probe runs under the caller's context,
// so a cancelled run dies with the probe's process tree instead of stalling
// the gate. The probe never stores the result: the display version stays
// tied to the live config binary.
func (p *Processor) CheckVersionFor(ctx context.Context, binary string) (string, error) {
	if binary == "" {
		return "", errors.New("m4b-merge binary is not set")
	}
	// The CANDIDATE's own reported version is the one under test: the live
	// cached version (what probeVersion stores) belongs to p.Binary and is
	// meaningless when the candidate differs.
	reported := p.probeVersionReport(ctx, binary)
	if reported == "unknown" {
		return "", fmt.Errorf("m4b-merge %s cannot report a version", binary)
	}
	if err := checkSemver(reported, MinimumM4bMergeVersion); err != nil {
		return "", fmt.Errorf("m4b-merge version %s is too old, need >= %s", reported, MinimumM4bMergeVersion)
	}
	return reported, nil
}

// probeVersion runs the --version probe against binary ("" yields "unknown"
// without spawning anything) and stores the result only when binary is the
// live config binary.
func (p *Processor) probeVersion(ctx context.Context, binary string) string {
	reported := p.probeVersionReport(ctx, binary)
	return p.storeLinkedVersion(binary, reported)
}

// probeVersionReport runs the --version probe against binary and returns the
// version the probe itself reported, WITHOUT storing it: callers validating
// a candidate binary must gate on the candidate's own report, not the live
// cache entry that belongs to p.Binary.
func (p *Processor) probeVersionReport(ctx context.Context, binary string) string {
	if binary == "" {
		// Nothing to execute; never spawn the bare "" command.
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()

	// Execute, retrying the transient ETXTBSY: Linux refuses to exec a
	// file while ANY fd in the process is open for writing it, so a
	// sibling thread's fork can carry the writer's O_CLOEXEC fd past the
	// writer's close and tax a freshly written binary until that
	// inherited fd dies (golang/go#22315). Each attempt is a fresh cmd,
	// because a failed Start marks the cmd consumed; the probe's context
	// deadline bounds the whole retry sequence.
	var (
		reported  string
		stdoutBuf bytes.Buffer
	)
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(50*attempt) * time.Millisecond)
		}
		cmd := exec.CommandContext(ctx, binary, "--version")
		// The whole tree: ctx cancellation must kill the spawned process
		// and everything it spawned (see processTree), not just the direct
		// child. m4b_merge_binary is user-settable and may be a script
		// wrapper; a grandchild that inherits the stdout pipe would
		// otherwise hold the probe open long after the child itself is
		// killed, stalling the settings request past the deadline.
		tree, terr := newProcessTree(cmd)
		if terr != nil {
			log.Printf("Warning: m4b-merge version probe for binary %s failed: %v", binary, terr)
			break
		}
		cmd.Cancel = func() error {
			return tree.kill()
		}
		// Guard against any writer we could not kill: force Wait to
		// return shortly after cancel instead of blocking on inherited
		// pipes.
		cmd.WaitDelay = 5 * time.Second
		// os/exec owns the drain: with buffers wired directly as the
		// child's stdout/stderr, exec internally reads both streams
		// concurrently and guarantees they are fully drained (and the
		// buffers valid) once Run returns.
		stdoutBuf.Reset()
		var stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Start()
		if err == nil {
			// Constrain the child to the tree NOW, before it can spawn
			// descendants that would escape a cancel-time assignment.
			if serr := tree.start(cmd); serr != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				err = serr
			} else {
				err = cmd.Wait()
			}
		}
		tree.finish()
		switch {
		case err == nil:
			version := strings.Fields(strings.TrimSpace(stdoutBuf.String()))
			if len(version) > 0 {
				reported = version[len(version)-1] // last token is the version string
			}
		case !errors.Is(err, syscall.ETXTBSY) || attempt >= 2:
			// Missing binary, permission denied, nonzero exit,
			// timeout, or empty output: record why the probe
			// produced no version.
			log.Printf("Warning: m4b-merge version probe for binary %s failed: %v%s",
				binary, err, formattedStderr(strings.TrimSpace(stderrBuf.String())))
		default:
			continue // transient ETXTBSY: let a later attempt retry
		}
		break
	}
	if reported == "" {
		reported = "unknown"
	}
	return reported
}

// formattedStderr appends the child's stderr as a suffix to a log line.
func formattedStderr(stderr string) string {
	if stderr == "" {
		return ""
	}
	return "; stderr: " + stderr
}

// storeLinkedVersion stores version under the write lock only if the
// binary field still matches the value the version was captured from,
// so a reconfiguration that landed while the probe ran is never
// shadowed by a result taken against the previous binary. It returns
// the version now in effect (which may be a concurrent writer's) so
// the result always corresponds to a consistent binary/version pair.
func (p *Processor) storeLinkedVersion(snapshotBinary, version string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Binary != snapshotBinary {
		return p.version
	}
	p.version = version
	return p.version
}

// ReadVersion returns the last recorded m4b-merge version, or "" if none
// has been stored.
func (p *Processor) ReadVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// checkSemver returns nil if got >= want. Both are "MAJOR.MINOR.PATCH" strings.
// Short forms ("2", "v2", "1.0") are tolerated: missing components count as 0,
// and a leading "v" is ignored.
func checkSemver(got, want string) error {
	parse := func(v string) (int, int, int) {
		parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
		atoiPart := func(i int) int {
			if i >= len(parts) {
				return 0
			}
			n, _ := strconv.Atoi(parts[i])
			return n
		}
		return atoiPart(0), atoiPart(1), atoiPart(2)
	}

	gMaj, gMin, gPat := parse(got)
	wMaj, wMin, wPat := parse(want)

	if gMaj > wMaj {
		return nil
	}
	if gMaj < wMaj {
		return fmt.Errorf("%s < %s", got, want)
	}
	if gMin > wMin {
		return nil
	}
	if gMin < wMin {
		return fmt.Errorf("%s < %s", got, want)
	}
	if gPat < wPat {
		return fmt.Errorf("%s < %s", got, want)
	}
	return nil
}
