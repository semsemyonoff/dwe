package pipeline

import (
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// planSecret is unique to this package's fixtures: the redactor is
// process-global and union-only, so a value shared with another test would
// keep being redacted for the rest of the test binary's life. No test in this
// file runs in parallel for the same reason.
const planSecret = "pl4n-redact-secret-value"

// secretCfg carries the plaintext under vars.token, the shape resolve-time
// rendering substitutes into cmd, with:, check: and when:.
func secretCfg() *config.DweConfig {
	return &config.DweConfig{
		Raw: map[string]any{
			"vars": map[string]any{"token": planSecret},
		},
	}
}

func registerPlanSecret(t *testing.T) {
	t.Helper()
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)
	trace.RegisterRedaction([]string{planSecret})
}

// TestDisplayStringsRedactResolvedSecret resolves a phase whose cmd, with:
// (both a `type: command` map and a builtin confirm message carrying a quote,
// a backslash and a newline), check: and shell when: all reference the secret,
// and asserts every display string shows *** and never the plaintext.
func TestDisplayStringsRedactResolvedSecret(t *testing.T) {
	registerPlanSecret(t)

	phase := config.DeployPhase{
		Name: "deploy",
		Steps: []config.DeployStep{
			{
				Name: "echo",
				Type: "shell",
				Cmd:  "echo 'token is ${vars.token}'",
				When: &condition.Condition{Type: condition.TypeShell, Cmd: "test -n '${vars.token}'"},
			},
			{
				Name:  "migrate",
				Type:  "command",
				Cmd:   "services.main.migrate",
				With:  map[string]any{"token": "${vars.token}", "keep": true},
				Check: &config.Action{Type: "shell", Cmd: "grep -q '${vars.token}' /tmp/state"},
			},
			{
				Name: "ask",
				Type: "builtin",
				Cmd:  "confirm",
				// The quote, backslash and newline are what confirm's %q
				// formatting escapes: after Describe the plaintext is no longer
				// a substring, so redaction must happen on the leaf first.
				With: map[string]any{"message": "proceed with \"${vars.token}\"\nor \\ abort"},
			},
		},
	}

	resolved, err := ResolvePhaseSteps(secretCfg(), nil, phase, "")
	if err != nil {
		t.Fatalf("ResolvePhaseSteps: %v", err)
	}
	if len(resolved) != 3 {
		t.Fatalf("resolved %d steps, want 3", len(resolved))
	}

	for _, rs := range resolved {
		display := map[string]string{
			"StepCommand":  StepCommand(rs.Step, "dwe"),
			"DisplayCheck": rs.DisplayCheck(),
			"when":         FormatCondition(rs.RuntimeWhen),
		}
		for surface, got := range display {
			if strings.Contains(got, planSecret) {
				t.Errorf("step %s %s = %q, still carries the plaintext", rs.Step.Name, surface, got)
			}
		}
		if got := display["StepCommand"]; !strings.Contains(got, "***") {
			t.Errorf("step %s StepCommand = %q, want the *** placeholder", rs.Step.Name, got)
		}
	}

	if got := resolved[0].DisplayCheck(); got != "" {
		t.Errorf("step echo has no check, DisplayCheck = %q", got)
	}
	if got := FormatCondition(resolved[0].RuntimeWhen); !strings.Contains(got, "***") {
		t.Errorf("when = %q, want ***", got)
	}
	if got := resolved[1].DisplayCheck(); !strings.Contains(got, "***") {
		t.Errorf("check = %q, want ***", got)
	}
}

// TestStepCommandDoesNotMutateWith is the deep-copy guard: cli/lifecycle's
// `reset step` calls StepCommand before the --dry-run branch and then executes
// the same step, so an in-place redaction would run the real command with ***
// as every secret parameter.
func TestStepCommandDoesNotMutateWith(t *testing.T) {
	registerPlanSecret(t)

	step := config.DeployStep{
		Name: "migrate",
		Type: "command",
		Cmd:  "services.main.migrate " + planSecret,
		With: map[string]any{
			"token":  planSecret,
			"nested": map[string]any{"inner": planSecret},
			"list":   []any{planSecret, 7},
			"count":  3,
		},
	}
	want := map[string]any{
		"token":  planSecret,
		"nested": map[string]any{"inner": planSecret},
		"list":   []any{planSecret, 7},
		"count":  3,
	}

	got := StepCommand(step, "dwe")
	if strings.Contains(got, planSecret) {
		t.Fatalf("StepCommand = %q, still carries the plaintext", got)
	}
	if !reflect.DeepEqual(step.With, want) {
		t.Errorf("step.With = %#v after StepCommand, want it untouched: %#v", step.With, want)
	}
	if step.Cmd != "services.main.migrate "+planSecret {
		t.Errorf("step.Cmd = %q after StepCommand, want it untouched", step.Cmd)
	}
}

