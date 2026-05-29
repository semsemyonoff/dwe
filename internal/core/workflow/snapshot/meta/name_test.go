package meta

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "feature-x", false},
		{"digits-only", "0", false},
		{"alnum-dots-dashes-underscores", "a.b_c-1", false},
		{"max-len", "a" + strings.Repeat("b", 62), false},

		{"empty", "", true},
		{"too-long", "a" + strings.Repeat("b", 63), true},
		{"leading-dash", "-foo", true},
		{"leading-dot", ".foo", true},
		{"uppercase", "Foo", true},
		{"space", "foo bar", true},
		{"slash", "foo/bar", true},
		{"dotdot", "..", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
		})
	}
}
