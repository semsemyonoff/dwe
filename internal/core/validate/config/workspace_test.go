package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestWorkspaceValidator(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantDiags     int
		wantWorkspace validate.Severity
	}{
		{
			name:          "bad keys",
			fixture:       "dwe-v2-bad-keys",
			wantDiags:     1,
			wantWorkspace: validate.SeverityError,
		},
		{
			name:          "good config",
			fixture:       "dwe-v2-good",
			wantDiags:     1,
			wantWorkspace: validate.SeverityOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", tt.fixture)
			ctx := validate.Context{
				ProjectRoot: fixturePath,
				ConfigPath:  filepath.Join(fixturePath, "workspace.yml"),
			}

			v := &workspaceValidator{}
			diags := v.Run(ctx)

			t.Logf("fixture=%s: got %d diags", tt.fixture, len(diags))
			for i, d := range diags {
				t.Logf("  [%d] severity=%v target=%s file=%s message=%s", i, d.Severity, d.Target, d.File, d.Message)
			}

			require.Equal(t, tt.wantDiags, len(diags), "diagnostic count mismatch")

			if len(diags) > 0 {
				require.Equal(t, tt.wantWorkspace, diags[0].Severity)
				require.Equal(t, "config.workspace", diags[0].Target)
			}
		})
	}
}

func TestWorkspaceValidatorID(t *testing.T) {
	v := &workspaceValidator{}
	require.Equal(t, "workspace", v.ID())
	require.Equal(t, "config", v.Domain())
}

func TestWorkspaceValidator_DocsValidation_InvalidMermaidMode(t *testing.T) {
	// Write a dwe project with invalid mermaid mode
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
docs:
  mermaid: invalid_mode
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	// Should have validation error for invalid mermaid mode
	hasDiag(t, diags, validate.SeverityError, "invalid")
}

func TestWorkspaceValidator_DocsValidation_NegativeCacheSize(t *testing.T) {
	// Write a dwe project with negative cache size
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
docs:
  cache_size_mb: -10
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	// Should have validation error for negative cache size
	hasDiag(t, diags, validate.SeverityError, "non-negative")
}

// TestWorkspaceValidator_LocalComposeMalformed exercises the validate-surface
// for the new local.yml overlay validators (validateLocalCompose,
// validateOverlayCompose, validateNonLocalCompose). LoadConfig surfaces these
// as workspaceValidator diagnostics. NOTE: Diagnostic.File currently
// attributes to workspace.yml even when the underlying error originates in
// local.yml — the local.yml path appears only in the error message. A future
// cleanup PR could tighten attribution to the originating layer; for now, we
// assert on the message text. Tighten the File assertion if/when that lands.
func TestWorkspaceValidator_LocalComposeMalformed(t *testing.T) {
	tests := []struct {
		name        string
		workspace   string
		serviceYML  map[string]string // service name → service.yml body
		localYML    string
		wantMessage string
	}{
		{
			name: "project-wide compose.extra wrong shape in local.yml",
			workspace: `project:
  name: test
  prefix: test
`,
			localYML: `compose:
  extra: "not-a-list"
`,
			wantMessage: "workspace/local.yml",
		},
		{
			name: "project-wide compose.extra non-string entry in local.yml",
			workspace: `project:
  name: test
  prefix: test
`,
			localYML: `compose:
  extra:
    - 123
`,
			wantMessage: "compose.extra",
		},
		{
			name: "per-service compose.extra wrong shape in local.yml",
			workspace: `project:
  name: test
  prefix: test
`,
			serviceYML: map[string]string{
				"web": "type: app\ncontainer: web\ndir: web\n",
			},
			localYML: `services:
  web:
    compose:
      extra: "not-a-list"
`,
			wantMessage: "services.web.compose.extra",
		},
		{
			name: "compose.extra in workspace.yml rejected",
			workspace: `project:
  name: test
  prefix: test
compose:
  extra:
    - foo.yml
`,
			wantMessage: "per-developer overlays belong in workspace/local.yml",
		},
		{
			name: "project-wide absolute path rejected",
			workspace: `project:
  name: test
  prefix: test
`,
			localYML: `compose:
  extra:
    - /etc/x.yml
`,
			wantMessage: "absolute paths are not permitted",
		},
		{
			name: "project-wide escape path rejected",
			workspace: `project:
  name: test
  prefix: test
`,
			localYML: `compose:
  extra:
    - ../escape.yml
`,
			wantMessage: "escapes project root",
		},
		{
			name: "project-wide missing file rejected",
			workspace: `project:
  name: test
  prefix: test
`,
			localYML: `compose:
  extra:
    - gone.yml
`,
			wantMessage: "file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(tt.workspace), 0o644))
			for name, body := range tt.serviceYML {
				dir := filepath.Join(root, "workspace", "services", name)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(body), 0o644))
			}
			if tt.localYML != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "local.yml"), []byte(tt.localYML), 0o644))
			}

			ctx := validate.Context{
				ProjectRoot: root,
				ConfigPath:  filepath.Join(root, "workspace.yml"),
			}
			v := &workspaceValidator{}
			diags := v.Run(ctx)

			// Find the error diagnostic with the expected substring in the message.
			// (Diagnostic.File attribution quirk: points to workspace.yml even
			// for local.yml-originated errors — assert the message instead.)
			hasDiag(t, diags, validate.SeverityError, tt.wantMessage)
		})
	}
}

