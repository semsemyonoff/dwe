package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/core/validate"
)

// runValidator writes the given command file content to a temporary
// devbox/commands directory and runs the Validator, returning the produced
// diagnostics.
func runValidator(t *testing.T, content string) []validate.Diagnostic {
	t.Helper()
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "devbox", "commands")
	require.NoError(t, os.MkdirAll(cmdDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cmdDir, "daemons.yml"), []byte(content), 0o644))
	v := &Validator{}
	return v.Run(validate.Context{ProjectRoot: dir})
}

// findDiag returns the first diagnostic whose Message contains substr, or
// nil if none. Helper for assertions.
func findDiag(diags []validate.Diagnostic, substr string) *validate.Diagnostic {
	for i := range diags {
		if strings.Contains(diags[i].Message, substr) {
			return &diags[i]
		}
	}
	return nil
}

// countContains returns the number of diagnostics whose Message contains substr.
func countContains(diags []validate.Diagnostic, substr string) int {
	n := 0
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			n++
		}
	}
	return n
}

func TestDaemonValidator_ServiceRequired(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    daemon:
      container_template: "q_${param.name}"
    params:
      name:
        default: default
        pattern: "^[a-z]+$"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "daemon: service required")
	require.NotNil(t, d, "expected service-required diagnostic; got: %#v", diags)
	require.Equal(t, validate.SeverityError, d.Severity)
	require.Equal(t, "commands:daemons.queue", d.Target)
	require.NotEmpty(t, d.Hint)
	require.Equal(t, 1, countContains(diags, "daemon: service required"), "diagnostic must not be double-emitted")
}

func TestDaemonValidator_ServiceNotLiteral(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app-${param.name}
    daemon:
      container_template: "q_${param.name}"
    params:
      name:
        default: default
        pattern: "^[a-z]+$"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "service must be literal")
	require.NotNil(t, d, "expected literal-service diagnostic; got: %#v", diags)
	require.Equal(t, validate.SeverityError, d.Severity)
	require.Equal(t, 1, countContains(diags, "service must be literal"))
}

func TestDaemonValidator_ContainerTemplateRequired(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: ""
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "container_template required")
	require.NotNil(t, d, "expected container_template diagnostic; got: %#v", diags)
	require.Equal(t, validate.SeverityError, d.Severity)
	require.Equal(t, 1, countContains(diags, "container_template required"))
}

func TestDaemonValidator_OnAlreadyRunningInvalid(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q"
      on_already_running: maybe
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "on_already_running")
	require.NotNil(t, d, "expected on_already_running diagnostic; got: %#v", diags)
	require.Contains(t, d.Message, `"maybe"`)
	require.Equal(t, 1, countContains(diags, "on_already_running"))
}

func TestDaemonValidator_StopTimeoutInvalid(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q"
      stop_timeout: "garbage"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "stop_timeout parse")
	require.NotNil(t, d, "expected stop_timeout parse diagnostic; got: %#v", diags)
	require.Equal(t, 1, countContains(diags, "stop_timeout"))
}

func TestDaemonValidator_StopTimeoutMustBePositive(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q"
      stop_timeout: "-5s"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "stop_timeout must be positive")
	require.NotNil(t, d, "expected positive-duration diagnostic; got: %#v", diags)
}

func TestDaemonValidator_StopTimeoutSubSecondAccepted(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q"
      stop_timeout: "500ms"
    argv: [echo]
`
	diags := runValidator(t, content)
	// No stop_timeout diagnostic should fire — sub-second values are valid.
	require.Nil(t, findDiag(diags, "stop_timeout"), "sub-second stop_timeout must be accepted; got: %#v", diags)
}

func TestDaemonValidator_MultiFieldNoDoubleEmission(t *testing.T) {
	// Daemon missing service: AND with stop_timeout: "garbage" — both must surface,
	// neither double-emitted via the model fallback.
	content := `commands:
  queue:
    type: daemon
    daemon:
      container_template: "q"
      stop_timeout: "garbage"
    argv: [echo]
`
	diags := runValidator(t, content)
	require.NotNil(t, findDiag(diags, "daemon: service required"))
	require.NotNil(t, findDiag(diags, "stop_timeout parse"))
	require.Equal(t, 1, countContains(diags, "daemon: service required"), "service must not be double-emitted")
	require.Equal(t, 1, countContains(diags, "stop_timeout parse"), "stop_timeout must not be double-emitted")
}

func TestDaemonValidator_MixedCategoryParamRef(t *testing.T) {
	// Valid service:, missing container_template:, AND a ${param.X} referencing
	// an undeclared param. Both diagnostics surface (categorised + validator-only).
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q_${param.missing}"
    argv: [echo]
`
	diags := runValidator(t, content)
	require.NotNil(t, findDiag(diags, "container_template references undeclared param"))
}

func TestDaemonValidator_ParamRefWithoutPatternWarns(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q_${param.name}"
    params:
      name:
        default: default
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, `references param "name" without a pattern`)
	require.NotNil(t, d, "expected pattern-advisory diagnostic; got: %#v", diags)
	require.Equal(t, validate.SeverityWarning, d.Severity)
}

func TestDaemonValidator_ServiceMissingAndBadOnAlreadyRunning(t *testing.T) {
	// Regression for the per-field suppression bug: a daemon missing service:
	// AND with invalid on_already_running must surface BOTH diagnostics.
	content := `commands:
  queue:
    type: daemon
    daemon:
      container_template: "q"
      on_already_running: bogus
    argv: [echo]
`
	diags := runValidator(t, content)
	require.NotNil(t, findDiag(diags, "daemon: service required"))
	require.NotNil(t, findDiag(diags, "on_already_running"))
	require.Equal(t, 1, countContains(diags, "daemon: service required"))
	require.Equal(t, 1, countContains(diags, "on_already_running"))
}

func TestDaemonValidator_HappyPath(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q_${param.name}"
      on_already_running: noop
      stop_timeout: "10s"
    params:
      name:
        default: default
        pattern: "^[a-z]+$"
    argv: [echo]
`
	diags := runValidator(t, content)
	for _, d := range diags {
		if d.Severity == validate.SeverityError {
			t.Fatalf("unexpected error diagnostic: %#v", d)
		}
	}
}

func TestDaemonValidator_FileFieldPopulated(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    daemon:
      container_template: "q"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "daemon: service required")
	require.NotNil(t, d)
	require.Equal(t, filepath.Join("devbox", "commands", "daemons.yml"), d.File)
}
