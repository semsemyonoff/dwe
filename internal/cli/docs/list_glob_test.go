package docs

import "testing"

func TestCompilePathGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"empty matches all", "", "anything/at/all", true},
		{"single star one segment", "reference/config/*", "reference/config/services", true},
		{"single star does not cross slash", "reference/config/*", "reference/config/sub/x", false},
		{"double star prefix", "reference/**", "reference/config/sub/x", true},
		{"double star suffix", "**/services", "reference/config/services", true},
		{"double star around", "reference/**/services", "reference/config/services", true},
		{"double star around deep", "reference/**/services", "reference/cli/dwe_services", false},
		{"question mark one char", "config/?", "config/a", true},
		{"question mark not slash", "config/?", "config/sub/x", false},
		{"escape dot", "config.yml", "configXyml", false},
		{"miss", "reference/cli/*", "reference/config/services", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := compilePathGlob(tt.pattern)
			if err != nil {
				t.Fatalf("compile %q: %v", tt.pattern, err)
			}
			if got := m(tt.path); got != tt.want {
				t.Errorf("compilePathGlob(%q)(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