func TestWorkspaceValidator_DocsValidation_ValidConfig(t *testing.T) {
	// Write a dwe project with valid docs config
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
docs:
  mermaid: auto
  cache_size_mb: 100
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	// Should have no validation errors (OK for workspace check)
	require.True(t, len(diags) > 0, "expected at least 1 diagnostic (workspace)")
	require.Equal(t, validate.SeverityOK, diags[0].Severity) // workspace
}

// TestWorkspaceValidator_UnknownRootKey asserts that a strict-root violation
// (a custom key not under vars:) surfaces as a SeverityError diagnostic via the
// LoadConfig-error path. The check itself lives in the loader (Task 1); the
// validator only mirrors load errors as diagnostics-as-data.
func TestWorkspaceValidator_UnknownRootKey(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
db:
  host: localhost
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	d := hasDiag(t, diags, validate.SeverityError, "unknown top-level key")
	require.Equal(t, "config.workspace", d.Target)
	require.Contains(t, d.Message, "vars:")
}

// TestWorkspaceValidator_BadUpdateMode asserts that an out-of-range update.mode
// surfaces as a SeverityError diagnostic via the LoadConfig-error path. The
// load-time value check lives in the loader (Task 3).
func TestWorkspaceValidator_BadUpdateMode(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
update:
  mode: yes-please
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	d := hasDiag(t, diags, validate.SeverityError, "update.mode")
	require.Equal(t, "config.workspace", d.Target)
	require.Contains(t, d.Message, "on, off")
}

// TestWorkspaceValidator_GoodUpdateMode asserts a valid update block produces no
// error diagnostic — the workspace check stays SeverityOK.
func TestWorkspaceValidator_GoodUpdateMode(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
update:
  mode: on
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	require.True(t, len(diags) > 0, "expected at least 1 diagnostic (workspace)")
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
	require.Equal(t, "config.workspace", diags[0].Target)
}

// TestVarsWritablePatternValid pins the structural grammar the validator uses to
// flag bridge.vars_writable typos that would otherwise silently fail-closed.
func TestVarsWritablePatternValid(t *testing.T) {
	cases := []struct {
		pat  string
		want bool
	}{
		// Valid: exact vars path, trailing wildcard, the broad vars.* form.
		{"vars.db.host", true},
		{"vars.db.*", true},
		{"vars.*", true},
		{"vars.a.b.c", true},
		// Invalid: non-vars namespace.
		{"project.name", false},
		{"db.host", false},
		// Invalid: bare prefix.
		{"vars.", false},
		{"", false},
		// Invalid: interior wildcard (would match nothing in the matcher).
		{"vars.*.host", false},
		{"vars.db.*.x", false},
		{"vars.d*b.host", false},
		// Invalid: exact pattern carrying a stray '*'.
		{"vars.db*", false},
	}
	for _, tc := range cases {
		if got := varsWritablePatternValid(tc.pat); got != tc.want {
			t.Errorf("varsWritablePatternValid(%q) = %v, want %v", tc.pat, got, tc.want)
		}
	}
}

