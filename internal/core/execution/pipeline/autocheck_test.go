package pipeline

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin"
	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
)

func autoCheckPhase(whenCmd string) config.DeployPhase {
	return config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name:  "clone",
				Type:  "shell",
				Cmd:   "git clone ${vars.source.repo} ${vars.source.dir}",
				When:  &condition.Condition{Type: condition.TypeShell, Cmd: whenCmd},
				Check: &config.Action{Type: config.AutoCheckType},
			},
		},
	}
}

// resolveAutoCheckStep resolves a single auto-check step and returns it.
func resolveAutoCheckStep(t *testing.T, whenCmd string) ResolvedStep {
	t.Helper()
	resolved, err := ResolvePhaseSteps(configWithSourceVars(), nil, autoCheckPhase(whenCmd), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d steps, want 1", len(resolved))
	}
	return resolved[0]
}

func TestResolvePhaseSteps_autoCheckDerivesBuiltinShell(t *testing.T) {
	rs := resolveAutoCheckStep(t, "[ ! -e ${vars.source.dir}/.git ]")

	if config.IsAutoCheck(rs.Step.Check) {
		t.Fatal("the sentinel survived resolve")
	}
	// The builtin form is the pinned one: it gets a hard `sh -c` (matching
	// condition.EvalCmd) rather than the project's overridable ShellBin.
	if rs.Step.Check.Type != "builtin" || rs.Step.Check.Cmd != "shell" {
		t.Fatalf("derived check = %+v, want {type: builtin, cmd: shell}", rs.Step.Check)
	}
	// Unbounded, like the when: it inverts.
	if got := rs.Step.Check.With["timeout"]; got != "0" {
		t.Errorf("derived check timeout = %v, want \"0\"", got)
	}
}

func TestResolvePhaseSteps_autoCheckInvertsTheRenderedWhenByteForByte(t *testing.T) {
	rs := resolveAutoCheckStep(t, "[ ! -e ${vars.source.dir}/.git ]")

	if rs.RuntimeWhen == nil {
		t.Fatal("expected a runtime when")
	}
	want := "! (\n" + rs.RuntimeWhen.Cmd + "\n)"
	if got := rs.Step.Check.With["cmd"]; got != want {
		t.Fatalf("derived check cmd = %q, want %q", got, want)
	}
	// And the string that got inverted is the rendered one, not the raw source.
	if strings.Contains(rs.RuntimeWhen.Cmd, "${") {
		t.Fatalf("when.Cmd was not rendered before inversion: %q", rs.RuntimeWhen.Cmd)
	}
}

func TestResolvePhaseSteps_autoCheckDoesNotMutateLoadedConfig(t *testing.T) {
	cfg := configWithSourceVars()
	phase := autoCheckPhase("test -e ${vars.source.dir}/.git")

	first, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if !config.IsAutoCheck(phase.Steps[0].Check) {
		t.Fatalf("loaded config was mutated: check = %+v", phase.Steps[0].Check)
	}
	second, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	// A double-wrapped "! ( ! ( ... ) )" would silently restore the original
	// logic, so resolving twice must produce identical text.
	if first[0].Step.Check.With["cmd"] != second[0].Step.Check.With["cmd"] {
		t.Fatalf("second resolve differs:\n first = %q\nsecond = %q",
			first[0].Step.Check.With["cmd"], second[0].Step.Check.With["cmd"])
	}
}

func TestResolvePhaseSteps_autoCheckInsideParallelGroup(t *testing.T) {
	cfg := configWithSourceVars()
	group := newParallelStep("group", 0, nil,
		config.DeployStep{
			Name: "a", Type: "shell", Cmd: "echo a",
			When:  &condition.Condition{Type: condition.TypeShell, Cmd: "test -e ${vars.source.dir}"},
			Check: &config.Action{Type: config.AutoCheckType},
		},
		config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
	)
	phase := config.DeployPhase{Name: "init", Steps: []config.DeployStep{group}}

	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub := resolved[0].Parallel.Steps[0]
	if sub.Step.Check == nil || sub.Step.Check.Cmd != "shell" {
		t.Fatalf("sub-step check = %+v, want the derived builtin shell", sub.Step.Check)
	}
	if got := sub.Step.Check.With["cmd"]; got != "! (\ntest -e app\n)" {
		t.Errorf("sub-step check cmd = %q", got)
	}
}

func TestResolvePhaseSteps_autoCheckWithoutRuntimeWhenIsResolveError(t *testing.T) {
	// Unreachable through the loader (validateAutoCheck rejects it), but the
	// resolver must not produce a half-built check for a hand-made step.
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "a", Type: "shell", Cmd: "echo a", Check: &config.Action{Type: config.AutoCheckType}},
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err == nil || !strings.Contains(err.Error(), "no when: to invert") {
		t.Fatalf("err = %v, want a 'no when: to invert' resolve error", err)
	}
}

