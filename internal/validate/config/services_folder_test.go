package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/validate"
)

// makeServiceFolder creates a minimal project with a devbox/services directory.
func makeServiceFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Create devbox.yml (not needed by the validator but keeps project structure valid).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "devbox"), 0o755))
	return dir
}

func TestServicesFolderValidator(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(root string)
		wantHasOK  bool
		wantErrors int
		wantWarns  int
		wantMsgHas []string
	}{
		{
			name: "no services directory",
			setup: func(root string) {
				// nothing — devbox/services/ does not exist
			},
			wantHasOK:  false,
			wantErrors: 0,
			wantWarns:  0,
		},
		{
			name: "well-formed folder produces no errors",
			setup: func(root string) {
				svcDir := filepath.Join(root, "devbox", "services", "redis")
				require.NoError(t, os.MkdirAll(svcDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: tool\ncontainer: redis\n"), 0o644))
			},
			wantHasOK:  true,
			wantErrors: 0,
			wantWarns:  0,
		},
		{
			name: "missing service.yml → error",
			setup: func(root string) {
				svcDir := filepath.Join(root, "devbox", "services", "myapp")
				require.NoError(t, os.MkdirAll(svcDir, 0o755))
				// no service.yml
			},
			wantHasOK:  false,
			wantErrors: 1,
			wantWarns:  0,
			wantMsgHas: []string{"myapp", "service.yml"},
		},
		{
			name: "unknown file in folder → warning",
			setup: func(root string) {
				svcDir := filepath.Join(root, "devbox", "services", "myapp")
				require.NoError(t, os.MkdirAll(svcDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: app\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "custom.txt"), []byte("extra"), 0o644))
			},
			wantHasOK:  true,
			wantErrors: 0,
			wantWarns:  1,
			wantMsgHas: []string{"myapp", "custom.txt"},
		},
		{
			name: "known files only (service + deploy + reset) → OK",
			setup: func(root string) {
				svcDir := filepath.Join(root, "devbox", "services", "postgres")
				require.NoError(t, os.MkdirAll(svcDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: infra\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte("phases: []\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte("phases: []\n"), 0o644))
			},
			wantHasOK:  true,
			wantErrors: 0,
			wantWarns:  0,
		},
		{
			name: "non-dir entry in services dir is ignored",
			setup: func(root string) {
				servicesDir := filepath.Join(root, "devbox", "services")
				require.NoError(t, os.MkdirAll(servicesDir, 0o755))
				// Write a plain file directly inside services/ (not a dir).
				require.NoError(t, os.WriteFile(filepath.Join(servicesDir, "notes.md"), []byte("ignore me"), 0o644))
				// Also a valid service folder so we get an OK.
				svcDir := filepath.Join(servicesDir, "redis")
				require.NoError(t, os.MkdirAll(svcDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: tool\n"), 0o644))
			},
			wantHasOK:  true,
			wantErrors: 0,
			wantWarns:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := makeServiceFolder(t)
			tt.setup(root)

			ctx := validate.Context{ProjectRoot: root}
			diags := (&servicesFolderValidator{}).Run(ctx)

			var okCount, errCount, warnCount int
			for _, d := range diags {
				switch d.Severity {
				case validate.SeverityOK:
					okCount++
				case validate.SeverityError:
					errCount++
				case validate.SeverityWarning:
					warnCount++
				}
			}

			if tt.wantHasOK {
				require.Equal(t, 1, okCount, "expected one OK diagnostic")
			} else {
				require.Equal(t, 0, okCount, "expected no OK diagnostic")
			}
			require.Equal(t, tt.wantErrors, errCount, "error count mismatch")
			require.Equal(t, tt.wantWarns, warnCount, "warning count mismatch")

			for _, substr := range tt.wantMsgHas {
				found := false
				for _, d := range diags {
					if d.Severity == validate.SeverityError || d.Severity == validate.SeverityWarning {
						if strings.Contains(d.Message, substr) || strings.Contains(d.Hint, substr) {
							found = true
							break
						}
					}
				}
				require.True(t, found, "expected message containing %q", substr)
			}
		})
	}
}
