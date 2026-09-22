package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestComposeFilesValidator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// services maps folder name to service.yml body.
		services map[string]string
		// files are created relative to the project root.
		files    []string
		dirs     []string
		wantMsgs []string
		wantHint string
	}{
		{
			name:     "all files present is silent",
			services: map[string]string{"app": "type: app\ndir: src\ncompose: [compose/app.yml]\ncompose_after: [compose/patch.yml]\n"},
			files:    []string{"compose/app.yml", "compose/patch.yml"},
		},
		{
			name:     "missing compose file warns",
			services: map[string]string{"app": "type: app\ndir: src\ncompose: [compose/ap.yml]\n"},
			wantMsgs: []string{`service app lists compose file "compose/ap.yml", which does not exist`},
			wantHint: "not the service folder",
		},
		{
			name:     "missing compose_after file warns",
			services: map[string]string{"otel": "type: tool\ncompose_after: [compose/otel-patch.yml]\n"},
			wantMsgs: []string{`service otel lists compose_after file "compose/otel-patch.yml", which does not exist`},
		},
		{
			// Disabled services are checked too: --all operations load the file
			// today and enabling the service later would break the stack.
			name:     "disabled service is checked",
			services: map[string]string{"adminer": "type: tool\ncompose: [compose/missing.yml]\n"},
			wantMsgs: []string{`service adminer lists compose file "compose/missing.yml", which does not exist`},
		},
		{
			name:     "path written relative to the service folder gets a pointed hint",
			services: map[string]string{"app": "type: app\ndir: src\ncompose: [overlay.yml]\n"},
			files:    []string{"workspace/services/app/overlay.yml"},
			wantMsgs: []string{`service app lists compose file "overlay.yml", which does not exist`},
			wantHint: `did you mean "workspace/services/app/overlay.yml"?`,
		},
		{
			name:     "directory instead of file warns",
			services: map[string]string{"app": "type: app\ndir: src\ncompose: [compose]\n"},
			dirs:     []string{"compose"},
			wantMsgs: []string{`service app lists compose file "compose", which is a directory, not a compose file`},
		},
		{
			// A child inheriting the parent's list via extends: reports the
			// shared path once, naming both services.
			name: "inherited path is reported once",
			services: map[string]string{
				"main":  "type: app\ndir: src\ncompose: [compose/main.yml]\n",
				"main2": "type: app\ndir: src2\nextends: main\n",
			},
			wantMsgs: []string{`services main, main2 list compose file "compose/main.yml", which does not exist`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: shop\n"), 0o644))
			for name, body := range tt.services {
				dir := filepath.Join(root, "workspace", "services", name)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(body), 0o644))
			}
			for _, f := range tt.files {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("services: {}\n"), 0o644))
			}
			for _, d := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
			}

			cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
			require.NoError(t, err)
			diags := (&composeFilesValidator{}).Run(validate.Context{
				ProjectRoot: root,
				ConfigPath:  filepath.Join(root, "workspace.yml"),
				Cfg:         cfg,
			})

			require.Len(t, diags, len(tt.wantMsgs))
			for i, want := range tt.wantMsgs {
				require.Equal(t, validate.SeverityWarning, diags[i].Severity)
				require.Equal(t, "config.compose_files", diags[i].Target)
				require.Equal(t, want, diags[i].Message)
			}
			if tt.wantHint != "" {
				require.Contains(t, diags[0].Hint, tt.wantHint)
			}
		})
	}
}

// TestComposeFilesValidator_NilConfig covers the partial-load path: with no
// merged config the validator reads the service folders itself.
func TestComposeFilesValidator_NilConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "workspace", "services", "app")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte("type: app\ndir: src\ncompose: [nope.yml]\n"), 0o644))

	diags := (&composeFilesValidator{}).Run(validate.Context{ProjectRoot: root})
	require.Len(t, diags, 1)
	require.Equal(t, "workspace/services/app/service.yml", diags[0].File)
}
