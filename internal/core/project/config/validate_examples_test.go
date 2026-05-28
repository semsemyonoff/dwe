package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLintersDocExamples round-trips the YAML snippets from
// docs/reference/config/validate.md § External linters through LoadValidateConfig
// so doc rot fails CI rather than silently misleading users.
func TestLintersDocExamples(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "wire-layout",
			yaml: `linters:
  shellcheck:
    enabled: true
    bin: shellcheck
    paths: [devbox/scripts, scripts]
    extensions: [.sh, .bash]
    flags: [--severity=warning]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
    extensions: [.dockerfile]
  yamllint:
    type: generic
    bin: yamllint
    paths: ["."]
    extensions: [.yml, .yaml]
    flags: [-s]
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "validate.yml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			cfg, warnings, err := LoadValidateConfig(path)
			if err != nil {
				t.Fatalf("LoadValidateConfig: %v", err)
			}
			if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %+v", warnings)
			}
			if len(cfg.Linters) != 3 {
				t.Fatalf("linters: got %d, want 3", len(cfg.Linters))
			}
		})
	}
}
