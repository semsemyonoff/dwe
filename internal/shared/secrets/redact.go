package secrets

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// RedactPlaceholder replaces a secret value in redacted output.
const RedactPlaceholder = "***"

// MinRedactRunes is the shortest value worth redacting. Redacting a two-rune
// secret would shred every unrelated line that happens to contain it, so short
// values are deliberately left alone (the docs say so).
const MinRedactRunes = 4

// Redactor replaces known plaintext secrets in a line of diagnostic output. It
// is used by internal/shared/trace to keep dwe's own command echoes free of
// decrypted values; child-process output is out of scope.
type Redactor struct {
	values []string
}

// NewRedactor builds a redactor over values. Values shorter than
// MinRedactRunes are dropped, duplicates removed, and the rest sorted longest
// first so a value nested inside a longer one is replaced only once.
func NewRedactor(values []string) *Redactor {
	seen := make(map[string]struct{}, len(values))
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if utf8.RuneCountInString(v) < MinRedactRunes {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		kept = append(kept, v)
	}
	slices.SortFunc(kept, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return &Redactor{values: kept}
}

// Values returns the registered values, longest first. Callers must not
// mutate the result.
func (r *Redactor) Values() []string {
	if r == nil {
		return nil
	}
	return r.values
}

// Redact replaces every registered value in line with RedactPlaceholder. A nil
// or empty redactor returns the line unchanged.
func (r *Redactor) Redact(line string) string {
	if r == nil || len(r.values) == 0 || line == "" {
		return line
	}
	for _, v := range r.values {
		line = strings.ReplaceAll(line, v, RedactPlaceholder)
	}
	return line
}
