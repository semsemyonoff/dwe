package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func runPortsExportsValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)
	return (&portsExportsValidator{}).Run(validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
		Cfg:         cfg,
	})
}

// TestPortsExportsValidator_Unexported pins the live beetDeck defect: service
// "app" declares ports.http with no exports.env rule reading from it, while
// the sibling ports.admin IS paired via exports.env and must stay silent.
func TestPortsExportsValidator_Unexported(t *testing.T) {
	t.Parallel()
	root := filepath.Join("testdata", "ports_unexported")
	diags := runPortsExportsValidator(t, root)

	d := hasDiag(t, diags, validate.SeverityWarning, "services.app.ports.http")
	require.Equal(t, "config.ports_exports", d.Target)
	require.Equal(t, filepath.Join("workspace", "services", "app", "service.yml"), d.File)
	require.Contains(t, d.Message, "app")
	require.Contains(t, d.Message, "ports.http")
	require.Contains(t, d.Hint, "display-only")
	require.Contains(t, d.Hint, "local.yml")
	require.Contains(t, d.Hint, "dwe test")

	// The paired port must not also warn.
	for _, diag := range diags {
		require.NotContains(t, diag.Message, "ports.admin")
	}
	require.Len(t, diags, 1)
}

func TestPortsExportsValidator_PairedPortIsSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := `project:
  name: test
exports:
  env:
    - name: APP_HTTP_PORT
      from: services.app.ports.http
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspace), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services", "app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "workspace", "services", "app", "service.yml"),
		[]byte("type: app\nrequired: true\nports:\n  http: 8080\n"),
		0o644,
	))

	diags := runPortsExportsValidator(t, root)
	require.Empty(t, diags)
}

func TestPortsExportsValidator_NoPortsIsSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services", "app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "workspace", "services", "app", "service.yml"),
		[]byte("type: app\nrequired: true\n"),
		0o644,
	))

	diags := runPortsExportsValidator(t, root)
	require.Empty(t, diags)
}

// TestPortsExportsValidator_DisabledServiceIsSilent confirms a disabled
// service's unexported port does not warn — it never binds, so nothing is
// display-only about it.
func TestPortsExportsValidator_DisabledServiceIsSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace", "services", "app"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "workspace", "services", "app", "service.yml"),
		[]byte("type: app\nports:\n  http: 8080\n"),
		0o644,
	))

	diags := runPortsExportsValidator(t, root)
	require.Empty(t, diags)
}

// TestPortsExportsValidator_ExtendsChildIsSilent pins that a service which
// declares no ports of its own — and therefore inherits the parent's whole
// port map through extends — is not reported a second time. The parent keeps
// its own finding; the child's service.yml never mentions the port, so a
// diagnostic anchored there would send the reader to the wrong file.
func TestPortsExportsValidator_ExtendsChildIsSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"app", "type: app\nrequired: true\nports:\n  http: 8080\n"},
		{"worker", "type: app\nrequired: true\nextends: app\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.app.ports.http")
	require.Equal(t, filepath.Join("workspace", "services", "app", "service.yml"), diags[0].File)
}

// TestPortsExportsValidator_ExtendsDisabledParentWarns pins the limit of the
// inheritance skip: it defers to the parent's own finding, but this validator
// only iterates ENABLED services (DeployOrder). A disabled `extends:` template
// never gets its turn, so an enabled child inheriting from one has to report
// the port itself or nobody does.
func TestPortsExportsValidator_ExtendsDisabledParentWarns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"base", "type: app\nports:\n  http: 8080\n"},
		{"worker", "type: app\nrequired: true\nextends: base\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.worker.ports.http")
	require.Equal(t, filepath.Join("workspace", "services", "worker", "service.yml"), diags[0].File)
}

// TestPortsExportsValidator_ExtendsChildOwnPortWarns is the counterpart: a
// child that declares its own port does not inherit, so the skip must not
// swallow it.
func TestPortsExportsValidator_ExtendsChildOwnPortWarns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := `project:
  name: test
exports:
  env:
    - name: APP_HTTP_PORT
      from: services.app.ports.http
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspace), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"app", "type: app\nrequired: true\nports:\n  http: 8080\n"},
		{"worker", "type: app\nrequired: true\nextends: app\nports:\n  metrics: 9090\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.worker.ports.metrics")
}

// TestPortsExportsValidator_ExtendsChildSharedPortNameWarns pins the
// all-or-nothing shape of extends port inheritance: the loader clones the
// parent's map only when the child declares NO ports, so a child declaring its
// own `http` inherits nothing — even though an ancestor happens to use the same
// port name. Asking per port name whether some ancestor uses it would swallow
// this genuinely unexported port.
func TestPortsExportsValidator_ExtendsChildSharedPortNameWarns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := `project:
  name: test
exports:
  env:
    - name: APP_HTTP_PORT
      from: services.app.ports.http
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(workspace), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"app", "type: app\nrequired: true\nports:\n  http: 8080\n"},
		{"worker", "type: app\nrequired: true\nextends: app\nports:\n  http: 8081\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.worker.ports.http")
	require.Equal(t, filepath.Join("workspace", "services", "worker", "service.yml"), diags[0].File)
}

// TestPortsExportsValidator_ExtendsThroughDisabledTemplateIsSilent pins the
// multi-hop case: ResolveServiceExtends runs in topological order, so the
// disabled intermediate template already carries the enabled base's cloned port
// map by the time the grandchild clones it in turn. Answering on the first
// ancestor that has ports would find the disabled template and make the
// grandchild warn — a duplicate of base's own finding, anchored at a
// service.yml that never declares the port.
func TestPortsExportsValidator_ExtendsThroughDisabledTemplateIsSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"base", "type: app\nrequired: true\nports:\n  http: 8080\n"},
		{"template", "type: app\nextends: base\n"},
		{"worker", "type: app\nrequired: true\nextends: template\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.base.ports.http")
	require.Equal(t, filepath.Join("workspace", "services", "base", "service.yml"), diags[0].File)
}

// TestPortsExportsValidator_ExtendsThroughAllDisabledWarns is the counterpart:
// when NO ancestor in the equal-map chain is enabled, nobody else reports the
// port, so the enabled leaf still has to.
func TestPortsExportsValidator_ExtendsThroughAllDisabledWarns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	for _, svc := range []struct{ name, body string }{
		{"base", "type: app\nports:\n  http: 8080\n"},
		{"template", "type: app\nextends: base\n"},
		{"worker", "type: app\nrequired: true\nextends: template\n"},
	} {
		dir := filepath.Join(root, "workspace", "services", svc.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(svc.body), 0o644))
	}

	diags := runPortsExportsValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "services.worker.ports.http")
	require.Equal(t, filepath.Join("workspace", "services", "worker", "service.yml"), diags[0].File)
}

func TestPortsExportsValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&portsExportsValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}
