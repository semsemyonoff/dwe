package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// makeDeployAfterProject creates a temporary project root with the given
// services map and optional per-service deploy.yml contents.
// services maps service name → type string; deployContents maps service name → deploy.yml YAML content.
// enabledServices is the set of service names considered enabled (others default to disabled).
// projectDeployYAML and projectResetYAML are optional project-wide file contents.
// serviceResetContents maps service name → reset.yml YAML content.
func makeDeployAfterProject(t *testing.T,
	services map[string]string,
	deployContents map[string]string,
	enabledServices map[string]bool,
	projectDeployYAML, projectResetYAML string,
	serviceResetContents map[string]string,
) (root string, cfg *devconfig.DevboxConfig) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "devbox"), 0o755))

	// Build ctx.Cfg manually so tests don't depend on LoadConfig succeeding.
	svcMap := make(map[string]devconfig.ServiceConfig, len(services))
	for name, svcType := range services {
		svcDir := filepath.Join(root, "devbox", "services", name)
		require.NoError(t, os.MkdirAll(svcDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"),
			[]byte("type: "+svcType+"\n"), 0o644))

		enabled := enabledServices == nil || enabledServices[name]
		svcMap[name] = devconfig.ServiceConfig{
			Type:    devconfig.ServiceType(svcType),
			Enabled: enabled,
		}
	}
	for name, content := range deployContents {
		depPath := filepath.Join(root, "devbox", "services", name, "deploy.yml")
		require.NoError(t, os.WriteFile(depPath, []byte(content), 0o644))
	}
	for name, content := range serviceResetContents {
		resetPath := filepath.Join(root, "devbox", "services", name, "reset.yml")
		require.NoError(t, os.WriteFile(resetPath, []byte(content), 0o644))
	}
	if projectDeployYAML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "devbox", "deploy.yml"),
			[]byte(projectDeployYAML), 0o644))
	}
	if projectResetYAML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "devbox", "reset.yml"),
			[]byte(projectResetYAML), 0o644))
	}

	cfg = &devconfig.DevboxConfig{Services: svcMap}
	return root, cfg
}

func runDeployAfter(t *testing.T, root string, cfg *devconfig.DevboxConfig) []validate.Diagnostic {
	t.Helper()
	ctx := validate.Context{
		ProjectRoot: root,
		Cfg:         cfg,
	}
	return (&deployAfterValidator{}).Run(ctx)
}

func TestDeployAfterValidator_WellFormedNoAfter(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool", "postgres": "infra"},
		map[string]string{
			"redis":    "phases: []\n",
			"postgres": "phases: []\n",
		},
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireOK(t, diags, "config.deploy-after")
}

func TestDeployAfterValidator_SelfReference(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool"},
		map[string]string{"redis": "after:\n  - redis\nphases: []\n"},
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "references itself")
}

func TestDeployAfterValidator_UnknownService(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool"},
		map[string]string{"redis": "after:\n  - nonexistent\nphases: []\n"},
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "unknown service")
}

func TestDeployAfterValidator_ServiceWithoutDeployYML(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool", "postgres": "infra"},
		map[string]string{"redis": "after:\n  - postgres\nphases: []\n"},
		// postgres exists in services but has no deploy.yml
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityWarning, "no deploy.yml")
}

func TestDeployAfterValidator_DisabledService(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool", "postgres": "infra"},
		map[string]string{
			"redis":    "after:\n  - postgres\nphases: []\n",
			"postgres": "phases: []\n",
		},
		map[string]bool{"redis": true, "postgres": false}, // postgres disabled
		"", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityWarning, "disabled")
}

func TestDeployAfterValidator_CycleThreeServices(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"a": "tool", "b": "tool", "c": "tool"},
		map[string]string{
			"a": "after:\n  - c\nphases: []\n",
			"b": "after:\n  - a\nphases: []\n",
			"c": "after:\n  - b\nphases: []\n",
		},
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "cycle")
	// Verify the cycle path is embedded in the message.
	found := false
	for _, d := range diags {
		if d.Severity == validate.SeverityError && strings.Contains(d.Message, "→") {
			found = true
			break
		}
	}
	require.True(t, found, "expected cycle path with → in message")
}

func TestDeployAfterValidator_WellFormedAfterGraph(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"a": "tool", "b": "tool", "c": "tool"},
		map[string]string{
			"b": "after:\n  - a\nphases: []\n",
			"c": "after:\n  - b\nphases: []\n",
			"a": "phases: []\n",
		},
		nil, "", "", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireOK(t, diags, "config.deploy-after")
}

func TestDeployAfterValidator_ProjectDeployAfterError(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool"},
		nil, nil,
		// project-wide deploy.yml with after: field
		"after:\n  - redis\nphases: []\n",
		"", nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "project-wide deploy.yml")
}

func TestDeployAfterValidator_ProjectResetAfterError(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool"},
		nil, nil,
		"",
		// project-wide reset.yml with after: field
		"after:\n  - redis\nphases: []\n",
		nil,
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "reset.yml")
}

func TestDeployAfterValidator_ServiceResetAfterError(t *testing.T) {
	root, cfg := makeDeployAfterProject(t,
		map[string]string{"redis": "tool"},
		nil, nil, "", "",
		map[string]string{
			"redis": "after:\n  - redis\nphases: []\n",
		},
	)
	diags := runDeployAfter(t, root, cfg)
	requireSeverity(t, diags, validate.SeverityError, "reset.yml")
}

// --- helpers ---

// requireOK asserts at least one SeverityOK diagnostic with the given target,
// and no SeverityError diagnostics.
func requireOK(t *testing.T, diags []validate.Diagnostic, target string) {
	t.Helper()
	for _, d := range diags {
		require.NotEqual(t, validate.SeverityError, d.Severity,
			"unexpected error: %s", d.Message)
	}
	found := false
	for _, d := range diags {
		if d.Severity == validate.SeverityOK && d.Target == target {
			found = true
			break
		}
	}
	require.True(t, found, "expected OK diagnostic for target %q", target)
}

// requireSeverity asserts at least one diagnostic of the given severity whose
// message contains msgSubstr.
func requireSeverity(t *testing.T, diags []validate.Diagnostic, sev validate.Severity, msgSubstr string) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == sev && strings.Contains(d.Message, msgSubstr) {
			return
		}
	}
	t.Fatalf("expected %v diagnostic containing %q; got:\n%s", sev, msgSubstr, formatDiags(diags))
}

func formatDiags(diags []validate.Diagnostic) string {
	var sb strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&sb, "  [%v] %s\n", d.Severity, d.Message)
	}
	return sb.String()
}
