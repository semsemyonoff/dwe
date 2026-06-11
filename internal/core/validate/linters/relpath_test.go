package linters

import (
	"path/filepath"
	"testing"
)

func TestRelToBase(t *testing.T) {
	// Build base and the outside-base path from t.TempDir()/filepath.Join so
	// filepath.IsAbs treats them as absolute on every OS — Unix-rooted literals
	// like "/home/..." are not absolute on Windows and would steer relToBase
	// down the wrong branch.
	base := t.TempDir()
	outside := filepath.Join(filepath.Dir(base), "outside", "passwd")

	tests := []struct {
		name string
		file string
		want string
	}{
		{
			name: "absolute under base is relativized",
			file: filepath.Join(base, "services", "api", "Dockerfile"),
			want: filepath.FromSlash("services/api/Dockerfile"),
		},
		{
			name: "already relative passes through",
			file: filepath.FromSlash("a.sh"),
			want: filepath.FromSlash("a.sh"),
		},
		{
			name: "empty passes through",
			file: "",
			want: "",
		},
		{
			name: "absolute outside base keeps original",
			file: outside,
			want: outside,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relToBase(base, tt.file); got != tt.want {
				t.Errorf("relToBase(%q, %q) = %q, want %q", base, tt.file, got, tt.want)
			}
		})
	}
}

func TestRelToBase_EmptyBase(t *testing.T) {
	abs := filepath.FromSlash("/home/user/project/x.sh")
	if got := relToBase("", abs); got != abs {
		t.Errorf("empty base should pass through, got %q", got)
	}
}
