package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// baseCfg returns a minimal but valid *config.DweConfig with one known
// service ("web"), suitable as ctx.Cfg for every test in this file.
func baseCfg() *config.DweConfig {
	return &config.DweConfig{
		Raw: map[string]any{},
		Services: map[string]config.ServiceConfig{
			"web": {},
		},
	}
}

// writeScenario writes body as workspace/tests/<name>.yml under root, creating
// the directory as needed, and returns root unchanged for chaining.
func writeScenario(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, "workspace", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write scenario %s: %v", name, err)
	}
	return root
}

// runFor runs the scenariosValidator against root/cfg with no command registry.
func runFor(root string, cfg *config.DweConfig) []validate.Diagnostic {
	v := &scenariosValidator{}
	return v.Run(validate.Context{ProjectRoot: root, Cfg: cfg})
}

// runForWithRegistry runs the scenariosValidator with an explicit command registry.
func runForWithRegistry(root string, cfg *config.DweConfig, reg *registry.Registry) []validate.Diagnostic {
	v := &scenariosValidator{}
	return v.Run(validate.Context{ProjectRoot: root, Cfg: cfg, CommandRegistry: reg})
}

func errorDiags(diags []validate.Diagnostic) []validate.Diagnostic {
	var out []validate.Diagnostic
	for _, d := range diags {
		if d.Severity == validate.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

func warningDiags(diags []validate.Diagnostic) []validate.Diagnostic {
	var out []validate.Diagnostic
	for _, d := range diags {
		if d.Severity == validate.SeverityWarning {
			out = append(out, d)
		}
	}
	return out
}

func TestScenariosValidator_IDAndDomain(t *testing.T) {
	v := &scenariosValidator{}
	if v.ID() != "scenarios" {
		t.Errorf("ID() = %q, want %q", v.ID(), "scenarios")
	}
	if v.Domain() != "tests" {
		t.Errorf("Domain() = %q, want %q", v.Domain(), "tests")
	}
}

func TestScenariosValidator_NilCfg(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "broken.yml", "not: [valid")
	diags := runFor(root, nil)
	if len(diags) != 0 {
		t.Fatalf("nil cfg: want no diagnostics, got %+v", diags)
	}
}

func TestScenariosValidator_NoTestsDir(t *testing.T) {
	diags := runFor(t.TempDir(), baseCfg())
	if len(diags) != 0 {
		t.Fatalf("absent tests dir: want no diagnostics, got %+v", diags)
	}
}

func TestScenariosValidator_ValidScenario(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "smoke.yml", `
description: a valid scenario
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	diags := runFor(root, baseCfg())
	if len(diags) != 0 {
		t.Fatalf("valid scenario: want no diagnostics, got %+v", diags)
	}
}

func TestScenariosValidator_BadName(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "Bad-Name!.yml", `
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("bad name: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "invalid scenario name") {
		t.Errorf("message = %q, want to mention invalid scenario name", diags[0].Message)
	}
}

func TestScenariosValidator_UnparseableTimeout(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badtimeout.yml", `
timeout: not-a-duration
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("bad timeout: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "invalid timeout") {
		t.Errorf("message = %q, want to mention invalid timeout", diags[0].Message)
	}
}

// TestScenariosValidator_NonPositiveTimeout pins parity with the runtime's
// resolveScenarioTimeout: a parseable but non-positive scenario timeout (which
// would fail the run at resolve time) is caught statically here too.
func TestScenariosValidator_NonPositiveTimeout(t *testing.T) {
	for _, raw := range []string{"-5m", "0"} {
		t.Run(raw, func(t *testing.T) {
			root := writeScenario(t, t.TempDir(), "badtimeout.yml", `
timeout: `+raw+`
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
			diags := errorDiags(runFor(root, baseCfg()))
			if len(diags) != 1 {
				t.Fatalf("timeout %q: want 1 error diagnostic, got %+v", raw, diags)
			}
			if !strings.Contains(diags[0].Message, "must be positive") {
				t.Errorf("message = %q, want to mention must be positive", diags[0].Message)
			}
		})
	}
}

func TestScenariosValidator_UnknownServiceReference(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badsvc.yml", `
env:
  services:
    enable: [ghost]
    disable: [also-ghost]
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 2 {
		t.Fatalf("unknown services: want 2 error diagnostics, got %+v", diags)
	}
	for _, d := range diags {
		if !strings.Contains(d.Message, "unknown service") {
			t.Errorf("message = %q, want to mention unknown service", d.Message)
		}
	}
}

func TestScenariosValidator_ServiceEnableReachesWhen(t *testing.T) {
	// A step gated on web being enabled, with an unresolvable builtin body.
	// baseCfg's web is disabled by default; the scenario force-enables it, so
	// the when: must fire and the bad builtin surface as a resolve error —
	// proving env.services.enable reaches the throwaway config's when: eval
	// (runtime resolves against the copy's toggled local.yml, so validate must
	// too, else this genuine error is missed).
	root := writeScenario(t, t.TempDir(), "toggle-on.yml", `
env:
  services:
    enable: [web]
steps:
  - name: gated
    type: builtin
    cmd: definitely_not_a_builtin
    when:
      type: template
      expr: '{{ (index .Services "web").Enabled }}'
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("enabled-gated bad step: want 1 resolve error, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "resolving steps") {
		t.Errorf("message = %q, want to mention resolving steps", diags[0].Message)
	}
}

func TestScenariosValidator_ServiceDisableReachesWhen(t *testing.T) {
	// Mirror of the enable case: a step gated on web being enabled with a bad
	// builtin body, but web is enabled in the project and the scenario disables
	// it. The when: must evaluate false and filter the step out, so no resolve
	// error surfaces — proving env.services.disable reaches the when: eval and
	// validate does not false-positive on a step the runtime would skip.
	cfg := &config.DweConfig{
		Raw:      map[string]any{},
		Services: map[string]config.ServiceConfig{"web": {Enabled: true}},
	}
	root := writeScenario(t, t.TempDir(), "toggle-off.yml", `
env:
  services:
    disable: [web]
steps:
  - name: gated
    type: builtin
    cmd: definitely_not_a_builtin
    when:
      type: template
      expr: '{{ (index .Services "web").Enabled }}'
`)
	if diags := errorDiags(runFor(root, cfg)); len(diags) != 0 {
		t.Fatalf("disabled-gated step should be filtered (no error), got %+v", diags)
	}
}

func TestScenariosValidator_UnknownCommandRef(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badcmd.yml", `
steps:
  - name: run-it
    type: command
    cmd: does.not.exist
`)
	reg := registry.NewEmptyRegistry()
	diags := errorDiags(runForWithRegistry(root, baseCfg(), reg))
	if len(diags) != 1 {
		t.Fatalf("unknown command: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "unknown command") {
		t.Errorf("message = %q, want to mention unknown command", diags[0].Message)
	}
}

func TestScenariosValidator_UnknownCommandRefInParallelSubstep(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badparallel.yml", `
steps:
  - name: fan-out
    parallel:
      steps:
        - name: run-it
          type: command
          cmd: does.not.exist
        - name: also
          type: shell
          cmd: echo hi
`)
	reg := registry.NewEmptyRegistry()
	diags := errorDiags(runForWithRegistry(root, baseCfg(), reg))
	if len(diags) != 1 {
		t.Fatalf("unknown command in parallel substep: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "unknown command") {
		t.Errorf("message = %q, want to mention unknown command", diags[0].Message)
	}
}

func TestScenariosValidator_KnownCommandRef(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "goodcmd.yml", `
steps:
  - name: run-it
    type: command
    cmd: queue.logs
`)
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "queue.logs", Type: model.CommandTypeBuiltin, Cmd: "docker_daemon_logs",
	})
	diags := runForWithRegistry(root, baseCfg(), reg)
	if len(diags) != 0 {
		t.Fatalf("known command: want no diagnostics, got %+v", diags)
	}
}

func TestScenariosValidator_NoRegistrySkipsCommandCheck(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "nocmdcheck.yml", `
steps:
  - name: run-it
    type: command
    cmd: does.not.exist
`)
	// No CommandRegistry in ctx at all (nil, not even a *registry.Registry) -
	// the command-ref check must self-skip rather than panic or misreport.
	diags := runFor(root, baseCfg())
	if len(diags) != 0 {
		t.Fatalf("nil registry: want no diagnostics (self-skip), got %+v", diags)
	}
}

func TestScenariosValidator_ConcreteInvalidBuiltinParam(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badbuiltin.yml", `
steps:
  - name: check
    type: builtin
    cmd: tcp_reachable
    with:
      host: localhost
      port: not-a-number
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("bad builtin param: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "resolving steps") {
		t.Errorf("message = %q, want to mention resolving steps", diags[0].Message)
	}
}

func TestScenariosValidator_AutoPortPlaceholderRenders(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "autoport.yml", `
env:
  vars:
    db.port: auto
steps:
  - name: check
    type: builtin
    cmd: tcp_reachable
    with:
      host: localhost
      port: "${vars.db.port}"
`)
	diags := runFor(root, baseCfg())
	if len(diags) != 0 {
		t.Fatalf("auto-var placeholder: want no diagnostics, got %+v", diags)
	}
}

func TestScenariosValidator_ConcreteBadParamNotMaskedByTemplatedField(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "maskcheck.yml", `
env:
  vars:
    base_url: http://example.com
steps:
  - name: check
    type: builtin
    cmd: http_check
    with:
      url: "${vars.base_url}"
      status: nope
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("concrete bad param: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "resolving steps") {
		t.Errorf("message = %q, want to mention resolving steps", diags[0].Message)
	}
}

func TestScenariosValidator_DuplicateStepNames(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "dupe.yml", `
steps:
  - name: same
    type: shell
    cmd: echo one
  - name: same
    type: shell
    cmd: echo two
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("duplicate step names: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "resolving steps") {
		t.Errorf("message = %q, want to mention resolving steps", diags[0].Message)
	}
}

func TestScenariosValidator_RenderErrorAbortsRemainingChecks(t *testing.T) {
	// A ${snapshot.*} reference is scope-rejected at RENDER time (scenarios have
	// no snapshot scope), which is a distinct diagnostic ("rendering steps") from
	// the resolve-time path — and it must early-return, so no later check runs.
	root := writeScenario(t, t.TempDir(), "renderfail.yml", `
steps:
  - name: bad-render
    type: shell
    cmd: echo ${snapshot.foo}
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("render error: want exactly 1 error diagnostic (abort after render), got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "rendering steps") {
		t.Errorf("message = %q, want to mention rendering steps", diags[0].Message)
	}
}

func TestScenariosValidator_BrokenWhen(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "badwhen.yml", `
steps:
  - name: maybe
    type: shell
    cmd: echo hi
    when:
      type: template
      expr: "{{ if }}"
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("broken when: want 1 error diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "resolving steps") {
		t.Errorf("message = %q, want to mention resolving steps", diags[0].Message)
	}
}

func TestScenariosValidator_MalformedScenarioFile(t *testing.T) {
	root := writeScenario(t, t.TempDir(), "malformed.yml", "")
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("empty scenario file: want 1 error diagnostic, got %+v", diags)
	}
}

func TestScenariosValidator_IsolationWarning(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "smoke.yml", `
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	composePath := filepath.Join(root, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(`
services:
  web:
    image: busybox
    container_name: fixed-name
`), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	cfg := baseCfg()
	cfg.Compose.Base = composePath

	diags := warningDiags(runFor(root, cfg))
	if len(diags) != 1 {
		t.Fatalf("isolation hazard: want 1 warning diagnostic, got %+v", diags)
	}
	if diags[0].Domain != "tests" || diags[0].Target != "tests.isolation" {
		t.Errorf("unexpected diagnostic shape: %+v", diags[0])
	}
}

func TestScenariosValidator_MultipleFilesSorted(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "b.yml", `
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	writeScenario(t, root, "a.yml", `
timeout: bad
steps:
  - name: ping
    type: shell
    cmd: echo hi
`)
	diags := errorDiags(runFor(root, baseCfg()))
	if len(diags) != 1 {
		t.Fatalf("want exactly 1 error diagnostic from a.yml, got %+v", diags)
	}
	if diags[0].Target != "tests.a" {
		t.Errorf("Target = %q, want %q", diags[0].Target, "tests.a")
	}
}
