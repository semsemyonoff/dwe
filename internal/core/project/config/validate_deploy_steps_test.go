package config

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
)

func TestValidateDeploySteps_Valid(t *testing.T) {
	steps := []DeployStep{
		{Name: "a", Type: "shell", Cmd: "echo hi"},
		{Name: "b", Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "x"}},
		{Name: "grp", Parallel: &ParallelGroup{Steps: []DeployStep{
			{Name: "c", Type: "command", Cmd: "db:dump"},
		}}},
	}
	if err := ValidateDeploySteps(steps, "scenario demo"); err != nil {
		t.Fatalf("ValidateDeploySteps: %v", err)
	}
}

func TestValidateDeploySteps_Errors(t *testing.T) {
	cases := []struct {
		name    string
		steps   []DeployStep
		wantSub string
	}{
		{
			"missing type",
			[]DeployStep{{Name: "a", Cmd: "echo"}},
			"type is required",
		},
		{
			"missing cmd",
			[]DeployStep{{Name: "a", Type: "shell"}},
			"cmd is required",
		},
		{
			"unknown type",
			[]DeployStep{{Name: "a", Type: "bogus", Cmd: "x"}},
			"unknown type",
		},
		{
			"shell with with",
			[]DeployStep{{Name: "a", Type: "shell", Cmd: "x", With: map[string]any{"k": "v"}}},
			"does not accept with",
		},
		{
			"invalid when",
			[]DeployStep{{Name: "a", Type: "shell", Cmd: "x", When: &condition.Condition{Type: "bogus"}}},
			"unknown condition type",
		},
		{
			"invalid nested parallel step",
			[]DeployStep{{Name: "grp", Parallel: &ParallelGroup{Steps: []DeployStep{{Name: "bad"}}}}},
			"type is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeploySteps(tc.steps, "scenario demo")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
			if !strings.Contains(err.Error(), "scenario demo") {
				t.Errorf("error %q does not carry context", err.Error())
			}
		})
	}
}

// autoCheck returns a fresh `check: auto` sentinel action.
func autoCheck() *Action { return &Action{Type: AutoCheckType} }

func TestValidateDeploySteps_AutoCheck_Accepted(t *testing.T) {
	steps := []DeployStep{
		{
			Name:  "clone",
			Type:  "shell",
			Cmd:   "git clone x y",
			When:  &condition.Condition{Type: condition.TypeShell, Cmd: "[ ! -e y/.git ]"},
			Check: autoCheck(),
		},
		{Name: "grp", Parallel: &ParallelGroup{Steps: []DeployStep{
			{
				Name:  "sub",
				Type:  "shell",
				Cmd:   "echo hi",
				When:  &condition.Condition{Type: condition.TypeShell, Cmd: "test -e x"},
				Check: autoCheck(),
			},
		}}},
	}
	if err := ValidateDeploySteps(steps, "scenario demo"); err != nil {
		t.Fatalf("ValidateDeploySteps: %v", err)
	}
}

func TestValidateDeploySteps_AutoCheck_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		step    DeployStep
		wantSub string
	}{
		{
			"auto without when",
			DeployStep{Name: "a", Type: "shell", Cmd: "echo hi", Check: autoCheck()},
			"has no when: to invert",
		},
		{
			"auto with builtin when",
			DeployStep{
				Name:  "a",
				Type:  "shell",
				Cmd:   "echo hi",
				When:  &condition.Condition{Type: condition.TypeBuiltin, Cmd: "dir-empty src"},
				Check: autoCheck(),
			},
			"when: {type: builtin}",
		},
		{
			"auto with template when",
			DeployStep{
				Name:  "a",
				Type:  "shell",
				Cmd:   "echo hi",
				When:  &condition.Condition{Type: condition.TypeTemplate, Expr: "{{ .Services.main.Enabled }}"},
				Check: autoCheck(),
			},
			"when: {type: template}",
		},
		{
			"auto on a parallel sub-step without when",
			DeployStep{Name: "grp", Parallel: &ParallelGroup{Steps: []DeployStep{
				{Name: "sub", Type: "shell", Cmd: "echo hi", Check: autoCheck()},
			}}},
			"has no when: to invert",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeploySteps([]DeployStep{tc.step}, "scenario demo")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
			if !strings.Contains(err.Error(), "check: auto") {
				t.Errorf("error %q does not name check: auto", err.Error())
			}
		})
	}
}
