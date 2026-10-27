package audio

import (
	"strings"
	"testing"
)

func TestValidatePathFormat(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		wantErrFr string // non-empty: error must contain this
	}{
		{name: "clean scheme", template: "{author}/{title}"},
		{name: "forbidden colon", template: "{title}:x", wantErrFr: `path text cannot contain ":"`},
		{name: "forbidden angle bracket", template: "{title}/x<>y", wantErrFr: `path text cannot contain "<"`},
		{name: "forbidden backslash", template: `{title}\vol`, wantErrFr: `path text cannot contain "\\"`},
		{name: "reserved con", template: "con", wantErrFr: `reserved device name`},
		{name: "reserved com1 with extension", template: "Com1.txt", wantErrFr: `reserved device name`},
		{name: "reserved check ignores mixed case and dot", template: "{title}/{author}/computer", wantErrFr: ``},
		{name: "reserved base name with space before dot", template: "con .txt", wantErrFr: `reserved device name`},
		{name: "reserved base name with NEL before extension", template: "con\u0085.txt", wantErrFr: `reserved device name`},
		{name: "reserved base name with ideographic space", template: "con\u3000.txt", wantErrFr: `reserved device name`},
		// U+FEFF is not whitespace per unicode.IsSpace, so the base name
		// stays "con\uFEFF" and is not reserved — mirrors the client set.
		// It still fails the no-placeholder check, which is expected.
		{name: "FEFF is not trimmed", template: "con\uFEFF.txt", wantErrFr: "must include at least one placeholder"},
		{name: "empty template", template: "", wantErrFr: "produces no path elements"},
		{name: "separators only", template: "///", wantErrFr: "produces no path elements"},
		{name: "whitespace only", template: "  / ", wantErrFr: "produces no path elements"},
		{name: "asin alone has no substitutable placeholder", template: "{asin}", wantErrFr: "must include at least one placeholder"},
		{name: "year scheme is valid (warnings are UI-only)", template: "{author}/{year}"},
		{name: "unicode text allowed", template: "{author}/{.Grüße} mídia"},
		{name: "control char forbidden", template: "{title}/a\x07b", wantErrFr: `path text cannot contain U+0007`},
		// E1/E2 boundary on control-only segments: "\t" is a unicode.IsSpace
		// rune, so trimming empties the segment and no element survives;
		// "\x07" is not, so the segment survives trimming and E2 fires.
		{name: "tab-only segment trims to empty (E1)", template: "\t", wantErrFr: "produces no path elements"},
		{name: "BEL-only segment survives trim (E2)", template: "\x07", wantErrFr: `path text cannot contain U+0007`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathFormat(tt.template)
			if tt.wantErrFr == "" {
				if err != nil {
					t.Fatalf("ValidatePathFormat(%q) = %v, want nil", tt.template, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidatePathFormat(%q) = nil, want error containing %q", tt.template, tt.wantErrFr)
			}
			if !strings.Contains(err.Error(), tt.wantErrFr) {
				t.Fatalf("ValidatePathFormat(%q) = %q, want error containing %q", tt.template, err, tt.wantErrFr)
			}
		})
	}
}

func TestValidateRelativePathFormat(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		wantErrFr string // non-empty: error must contain this
	}{
		{name: "relative scheme passes", template: "Genre/{title}"},
		{name: "relative multi-segment scheme passes", template: "{author}/{title}"},
		{name: "absolute scheme", template: "/abs/{title}", wantErrFr: "must not start with '/'"},
		{name: "rooted traversal", template: "../x/{title}", wantErrFr: "'..' path segment"},
		{name: "mid-segment traversal", template: "a/../../b/{title}", wantErrFr: "'..' path segment"},
		// Traversal is checked on the TRIMMED segment (the same trimming the
		// path-side segment rules apply), so whitespace padding cannot dress
		// up a ".." segment and slip past the raw-text comparison.
		{name: "whitespace-padded traversal segment", template: " .. /{title}", wantErrFr: "'..' path segment"},
		{name: "tab and vertical tab pad traversal segment", template: "\t..\v/{title}", wantErrFr: "'..' path segment"},
		{name: "ideographic space pads traversal segment", template: "\u3000..\u3000/{title}", wantErrFr: "'..' path segment"},
		{name: "mixed valid and padded-traversal segments", template: "Genre/{title} / .. /season", wantErrFr: "'..' path segment"},
		{name: "padded dot dot with text is not traversal", template: "{title}/ ..x/{author}", wantErrFr: ""},
		{name: "dotdot as literal text is fine", template: "{title}/fd..{author}", wantErrFr: ""},
		// Delegation: token/character rules of ValidatePathFormat must still
		// apply through the relative wrapper.
		{name: "no placeholder", template: "plain", wantErrFr: "must include at least one placeholder"},
		{name: "illegal char", template: "{title}:x", wantErrFr: `path text cannot contain ":"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRelativePathFormat(tt.template)
			if tt.wantErrFr == "" {
				if err != nil {
					t.Fatalf("ValidateRelativePathFormat(%q) = %v, want nil", tt.template, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRelativePathFormat(%q) = nil, want error containing %q", tt.template, tt.wantErrFr)
			}
			if !strings.Contains(err.Error(), tt.wantErrFr) {
				t.Fatalf("ValidateRelativePathFormat(%q) = %q, want error containing %q", tt.template, err, tt.wantErrFr)
			}
		})
	}
}