// TestWorkspaceValidator_BadVarsWritablePattern asserts an interior-wildcard typo
// in bridge.vars_writable surfaces a diagnostic (it would otherwise silently deny
// every container write).
func TestWorkspaceValidator_BadVarsWritablePattern(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services"), 0o755))

	workspaceYML := `
project:
  name: test
  prefix: test
bridge:
  vars_writable:
    - vars.*.host
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspaceYML), 0o644))

	ctx := validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
	}

	v := &workspaceValidator{}
	diags := v.Run(ctx)

	d := hasDiag(t, diags, validate.SeverityError, "vars_writable")
	require.Equal(t, "config.workspace.bridge.vars_writable", d.Target)
}

// writeServicesDir sets up a project root with per-folder services under workspace/services/
// for servicesValidator tests. The body is a YAML fragment shaped like `services: {name: {...}}`.
// Returns the project root path.
func writeServicesDir(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	type wrap struct {
		Services map[string]any `yaml:"services"`
	}
	var w wrap
	require.NoError(t, yaml.Unmarshal([]byte(body), &w))
	for name, svc := range w.Services {
		dir := filepath.Join(root, "workspace", "services", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		data, err := yaml.Marshal(svc)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), data, 0o644))
	}
	return root
}

// hasDiag asserts that diags contains one with the given severity whose
// Message contains substr. Returns the matched diagnostic for further checks.
func hasDiag(t *testing.T, diags []validate.Diagnostic, sev validate.Severity, substr string) validate.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Severity == sev && (substr == "" || contains(d.Message, substr)) {
			return d
		}
	}
	for _, d := range diags {
		t.Logf("  diag: sev=%v target=%s msg=%s", d.Severity, d.Target, d.Message)
	}
	t.Fatalf("no diagnostic with severity=%v containing %q", sev, substr)
	return validate.Diagnostic{}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestServicesValidator_MissingFileIsInfo(t *testing.T) {
	root := t.TempDir()
	v := &servicesValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityInfo, diags[0].Severity)
	require.Equal(t, "config.services", diags[0].Target)
}

func TestServicesValidator_GoodMixedTypes(t *testing.T) {
	body := `
services:
  app:
    type: app
    container: app
    dir: app
    ports:
      http: 3000
  cache:
    type: infra
    container: redis
    ports:
      tcp: 6379
    depends_on: [other]
  other:
    type: infra
    container: other
  adminer:
    type: tool
    container: adminer
    ports:
      web: 8080
`
	root := writeServicesDir(t, body)
	v := &servicesValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	// Exactly one OK summary, no errors, no warnings.
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityError, d.Severity, "unexpected error: %s", d.Message)
		require.NotEqual(t, validate.SeverityWarning, d.Severity, "unexpected warning: %s", d.Message)
	}
	hasDiag(t, diags, validate.SeverityOK, "")
}

func TestServicesValidator_MissingType(t *testing.T) {
	body := `
services:
  app:
    container: app
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	d := hasDiag(t, diags, validate.SeverityError, "missing type")
	require.Equal(t, "config.services:app", d.Target)
}

func TestServicesValidator_UnknownType(t *testing.T) {
	body := `
services:
  app:
    type: worker
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "unknown service type")
}

func TestServicesValidator_ToolWithAppOnlyField(t *testing.T) {
	body := `
services:
  adminer:
    type: tool
    container: adminer
    dir: adminer
    extends: foo
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, `field "dir" not allowed`)
	// "extends" on a non-app emits the more specific cross-type error only.
	hasDiag(t, diags, validate.SeverityError, "extends only permitted for type app")
}

