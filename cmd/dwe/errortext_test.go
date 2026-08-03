package main

import "testing"

// TestSentenceCaseFirstRune pins the replacement for fang's error-text
// transform. fang applies cases.Title to the entire first word, which mangles
// the identifiers dwe messages open with — the bug this function exists to fix.
func TestSentenceCaseFirstRune(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// The regressions. fang rendered these as "--Tty …" and "-C/--Command …".
		{"long flag", "--tty and --no-tty are mutually exclusive", "--tty and --no-tty are mutually exclusive"},
		{"short/long pair", "-c/--command cannot be empty", "-c/--command cannot be empty"},

		// Lowercase identifiers as the first word: fang made these "Dwe" and
		// "Workspace.Yml". Sentence-casing the first rune is acceptable here —
		// whole-word title-casing was not.
		{"lowercase word", "unknown command \"map\"", "Unknown command \"map\""},
		{"dotted filename", "workspace.yml is invalid", "Workspace.yml is invalid"},

		// Already-correct casing must survive untouched — fang turned "TTY" into "Tty".
		{"acronym stays intact", "TTY allocation failed", "TTY allocation failed"},
		{"already capitalised", "Unknown flag: --lang", "Unknown flag: --lang"},

		// Non-letter openers are left alone entirely.
		{"quoted", `"site.test" not found`, `"site.test" not found`},
		{"path", "/tmp/x: permission denied", "/tmp/x: permission denied"},
		{"digit", "3 services failed", "3 services failed"},

		// Leading whitespace is preserved, not consumed.
		{"leading space", "  unknown command", "  Unknown command"},

		// Degenerate inputs must not panic or corrupt.
		{"empty", "", ""},
		{"whitespace only", "   ", "   "},
		{"single letter", "x", "X"},

		// Multi-byte: the rune-length arithmetic must not split a UTF-8 sequence.
		{"cyrillic", "неизвестная команда", "Неизвестная команда"},
		{"non-cased script", "日本語のエラー", "日本語のエラー"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sentenceCaseFirstRune(tc.in); got != tc.want {
				t.Errorf("sentenceCaseFirstRune(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
