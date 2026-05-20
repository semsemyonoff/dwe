package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/validate"
)

func writeTempDevbox(t *testing.T, body string) (root, configPath string) {
	t.Helper()
	root = t.TempDir()
	configPath = filepath.Join(root, "devbox.yml")
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root, configPath
}

func TestUIValidator(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		body       string
		wantSev    validate.Severity
		wantSubstr string
	}{
		{
			name:    "no ui block",
			body:    "schema_version: \"2\"\n",
			wantSev: 0, // no diagnostic emitted
		},
		{
			name:    "clean ui block",
			body:    "schema_version: \"2\"\nui:\n  commands:\n    default_expanded_depth: 2\n    auto_collapse_empty: false\n    show_type_badges: true\n",
			wantSev: validate.SeverityOK,
		},
		{
			name:       "negative depth is error",
			body:       "schema_version: \"2\"\nui:\n  commands:\n    default_expanded_depth: -1\n",
			wantSev:    validate.SeverityError,
			wantSubstr: "default_expanded_depth",
		},
		{
			name:       "unknown key is warning",
			body:       "schema_version: \"2\"\nui:\n  commands:\n    bogus: true\n",
			wantSev:    validate.SeverityWarning,
			wantSubstr: "bogus",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, cfgPath := writeTempDevbox(t, tc.body)
			v := &uiValidator{}
			diags := v.Run(validate.Context{ProjectRoot: root, ConfigPath: cfgPath})
			if tc.wantSev == 0 {
				if len(diags) != 0 {
					t.Fatalf("expected no diagnostics, got %d: %+v", len(diags), diags)
				}
				return
			}
			found := false
			for _, d := range diags {
				if d.Severity == tc.wantSev && (tc.wantSubstr == "" || strings.Contains(d.Message, tc.wantSubstr)) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected diagnostic with severity=%v substr=%q, got %+v", tc.wantSev, tc.wantSubstr, diags)
			}
		})
	}
}