func TestServicesValidator_BridgeFieldAllowedAllTypes(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: ./services/api
    bridge:
      enabled: true
      shim_path: /opt/bin/dwe
      on_unreachable: warn
  worker:
    type: infra
    container: worker
    bridge:
      enabled: true
  adminer:
    type: tool
    container: adminer
    bridge:
      enabled: false
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityError, d.Severity, "unexpected error: %s", d.Message)
		require.NotEqual(t, validate.SeverityWarning, d.Severity, "unexpected warning: %s", d.Message)
	}
	hasDiag(t, diags, validate.SeverityOK, "")
}

func TestServicesValidator_InfraExtendsRejected(t *testing.T) {
	body := `
services:
  db:
    type: infra
    container: pg
    extends: other
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "extends only permitted for type app")
}

func TestServicesValidator_AppMissingDirWarning(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	d := hasDiag(t, diags, validate.SeverityWarning, "no dir or dir_internal")
	require.Equal(t, "config.services:api", d.Target)
}

func TestServicesValidator_AppExtendsDirDoesNotWarn(t *testing.T) {
	body := `
services:
  main:
    type: app
    container: app-main
    dir: services/main
    dir_internal: /workspace
  main-debug:
    type: app
    container: app-main-debug
    extends: main
`
	root := writeServicesDir(t, body)
	ctx := validate.Context{
		ProjectRoot: root,
		Cfg: &devconfig.DweConfig{
			Services: map[string]devconfig.ServiceConfig{
				"main": {
					Type:        devconfig.ServiceTypeApp,
					Dir:         "services/main",
					DirInternal: "/workspace",
				},
				"main-debug": {
					Type:        devconfig.ServiceTypeApp,
					Dir:         "services/main",
					DirInternal: "/workspace",
				},
			},
		},
	}

	diags := (&servicesValidator{}).Run(ctx)
	for _, d := range diags {
		require.False(t,
			d.Severity == validate.SeverityWarning && contains(d.Message, "main-debug") && contains(d.Message, "no dir or dir_internal"),
			"unexpected inherited-dir warning: %+v",
			d,
		)
	}
}

func TestServicesValidator_AppExtendsSkipsDirWarningWhenConfigUnavailable(t *testing.T) {
	body := `
services:
  main-debug:
    type: app
    container: app-main-debug
    extends: main
`
	root := writeServicesDir(t, body)

	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	for _, d := range diags {
		require.False(t,
			d.Severity == validate.SeverityWarning && contains(d.Message, "main-debug") && contains(d.Message, "no dir or dir_internal"),
			"unexpected overlay warning: %+v",
			d,
		)
	}
}

func TestServicesValidator_DependsOnToolRejected(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: api
    depends_on: [adminer]
  adminer:
    type: tool
    container: adminer
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	d := hasDiag(t, diags, validate.SeverityError, `depends_on target "adminer" is type tool`)
	require.Equal(t, "config.services:api", d.Target)
}

func TestServicesValidator_DependsOnInfraAllowed(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: api
    depends_on: [db]
  db:
    type: infra
    container: pg
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityError, d.Severity, "unexpected error: %s", d.Message)
	}
}

func TestServicesValidator_PortsShape(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: api
    ports: 3000
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "ports must be a map")
}

func TestServicesValidator_PortOutOfRange(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: api
    ports:
      http: 99999
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "out of range")
}

func TestServicesValidator_HostsShape(t *testing.T) {
	body := `
services:
  api:
    type: app
    container: api
    dir: api
    hosts: somehost
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "hosts must be a map")
}

func TestServicesValidator_InterfaceCompileCheck(t *testing.T) {
	// Compile-time enforcement lives in workspace.go (`var _ validate.Validator = ...`).
	// This runtime smoke test exercises the ID()/Domain() pair as a second layer.
	v := &servicesValidator{}
	require.Equal(t, "services", v.ID())
	require.Equal(t, "config", v.Domain())
}

func writeStylesFile(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "styles.yml"), []byte(body), 0o644))
	return root
}