// TestRedactWithValueCoversEveryContainerShape pins the two `with:` shapes the
// deep copy handles but no plan surface exercises directly: a YAML-decoded
// map[any]any (what an untyped nested map decodes to outside the strict
// loaders) and a []string. A missed arm would copy the container by reference
// AND leak the plaintext.
func TestRedactWithValueCoversEveryContainerShape(t *testing.T) {
	registerPlanSecret(t)

	inner := map[any]any{"k": planSecret, 2: []string{planSecret, "public"}}
	with := map[string]any{"nested": inner}

	out := redactWithValues(with)

	got, ok := out["nested"].(map[any]any)
	if !ok {
		t.Fatalf("nested = %#v, want map[any]any", out["nested"])
	}
	if got["k"] != "***" {
		t.Errorf("nested[k] = %v, want ***", got["k"])
	}
	list, ok := got[2].([]string)
	if !ok {
		t.Fatalf("nested[2] = %#v, want []string", got[2])
	}
	if !reflect.DeepEqual(list, []string{"***", "public"}) {
		t.Errorf("nested[2] = %#v, want [*** public]", list)
	}
	// The caller's map must be untouched — same contract as the top-level copy.
	if !reflect.DeepEqual(inner, map[any]any{"k": planSecret, 2: []string{planSecret, "public"}}) {
		t.Errorf("the source map was mutated: %#v", inner)
	}
}

// TestStepCommandRedactsEveryStepType pins the redaction on all four branches
// plus the default one.
func TestStepCommandRedactsEveryStepType(t *testing.T) {
	registerPlanSecret(t)

	tests := []struct {
		name string
		step config.DeployStep
		want string
	}{
		{"shell", config.DeployStep{Type: "shell", Cmd: "echo " + planSecret}, "echo ***"},
		{"dwe", config.DeployStep{Type: "dwe", Cmd: "run " + planSecret}, "dwe run ***"},
		{
			"command",
			config.DeployStep{Type: "command", Cmd: "app.migrate", With: map[string]any{"token": planSecret}},
			"dwe commands run app.migrate --set token=***",
		},
		{"unknown type", config.DeployStep{Type: "custom", Cmd: planSecret}, "custom: ***"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StepCommand(tc.step, "dwe"); got != tc.want {
				t.Errorf("StepCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatActionAndConditionRedact covers the two executor.go display
// helpers directly, including the template condition branch (Expr, not Cmd).
func TestFormatActionAndConditionRedact(t *testing.T) {
	registerPlanSecret(t)

	if got, want := FormatAction(&config.Action{Type: "shell", Cmd: "grep " + planSecret}), "shell grep ***"; got != want {
		t.Errorf("FormatAction = %q, want %q", got, want)
	}
	if got, want := FormatCondition(&condition.Condition{Type: condition.TypeShell, Cmd: "test " + planSecret}), "shell test ***"; got != want {
		t.Errorf("FormatCondition(shell) = %q, want %q", got, want)
	}
	if got, want := FormatCondition(&condition.Condition{Type: condition.TypeBuiltin, Cmd: "file_exists " + planSecret}), "builtin file_exists ***"; got != want {
		t.Errorf("FormatCondition(builtin) = %q, want %q", got, want)
	}
	if got, want := FormatCondition(&condition.Condition{Type: condition.TypeTemplate, Expr: "eq .Vars " + planSecret}), "template eq .Vars ***"; got != want {
		t.Errorf("FormatCondition(template) = %q, want %q", got, want)
	}
}

// TestUnresolvedRefsOnRedactedDisplayString pins that the unresolved scan runs
// on the redacted string: a secret whose value itself contains ${...} must not
// resurface through the [unresolved: …] line.
func TestUnresolvedRefsOnRedactedDisplayString(t *testing.T) {
	const braced = "s3cr3t-${leak}-value"
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)
	trace.RegisterRedaction([]string{braced})

	cmd := StepCommand(config.DeployStep{Type: "shell", Cmd: "echo " + braced}, "dwe")
	if got, want := cmd, "echo ***"; got != want {
		t.Fatalf("StepCommand = %q, want %q", got, want)
	}
	if refs := UnresolvedTemplateRefs(cmd); len(refs) != 0 {
		t.Errorf("UnresolvedTemplateRefs = %v, want none — the secret's own ${…} leaked", refs)
	}
}

// TestShortSecretIsNotRedacted pins the documented limit
// (secrets.MinRedactRunes): a value under 4 runes is never redacted, on the
// plan surfaces as everywhere else.
func TestShortSecretIsNotRedacted(t *testing.T) {
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)
	trace.RegisterRedaction([]string{"abc"})

	if got, want := StepCommand(config.DeployStep{Type: "shell", Cmd: "echo abc"}, "dwe"), "echo abc"; got != want {
		t.Errorf("StepCommand = %q, want %q", got, want)
	}
	if got, want := FormatAction(&config.Action{Type: "shell", Cmd: "echo abc"}), "shell echo abc"; got != want {
		t.Errorf("FormatAction = %q, want %q", got, want)
	}
	if got, want := FormatCondition(&condition.Condition{Type: condition.TypeShell, Cmd: "test abc"}), "shell test abc"; got != want {
		t.Errorf("FormatCondition = %q, want %q", got, want)
	}
}

// TestDisplayStringsUnchangedWithoutRedaction guards the no-secrets baseline:
// with nothing registered every display string is byte-identical to what it
// was before redaction was introduced.
func TestDisplayStringsUnchangedWithoutRedaction(t *testing.T) {
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)

	step := config.DeployStep{Type: "command", Cmd: "app.migrate", With: map[string]any{"b": 2, "a": "x"}}
	if got, want := StepCommand(step, "dwe"), "dwe commands run app.migrate --set a=x --set b=2"; got != want {
		t.Errorf("StepCommand = %q, want %q", got, want)
	}
	if got, want := FormatAction(&config.Action{Type: "shell", Cmd: "test -f .env"}), "shell test -f .env"; got != want {
		t.Errorf("FormatAction = %q, want %q", got, want)
	}
}
