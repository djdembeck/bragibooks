package audio

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// tokenRegistry is the set of "{key}" placeholders the web scheme accepts,
// populated from the ENGINE's verified capabilities at startup
// (Capabilities.PathTokens) — not a local copy of the engine's token set.
// Unknown braces are literal text, so a registry that drifts only changes
// which keys count as substitutions; the structural rules below are the
// safety-critical part and are local.
//
// The registry is a complete, immutable map published atomically: a binary
// re-point can republish it from a settings request while request and worker
// paths validate concurrently, and a partially built map read mid-replacement
// would race the write (fatal concurrent-map panic). Readers therefore always
// see either the old complete registry or the new complete one.
var tokenRegistry atomic.Pointer[map[string]struct{}]

// SetPathFormatTokens installs the engine's substitution tokens after its
// capabilities are verified. Must be called before any path-format
// validation runs (main does it right after CheckCapabilities).
func SetPathFormatTokens(tokens []string) {
	reg := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		reg[t] = struct{}{}
	}
	tokenRegistry.Store(&reg)
}

func pathTokenSet() map[string]struct{} {
	return *tokenRegistry.Load()
}

func init() {
	tokenRegistry.Store(&map[string]struct{}{})
}

const pathFormatIllegalChars = `<:>"\|?*`

var pathFormatReservedNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// pathFormatSegment is one "/"-separated path element: the concatenated
// literal text of its text runs plus a flag for any substituted token.
type pathFormatSegment struct {
	text     string // text runs concatenated (untrimmed); empty for pure-token segments
	hasToken bool
}

// validatePathFormatParse walks a template: every '/' separates segments,
// "{known-key}" is a token, and all other runs are literal text.
func parsePathFormat(template string) []pathFormatSegment {
	reg := pathTokenSet()
	var segs []pathFormatSegment
	var cur strings.Builder
	var curHasToken bool
	flush := func() {
		segs = append(segs, pathFormatSegment{text: cur.String(), hasToken: curHasToken})
		cur.Reset()
		curHasToken = false
	}
	for i := 0; i < len(template); {
		c := template[i]
		if c == '/' {
			flush()
			i++
			continue
		}
		if c == '{' {
			if end := strings.IndexByte(template[i+1:], '}'); end >= 0 {
				key := template[i+1 : i+1+end]
				if isPathFormatToken(reg, key) {
					curHasToken = true
					i += 2 + end
					continue
				}
			}
		}
		cur.WriteByte(c)
		i++
	}
	flush()
	return segs
}

func isPathFormatToken(reg map[string]struct{}, key string) bool {
	_, ok := reg[key]
	return ok
}

// NormalizeEnginePathFormat strips a trailing literal ".m4b" from a path
// format before it is handed to the engine's job protocol. The engine appends
// ".m4b" to the last component itself (and the legacy -p path did the same),
// so a stored format that already carries the extension must not gain a
// second one: "{author}/{title}.m4b" resolves to exactly the same file name
// as "{author}/{title}". Only the last path segment's suffix is considered,
// so a token like "{title}" or a literal "archive.tar" is untouched.
func NormalizeEnginePathFormat(template string) string {
	if idx := strings.LastIndexByte(template, '/'); idx >= 0 {
		rest := template[idx+1:]
		if n := strings.TrimSuffix(rest, ".m4b"); n != rest {
			return template[:idx+1] + n
		}
		return template
	}
	return strings.TrimSuffix(template, ".m4b")
}

// ValidateRelativePathFormat checks a template that must resolve INSIDE the
// output root (m4b-merge -p): relative — no leading "/" — and free of ".."
// path segments — before delegating the token/character rules to
// ValidatePathFormat. The traversal check runs on each segment AFTER
// strings.TrimSpace, the same trimming the path-side segment rules apply,
// so a whitespace-padded segment (" .. ") fails here instead of slipping
// past the raw-text comparison and resolving into a traversal. Used by
// output-profile writes, queue-time resolution, and worker validation so a
// stored snapshot can never steer writes outside the resolved destination.
func ValidateRelativePathFormat(template string) error {
	if strings.HasPrefix(template, "/") {
		return fmt.Errorf("relative path format must not start with '/'")
	}
	for _, seg := range strings.Split(template, "/") {
		trimmed := strings.TrimSpace(seg)
		if trimmed == ".." {
			return fmt.Errorf("relative path format must not contain '..' path segments")
		}
	}
	return ValidatePathFormat(template)
}

// ValidatePathFormat checks a m4b-merge -p template against the
// Linux+Windows character union (output roots are commonly foreign-OS
// mounts). Mirrors the web scheme rules so live UI feedback and saved
// 400s agree.
func ValidatePathFormat(template string) error {
	// One registry snapshot for the whole validation: a concurrent binary
	// re-point republishes the registry atomically, and mixing snapshots
	// between the E0 scan and the parse would make the two disagree.
	reg := pathTokenSet()
	// E0: brace-delimited tokens the engine does not know are rejected by the
	// engine's parser (PathResolutionError::UnknownToken), so a stored format
	// containing one must fail validation HERE, before save — the legacy
	// literal pass-through applied only to an UNCLOSED brace, which a closing
	// brace is not. An engine-version capability gap (registry empty) is not
	// a user typo: the boot gate already refused to start in that state.
	if len(reg) > 0 {
		for i := 0; i < len(template); i++ {
			if template[i] != '{' {
				continue
			}
			if end := strings.IndexByte(template[i+1:], '}'); end >= 0 {
				name := template[i+1 : i+1+end]
				if !isPathFormatToken(reg, name) {
					return fmt.Errorf("path format contains unsupported token {%s}", name)
				}
				i += end
			}
		}
	}

	segs := parsePathFormat(template)

	plainSegs := make([]string, 0, len(segs))
	anyElement := false
	for _, s := range segs {
		trimmed := strings.TrimSpace(s.text)
		if trimmed != "" || s.hasToken {
			anyElement = true
		}
		if !s.hasToken && trimmed != "" {
			plainSegs = append(plainSegs, trimmed)
		}
	}
	// E1: no path elements after trimming.
	if !anyElement {
		return fmt.Errorf("format produces no path elements")
	}

	// E2: forbidden characters in literal text (first offender wins).
	for _, s := range segs {
		for _, c := range s.text {
			if c < 0x20 || c == 0x7f {
				return fmt.Errorf("path text cannot contain U+%04X", c)
			}
			if strings.ContainsRune(pathFormatIllegalChars, c) {
				return fmt.Errorf("path text cannot contain %q", string(c))
			}
		}
	}

	// E3: reserved Windows device name as a static (token-free) segment.
	// plainSegs entries are already trimmed, so the base name matches the
	// client's trimmed rule (parity: a "con .txt" segment is reserved on
	// both sides).
	for _, seg := range plainSegs {
		base := seg
		if dot := strings.IndexByte(seg, '.'); dot >= 0 {
			base = strings.TrimSpace(seg[:dot])
		}
		if _, ok := pathFormatReservedNames[strings.ToLower(base)]; ok {
			return fmt.Errorf("segment %q is a reserved device name on Windows", strings.ToLower(base))
		}
	}

	// E4: at least one placeholder from the engine's substitution token
	// set. Tokens only exist in the exact "{key}" spelling, so a raw scan
	// is equivalent to scanning parsed parts. (The engine's set is
	// capability-driven; it no longer includes the legacy {asin} token.)
	for t := range reg {
		if strings.Contains(template, "{"+t+"}") {
			return nil
		}
	}
	return fmt.Errorf("format must include at least one placeholder such as {title}")
}