func TestStylesValidator_RenameWarnings(t *testing.T) {
	type want struct {
		msgContains  string
		hintContains string
	}
	tests := []struct {
		name string
		yaml string
		want []want
	}{
		{
			name: "label",
			yaml: "colors:\n  label: \"#fff\"\n",
			want: []want{{"colors.label is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "section_title",
			yaml: "colors:\n  section_title: \"#fff\"\n",
			want: []want{{"colors.section_title is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "subheader",
			yaml: "colors:\n  subheader: \"#fff\"\n",
			want: []want{{"colors.subheader is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "info",
			yaml: "colors:\n  info: \"#fff\"\n",
			want: []want{{"colors.info is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "table_header",
			yaml: "colors:\n  table_header: \"#fff\"\n",
			want: []want{{"colors.table_header is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "focus_border",
			yaml: "colors:\n  focus_border: \"#fff\"\n",
			want: []want{{"colors.focus_border is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "filter_match",
			yaml: "colors:\n  filter_match: \"#fff\"\n",
			want: []want{{"colors.filter_match is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "pagination_active",
			yaml: "colors:\n  pagination_active: \"#fff\"\n",
			want: []want{{"colors.pagination_active is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "mandatory",
			yaml: "colors:\n  mandatory: \"#fff\"\n",
			want: []want{{"colors.mandatory is no longer supported", "rename to colors.accent"}},
		},
		{
			name: "enabled",
			yaml: "colors:\n  enabled: \"#fff\"\n",
			want: []want{{"colors.enabled is no longer supported", "rename to colors.success"}},
		},
		{
			name: "partial",
			yaml: "colors:\n  partial: \"#fff\"\n",
			want: []want{{"colors.partial is no longer supported", "rename to colors.warning"}},
		},
		{
			name: "description",
			yaml: "colors:\n  description: \"#fff\"\n",
			want: []want{{"colors.description is no longer supported", "rename to colors.muted"}},
		},
		{
			name: "tree_count",
			yaml: "colors:\n  tree_count: \"#fff\"\n",
			want: []want{{"colors.tree_count is no longer supported", "rename to colors.muted"}},
		},
		{
			name: "tree_arrow",
			yaml: "colors:\n  tree_arrow: \"#fff\"\n",
			want: []want{{"colors.tree_arrow is no longer supported", "rename to colors.muted"}},
		},
		{
			name: "pagination_inactive",
			yaml: "colors:\n  pagination_inactive: \"#fff\"\n",
			want: []want{{"colors.pagination_inactive is no longer supported", "rename to colors.muted"}},
		},
		{
			name: "disabled",
			yaml: "colors:\n  disabled: \"#fff\"\n",
			want: []want{{"colors.disabled is no longer supported", "rename to colors.muted"}},
		},
		{
			name: "table_border",
			yaml: "colors:\n  table_border: \"#fff\"\n",
			want: []want{{"colors.table_border is no longer supported", "rename to colors.border"}},
		},
		{
			name: "help_block",
			yaml: "colors:\n  help:\n    title: \"#fff\"\n",
			want: []want{{"colors.help is no longer supported", "Fang help colors are derived"}},
		},
		{
			name: "header_color",
			yaml: "header:\n  color: \"cyan\"\n",
			want: []want{{"header.color is no longer supported", "always rendered in accent"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeStylesFile(t, tt.yaml)
			diags := (&stylesValidator{}).Run(validate.Context{ProjectRoot: root})
			for _, w := range tt.want {
				d := hasDiag(t, diags, validate.SeverityWarning, w.msgContains)
				if w.hintContains != "" {
					require.Contains(t, d.Hint, w.hintContains, "hint mismatch for %s", tt.name)
				}
			}
		})
	}
}

func TestStylesValidator_NewTokensSilent(t *testing.T) {
	yamlBody := `colors:
  accent: "#fff"
  success: "#fff"
  warning: "#fff"
  danger: "#fff"
  muted: "#fff"
  border: "#fff"
  text: "#fff"
header:
  lines:
    - "hello"
  font: "ANSI Shadow"
  tagline: "dev box"
`
	root := writeStylesFile(t, yamlBody)
	diags := (&stylesValidator{}).Run(validate.Context{ProjectRoot: root})
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityWarning, d.Severity, "unexpected warning: %s", d.Message)
	}
}

func TestStylesValidator_NoFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	diags := (&stylesValidator{}).Run(validate.Context{ProjectRoot: root})
	// no styles.yml -> info diagnostic only, no warnings
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityWarning, d.Severity)
	}
}

// TestResetValidator_NoFileIsSilent pins that an absent reset.yml produces no
// diagnostic at all. Unlike deploy.yml/lifecycle.yml, reset.yml is never
// shipped by the scaffold, so its absence is the universal default state on
// every project — not a deliberate opt-out worth reporting.
func TestResetValidator_NoFileIsSilent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	diags := (&resetValidator{}).Run(validate.Context{ProjectRoot: root})
	require.Empty(t, diags, "expected no diagnostic for absent reset.yml; got %+v", diags)
}

// TestResetValidator_FileExistsStillReportsOK confirms silencing the
// not-exist branch didn't silence the validator wholesale: a project that
// deliberately authored reset.yml still gets its OK diagnostic.
func TestResetValidator_FileExistsStillReportsOK(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "reset.yml"), []byte(`phases: []
`), 0o644))
	diags := (&resetValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityOK, "")
}

func TestServicesValidator_InfoTitleWithControlChars(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      title: "Bad\x00Title"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "info.title contains control characters")
}

func TestServicesValidator_InfoPathsEmpty(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - name: ""
          path: "/api"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "name is empty")
}

func TestServicesValidator_InfoPathPathEmpty(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - name: "docs"
          path: ""
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "path is empty")
}

func TestServicesValidator_InfoPathDuplicateNames(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - name: "docs"
          path: "/api/docs"
        - name: "docs"
          path: "/api/reference"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	diag := hasDiag(t, diags, validate.SeverityError, "duplicate name")
	require.Contains(t, diag.Message, "docs")
	require.Contains(t, diag.Hint, "docs")
}

func TestServicesValidator_InfoPathMissingLeadingSlash(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - name: "docs"
          path: "api/docs"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	diag := hasDiag(t, diags, validate.SeverityWarning, "does not start with /")
	require.Contains(t, diag.Message, "api/docs")
}

func TestServicesValidator_InfoPathValid(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      title: "Main API"
      paths:
        - name: "docs"
          path: "/api/docs"
        - name: "playground"
          path: "/graphql"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	// Should have one OK diagnostic for services
	hasDiag(t, diags, validate.SeverityOK, "")
	// Should not have any error diagnostics
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityError, d.Severity, "unexpected error: %s", d.Message)
	}
}

func TestServicesValidator_InfoPathsMissingName(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - path: "/api/docs"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "name is required")
}

func TestServicesValidator_InfoPathsMissingPath(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths:
        - name: "docs"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "path is required")
}

func TestServicesValidator_InfoNotAMap(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info: "invalid"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "info must be a map")
}