func TestResolveAutoCheck_rejectsNonShellWhen(t *testing.T) {
	for _, c := range []*condition.Condition{
		{Type: condition.TypeBuiltin, Cmd: "dir-empty src"},
		{Type: condition.TypeTemplate, Expr: "{{ true }}"},
	} {
		if _, err := ResolveAutoCheck(c); err == nil {
			t.Errorf("%s: expected an error", c.Type)
		}
	}
}

// TestInvertShellCommand_semantics runs both the original condition and its
// inverse through the real shell builtin, pinning that the inversion is a
// logical negation and that the newline wrapping survives forms an inline
// `! ( ... )` would break on.
func TestInvertShellCommand_semantics(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	ectx := builtin.ExecContext{ProjectRoot: dir}

	cases := []struct {
		name     string
		when     string
		whenTrue bool
	}{
		{"true condition", "test -e marker", true},
		{"false condition", "test -e absent", false},
		// The naive inline wrap swallows the closing paren into the comment
		// and yields a syntax error rather than the inverse.
		{"trailing comment, true", "test -e marker  # already cloned?", true},
		{"trailing comment, false", "test -e absent  # already cloned?", false},
		{"multi-line", "test -e marker\ntest -e marker", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, err := ResolveAutoCheck(&condition.Condition{Type: condition.TypeShell, Cmd: tc.when})
			if err != nil {
				t.Fatalf("ResolveAutoCheck: %v", err)
			}
			// The derived check runs through the same registry entry a
			// hand-written `check: {type: builtin, cmd: shell}` would.
			err = builtin.Run(context.Background(), action.Cmd, action.With, ectx, builtin.CtxPredicate)
			inverseTrue := err == nil
			if inverseTrue == tc.whenTrue {
				t.Fatalf("inverse of a %v condition evaluated to %v (err=%v)", tc.whenTrue, inverseTrue, err)
			}
			if err != nil && strings.Contains(err.Error(), "syntax error") {
				t.Fatalf("wrapping produced a syntax error: %v", err)
			}
		})
	}
}

// TestResolveAutoCheck_cwdIsProjectRoot pins that the derived check resolves
// relative paths against the project root — the same base condition.EvalCmd
// uses for the `when:` it was derived from.
func TestResolveAutoCheck_cwdIsProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// The when: is true in the project root, so its inverse must be false there.
	whenCmd := "test -e marker"
	if ok, err := condition.EvalCmd(whenCmd, root); err != nil || !ok {
		t.Fatalf("when: precondition failed: ok=%v err=%v", ok, err)
	}
	action, err := ResolveAutoCheck(&condition.Condition{Type: condition.TypeShell, Cmd: whenCmd})
	if err != nil {
		t.Fatalf("ResolveAutoCheck: %v", err)
	}
	err = builtin.Run(context.Background(), action.Cmd, action.With, builtin.ExecContext{ProjectRoot: root}, builtin.CtxPredicate)
	if err == nil {
		t.Fatal("derived check passed; it did not run in the project root")
	}
}

// TestDisplayCheck_autoIsReportedAsAuthored pins that plan output names the
// directive the author wrote. Printing the derived `builtin: shell(cmd=! ( … ))`
// would send the reader looking for a check that appears nowhere in their
// pipeline file.
func TestDisplayCheck_autoIsReportedAsAuthored(t *testing.T) {
	rs := resolveAutoCheckStep(t, "test -e ${vars.source.dir}/.git")

	if got, want := rs.DisplayCheck(), "auto (inverse of when)"; got != want {
		t.Fatalf("DisplayCheck() = %q, want %q", got, want)
	}
}

func TestDisplayCheck_explicitAndAbsent(t *testing.T) {
	explicit := ResolvedStep{Step: config.DeployStep{
		Check: &config.Action{Type: "shell", Cmd: "test -e x"},
	}}
	if got, want := explicit.DisplayCheck(), FormatAction(explicit.Step.Check); got != want {
		t.Errorf("explicit check: DisplayCheck() = %q, want %q", got, want)
	}
	if got := (ResolvedStep{}).DisplayCheck(); got != "" {
		t.Errorf("no check: DisplayCheck() = %q, want empty", got)
	}
}

// TestPrintPlanTable_autoCheckLine pins the human plan line end to end.
func TestPrintPlanTable_autoCheckLine(t *testing.T) {
	rs := resolveAutoCheckStep(t, "test -e ${vars.source.dir}/.git")

	var buf bytes.Buffer
	PrintPlanTable([]ResolvedStep{rs}, sharedrender.NewWriter(&buf), "dwe")

	if !strings.Contains(buf.String(), "[check: auto (inverse of when)]") {
		t.Fatalf("plan output missing the auto-check line:\n%s", buf.String())
	}
}
