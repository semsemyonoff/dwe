package commands

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// serviceTargetProject is a project whose compose chain declares `app` in the
// base file and `otel-lookup` only inside a disabled tool's overlay (behind a
// compose profile, as the otel experiment shipped it). The dwe service `web`
// runs as compose service `app`.
var serviceTargetProject = map[string]string{
	"workspace.yml":                       "project:\n  name: shop\ncompose:\n  base: docker-compose.yml\n",
	"docker-compose.yml":                  "services:\n  app:\n    image: php\n",
	"workspace/services/web/service.yml":  "type: app\ndir: src\ncontainer: app\n",
	"workspace/services/otel/service.yml": "type: tool\ncompose: [workspace/services/otel/compose.yml]\n",
	"workspace/services/otel/compose.yml": "services:\n  otel-lookup:\n    image: curl\n    profiles: [lookup]\n",
}

func TestServiceTargetDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		// command is the body of one command named "target" in group "test".
		command string
		// extra files written on top of serviceTargetProject; "" deletes.
		extra    map[string]string
		wantMsg  string
		wantHint string
	}{
		{
			name:    "compose service of an enabled service",
			command: "type: service_exec\n    service: app\n    cmd: ls",
		},
		{
			name:    "compose-only service of a disabled tool overlay",
			command: "type: service_run\n    service: otel-lookup\n    cmd: ls",
		},
		{
			name:     "typo",
			command:  "type: service_run\n    service: otel-lookupx\n    cmd: ls",
			wantMsg:  `service: "otel-lookupx" is not a service in any compose overlay, disabled services included`,
			wantHint: `did you mean "otel-lookup"?`,
		},
		{
			name:     "runner.service overrides service",
			command:  "type: service_exec\n    service: app\n    runner:\n      service: ap\n    cmd: ls",
			wantMsg:  `runner.service: "ap" is not a service in any compose overlay, disabled services included`,
			wantHint: `did you mean "app"?`,
		},
		{
			name:     "dwe service key instead of its compose name",
			command:  "type: service_exec\n    service: web\n    cmd: ls",
			wantMsg:  `service: "web" is not a service in any compose overlay, disabled services included`,
			wantHint: `dwe service "web" runs as compose service "app"`,
		},
		{
			name:     "no close match lists the known names",
			command:  "type: service_exec\n    service: database\n    cmd: ls",
			wantMsg:  `service: "database" is not a service in any compose overlay, disabled services included`,
			wantHint: "known compose services: app, otel-lookup",
		},
		{
			name:    "templated value is skipped",
			command: "type: service_exec\n    service: ${param.svc}\n    params:\n      svc:\n        type: string\n        default: nope\n    cmd: ls",
		},
		{
			name:     "daemon source",
			command:  "type: daemon\n    service: otel-lookpu\n    argv: [sleep, infinity]\n    daemon:\n      container_template: shop-lookup",
			wantMsg:  `service: "otel-lookpu" is not a service in any compose overlay, disabled services included`,
			wantHint: `did you mean "otel-lookup"?`,
		},
		{
			name:    "host command is not checked",
			command: "type: shell\n    service: nope\n    cmd: ls",
		},
		{
			name:    "unreadable overlay silences the check",
			command: "type: service_run\n    service: otel-lookupx\n    cmd: ls",
			extra:   map[string]string{"workspace/services/otel/compose.yml": ""},
		},
		{
			name:    "top-level include silences the check",
			command: "type: service_run\n    service: from-include\n    cmd: ls",
			extra:   map[string]string{"docker-compose.yml": "include: [more.yml]\nservices:\n  app:\n    image: php\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := maps.Clone(serviceTargetProject)
			files["workspace/commands/test.yml"] = "commands:\n  target:\n    " + tt.command + "\n"
			maps.Copy(files, tt.extra)
			for rel, body := range files {
				p := filepath.Join(root, rel)
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				if v, ok := tt.extra[rel]; ok && v == "" {
					continue // deleted: the chain references a file that is absent
				}
				require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
			}

			cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
			require.NoError(t, err)
			require.False(t, cfg.Services["otel"].Enabled, "fixture: the otel tool must stay disabled")
			diags := (&Validator{}).Run(validate.Context{
				ProjectRoot: root,
				ConfigPath:  filepath.Join(root, "workspace.yml"),
				Cfg:         cfg,
			})

			var got []validate.Diagnostic
			for _, d := range diags {
				if strings.Contains(d.Message, "compose overlay") {
					got = append(got, d)
				}
			}
			if tt.wantMsg == "" {
				require.Empty(t, got, "all diagnostics: %#v", diags)
				return
			}
			require.Len(t, got, 1, "all diagnostics: %#v", diags)
			require.Equal(t, validate.SeverityWarning, got[0].Severity)
			require.Equal(t, "commands", got[0].Domain)
			require.Equal(t, "commands:test.target", got[0].Target)
			require.Equal(t, filepath.Join("workspace", "commands", "test.yml"), got[0].File)
			require.Equal(t, tt.wantMsg, got[0].Message)
			require.Contains(t, got[0].Hint, tt.wantHint)
		})
	}
}

// TestServiceTargetDiagnostics_NilConfig covers the partial-load path: without
// a merged config there is no compose chain to check against.
func TestServiceTargetDiagnostics_NilConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "workspace", "commands")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.yml"),
		[]byte("commands:\n  target:\n    type: service_exec\n    service: nope\n    cmd: ls\n"), 0o644))

	for _, d := range (&Validator{}).Run(validate.Context{ProjectRoot: root}) {
		require.NotContains(t, d.Message, "compose overlay")
	}
}