func TestServicesValidator_InfoPathsNotAList(t *testing.T) {
	t.Parallel()
	body := `
services:
  api:
    type: app
    container: api
    info:
      paths: "invalid"
`
	root := writeServicesDir(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "info.paths must be a list")
}

func TestInfoValidator_autoUrlsPortViaUnknownService(t *testing.T) {
	t.Parallel()
	body := `
sections:
  - id: urls
    items:
      - type: auto-urls
        port_via: nonexistent_service
`
	root := writeInfoYML(t, body)
	cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	diags := (&infoValidator{}).Run(validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	})
	diag := hasDiag(t, diags, validate.SeverityError, "port_via references unknown service")
	require.Contains(t, diag.Message, "nonexistent_service")
}

func TestInfoValidator_autoUrlsHideUnknownService(t *testing.T) {
	t.Parallel()
	body := `
sections:
  - id: urls
    items:
      - type: auto-urls
        hide: [unknown_service]
`
	root := writeInfoYML(t, body)
	cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	diags := (&infoValidator{}).Run(validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	})
	diag := hasDiag(t, diags, validate.SeverityWarning, "hide references unknown service")
	require.Contains(t, diag.Message, "unknown_service")
}

func TestInfoValidator_autoHostsIPInvalid(t *testing.T) {
	t.Parallel()
	body := `
sections:
  - id: hosts
    items:
      - type: auto-hosts
        ip: "not-an-ip"
`
	root := writeInfoYML(t, body)
	cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	diags := (&infoValidator{}).Run(validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	})
	diag := hasDiag(t, diags, validate.SeverityWarning, "ip")
	require.Contains(t, diag.Message, "does not parse")
}

