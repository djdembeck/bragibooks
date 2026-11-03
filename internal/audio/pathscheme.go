package audio

import (
	"fmt"
	"strings"
)

// pathFormatTokens is the set of "{key}" placeholders the web scheme accepts:
// m4b-merge's seven substitution tokens plus {asin}, which the engine leaves
// literal (mirrors format_path in the m4b-merge processor).
var pathFormatTokens = []string{
	"asin", "author", "narrator", "series_name", "series_position", "subtitle", "title", "year",
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
				if isPathFormatToken(key) {
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

func isPathFormatToken(key string) bool {
	for _, t := range pathFormatTokens {
		if t == key {
			return true
		}
	}
	return false
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

	// E4: at least one substitutable (non-asin) placeholder. Tokens only
	// exist in the exact "{key}" spelling, so a raw scan is equivalent to
	// scanning parsed parts.
	for _, t := range pathFormatTokens {
		if t == "asin" {
			continue
		}
		if strings.Contains(template, "{"+t+"}") {
			return nil
		}
	}
	return fmt.Errorf("format must include at least one placeholder such as {title}")
}
