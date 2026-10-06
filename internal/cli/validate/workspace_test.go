package validate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
)

func TestValidateWorkspaceTemplates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		packs    string
		create   bool
		wantCode int
		wantText string
	}{
		{name: "clean pack", packs: "[tool]", create: true},
		{name: "empty list", packs: "[]", wantText: "no workspace packs configured"},
		{name: "missing pack", packs: "[missing]", wantCode: 1, wantText: "stat workspace pack \"missing\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			configPath := filepath.Join(root, "workspace.yml")
			if err := os.WriteFile(configPath, []byte("schema_version: \"2\"\nproject:\n  name: test\nrender:\n  workspace: "+tt.packs+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.create {
				pack := filepath.Join(root, "workspace", "templates", "workspace", "tool")
				if err := os.MkdirAll(pack, 0o755); err != nil {
					t.Fatal(err)
				}
				for name, content := range map[string]string{
					"manifest.yml": "render: [{from: task.md, to: .tool/task.md}]\n",
					"task.md":      "Review {{PLAN_FILE}} verbatim.\n",
				} {
					if err := os.WriteFile(filepath.Join(pack, name), []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			cmd := NewCmd("", &cmdctx.RootFlags{ConfigPath: configPath, Output: "json"})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"templates", "workspace"})
			err := cmd.Execute()
			if tt.wantCode == 0 && err != nil {
				t.Fatalf("validate failed: %v; stderr: %s", err, stderr.String())
			}
			if tt.wantCode != 0 {
				vfe, ok := err.(*validationFailedError)
				if !ok || vfe.ExitCode() != tt.wantCode {
					t.Fatalf("got error %v, want exit code %d", err, tt.wantCode)
				}
			}
			var result validateJSON
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("invalid diagnostic JSON: %v; stdout: %s", err, out.String())
			}
			if result.Summary.Scope != "templates/workspace" || result.Summary.Error != tt.wantCode {
				t.Fatalf("unexpected summary: %+v", result.Summary)
			}
			if tt.wantText == "" {
				if len(result.Diagnostics) != 1 || result.Diagnostics[0].Severity != "ok" || result.Diagnostics[0].Scope != "templates/templates.workspace" || result.Diagnostics[0].Message != "all workspace template packs valid" {
					t.Fatalf("want one OK diagnostic, got: %+v", result.Diagnostics)
				}
			} else if len(result.Diagnostics) != 1 || result.Diagnostics[0].Scope != "templates/templates.workspace" || !strings.Contains(result.Diagnostics[0].Message, tt.wantText) {
				t.Fatalf("unexpected diagnostics: %+v", result.Diagnostics)
			}
			if _, err := os.Lstat(filepath.Join(root, ".tool")); !os.IsNotExist(err) {
				t.Fatalf("validation created output: %v", err)
			}
		})
	}
}
