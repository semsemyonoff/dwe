package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/validate"
)

func TestDevboxValidator(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantDiags     int
		wantSchema    validate.Severity
		wantDevbox    validate.Severity
		wantSchemaMsg string
	}{
		{
			name:          "missing schema",
			fixture:       "devbox-missing-schema",
			wantDiags:     2,
			wantSchema:    validate.SeverityError,
			wantDevbox:    validate.SeverityOK, // LoadConfig succeeds without schema validation
			wantSchemaMsg: "schema_version",
		},
		{
			name:          "legacy schema",
			fixture:       "devbox-legacy-schema",
			wantDiags:     2,
			wantSchema:    validate.SeverityError,
			wantDevbox:    validate.SeverityOK, // LoadConfig succeeds without schema validation
			wantSchemaMsg: "schema_version",
		},
		{
			name:       "bad keys",
			fixture:    "devbox-v2-bad-keys",
			wantDiags:  2,
			wantSchema: validate.SeverityOK,
			wantDevbox: validate.SeverityError,
		},
		{
			name:       "good config",
			fixture:    "devbox-v2-good",
			wantDiags:  2,
			wantSchema: validate.SeverityOK,
			wantDevbox: validate.SeverityOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", tt.fixture)
			ctx := validate.Context{
				ProjectRoot: fixturePath,
				ConfigPath:  filepath.Join(fixturePath, "devbox.yml"),
			}

			v := &devboxValidator{}
			diags := v.Run(ctx)

			t.Logf("fixture=%s: got %d diags", tt.fixture, len(diags))
			for i, d := range diags {
				t.Logf("  [%d] severity=%v target=%s file=%s message=%s", i, d.Severity, d.Target, d.File, d.Message)
			}

			require.Equal(t, tt.wantDiags, len(diags), "diagnostic count mismatch")

			if len(diags) > 0 {
				// First diagnostic should be schema check
				require.Equal(t, tt.wantSchema, diags[0].Severity)
				require.Equal(t, "config.devbox.schema", diags[0].Target)
			}

			if len(diags) > 1 {
				// Second diagnostic should be devbox load
				require.Equal(t, tt.wantDevbox, diags[1].Severity)
				require.Equal(t, "config.devbox", diags[1].Target)
			}

			if tt.wantSchemaMsg != "" && len(diags) > 0 {
				require.Contains(t, diags[0].Message, tt.wantSchemaMsg)
			}
		})
	}
}

func TestDevboxValidatorID(t *testing.T) {
	v := &devboxValidator{}
	require.Equal(t, "devbox", v.ID())
	require.Equal(t, "config", v.Domain())
}

// writeServicesFile sets up a project root with devbox/services.yml for
// servicesValidator tests. Returns the project root path.
func writeServicesFile(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "devbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "devbox", "services.yml"), []byte(body), 0o644))
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, `field "dir" not allowed`)
	hasDiag(t, diags, validate.SeverityError, `field "extends" not allowed`)
	hasDiag(t, diags, validate.SeverityError, "extends only permitted for type app")
}

func TestServicesValidator_InfraExtendsRejected(t *testing.T) {
	body := `
services:
  db:
    type: infra
    container: pg
    extends: other
`
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	d := hasDiag(t, diags, validate.SeverityWarning, "no dir or dir_internal")
	require.Equal(t, "config.services:api", d.Target)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
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
	root := writeServicesFile(t, body)
	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	hasDiag(t, diags, validate.SeverityError, "hosts must be a map")
}

func TestServicesValidator_InterfaceCompileCheck(t *testing.T) {
	// Compile-time enforcement lives in devbox.go (`var _ validate.Validator = ...`).
	// This runtime smoke test exercises the ID()/Domain() pair as a second layer.
	v := &servicesValidator{}
	require.Equal(t, "services", v.ID())
	require.Equal(t, "config", v.Domain())
}