func TestInfoValidator_autoHostsIPValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ip   string
	}{
		{name: "ipv4 loopback", ip: "127.0.0.1"},
		{name: "ipv6 loopback", ip: "::1"},
		{name: "ipv4 broadcast", ip: "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf(`
sections:
  - id: hosts
    items:
      - type: auto-hosts
        ip: %q
`, tt.ip)
			root := writeInfoYML(t, body)
			cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
			diags := (&infoValidator{}).Run(validate.Context{
				ProjectRoot: root,
				Cfg:         cfg,
			})
			// Should not have warning about IP
			for _, d := range diags {
				if d.Severity == validate.SeverityWarning {
					require.NotContains(t, d.Message, "does not parse")
				}
			}
		})
	}
}

func TestInfoValidator_autoUrlsValid(t *testing.T) {
	t.Parallel()
	body := `
sections:
  - id: urls
    items:
      - type: auto-urls
        include: [app, tool]
        hide: []
`
	root := writeInfoYML(t, body)
	cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	diags := (&infoValidator{}).Run(validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	})
	// Should have OK diagnostic
	hasDiag(t, diags, validate.SeverityOK, "")
}

// TestInfoValidator_decodeStates pins the honest verdict for the same four
// decode states config.LoadInfoConfig pins in TestLoadInfoConfig_fallbackStates:
// the all-comment/empty file and the deliberate `sections: []` must no longer
// read as SeverityOK — only an authored dashboard earns that.
func TestInfoValidator_decodeStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantSev    validate.Severity
		wantSubstr string
	}{
		{
			name: "fully commented",
			body: `# workspace/info.yml — inert mirror.
# sections:
#   - id: project
#     items: []
# footer: true
`,
			wantSev:    validate.SeverityInfo,
			wantSubstr: "built-in dashboard is active",
		},
		{
			name:       "empty file",
			body:       ``,
			wantSev:    validate.SeverityInfo,
			wantSubstr: "built-in dashboard is active",
		},
		{
			name: "deliberate empty sections",
			body: `sections: []
`,
			wantSev:    validate.SeverityInfo,
			wantSubstr: "deliberately empty",
		},
		{
			name: "one real section",
			body: `sections:
  - id: custom
    items:
      - type: separator
`,
			wantSev:    validate.SeverityOK,
			wantSubstr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeInfoYML(t, tt.body)
			cfg, _ := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
			diags := (&infoValidator{}).Run(validate.Context{
				ProjectRoot: root,
				Cfg:         cfg,
			})
			hasDiag(t, diags, tt.wantSev, tt.wantSubstr)
		})
	}
}

// TestInfoValidator_absentFileStaysInformational confirms Task 10 left the
// missing-file case untouched — only the present-file verdict was inverted.
func TestInfoValidator_absentFileStaysInformational(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	workspaceYML := filepath.Join(root, "workspace.yml")
	require.NoError(t, os.WriteFile(workspaceYML, []byte("project:\n  name: test\n"), 0o644))

	cfg, _ := devconfig.LoadConfig(workspaceYML)
	diags := (&infoValidator{}).Run(validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	})
	hasDiag(t, diags, validate.SeverityInfo, "no info.yml")
}

func writeInfoYML(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write minimal workspace.yml
	workspaceYML := filepath.Join(root, "workspace.yml")
	if err := os.WriteFile(workspaceYML, []byte("project:\n  name: test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile workspace.yml: %v", err)
	}

	// Write info.yml
	infoYML := filepath.Join(workspaceDir, "info.yml")
	if err := os.WriteFile(infoYML, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile info.yml: %v", err)
	}

	return root
}
