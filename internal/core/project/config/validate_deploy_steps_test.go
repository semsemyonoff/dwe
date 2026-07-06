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
