package reset_test

import (
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/workflow/reset"
)

func TestDefaultResetConfig_Shape(t *testing.T) {
	cfg := reset.DefaultResetConfig()
	if cfg == nil {
		t.Fatal("DefaultResetConfig returned nil")
	}

	wantPhases := []struct {
		name      string
		untracked bool
		stepNames []string
		stepTypes []string
		stepCmds  []string
	}{
		{
			name:      "pre",
			untracked: true,
			stepNames: []string{"confirm"},
			stepTypes: []string{"builtin"},
			stepCmds:  []string{"confirm"},
		},
		{
			name:      "stop",
			stepNames: []string{"down"},
			stepTypes: []string{"devbox"},
			stepCmds:  []string{"docker down"},
		},
		{
			name:      "cleanup",
			stepNames: []string{"remove-volumes", "remove-services"},
			stepTypes: []string{"builtin", "builtin"},
			stepCmds:  []string{"docker_remove_project_volumes", "remove_paths"},
		},
	}

	if len(cfg.Phases) != len(wantPhases) {
		t.Fatalf("phase count = %d, want %d", len(cfg.Phases), len(wantPhases))
	}

	for i, wp := range wantPhases {
		ph := cfg.Phases[i]
		if ph.Name != wp.name {
			t.Errorf("phase[%d].Name = %q, want %q", i, ph.Name, wp.name)
		}
		if ph.Untracked != wp.untracked {
			t.Errorf("phase[%d].Untracked = %v, want %v", i, ph.Untracked, wp.untracked)
		}
		if len(ph.Steps) != len(wp.stepNames) {
			t.Errorf("phase[%d] step count = %d, want %d", i, len(ph.Steps), len(wp.stepNames))
			continue
		}
		for j, sn := range wp.stepNames {
			s := ph.Steps[j]
			if s.Name != sn {
				t.Errorf("phase[%d].steps[%d].Name = %q, want %q", i, j, s.Name, sn)
			}
			if j < len(wp.stepTypes) && s.Type != wp.stepTypes[j] {
				t.Errorf("phase[%d].steps[%d].Type = %q, want %q", i, j, s.Type, wp.stepTypes[j])
			}
			if j < len(wp.stepCmds) && s.Cmd != wp.stepCmds[j] {
				t.Errorf("phase[%d].steps[%d].Cmd = %q, want %q", i, j, s.Cmd, wp.stepCmds[j])
			}
		}
	}

	// Confirm step must have a message in With
	confirmStep := cfg.Phases[0].Steps[0]
	if confirmStep.With == nil {
		t.Error("confirm step has nil With map")
	} else if _, ok := confirmStep.With["message"]; !ok {
		t.Error("confirm step missing 'message' key in With")
	}

	// remove-services step must have paths in With
	removeSvcsStep := cfg.Phases[2].Steps[1]
	if removeSvcsStep.With == nil {
		t.Error("remove-services step has nil With map")
	} else if paths, ok := removeSvcsStep.With["paths"]; !ok {
		t.Error("remove-services step missing 'paths' key in With")
	} else if pathSlice, ok := paths.([]any); !ok || len(pathSlice) == 0 {
		t.Error("remove-services step 'paths' must be a non-empty slice")
	}
}

func TestDefaultResetConfig_FreshAllocation(t *testing.T) {
	a := reset.DefaultResetConfig()
	b := reset.DefaultResetConfig()
	a.Phases = append(a.Phases, config.DeployPhase{Name: "extra"})
	if len(b.Phases) == len(a.Phases) {
		t.Error("DefaultResetConfig returned shared slice: mutating one affected the other")
	}
}

func TestEnsureResetConfig(t *testing.T) {
	userPhases := []config.DeployPhase{{Name: "custom", Steps: []config.DeployStep{{Name: "step1", Type: "shell", Cmd: "echo hi"}}}}

	tests := []struct {
		name           string
		input          *config.ProjectDeployConfig
		wantDefaulted  bool
		wantFirstPhase string
	}{
		{
			name:           "nil input returns default",
			input:          nil,
			wantDefaulted:  true,
			wantFirstPhase: "pre",
		},
		{
			name:           "empty phases returns default",
			input:          &config.ProjectDeployConfig{Phases: []config.DeployPhase{}},
			wantDefaulted:  true,
			wantFirstPhase: "pre",
		},
		{
			name:           "populated input returned unchanged",
			input:          &config.ProjectDeployConfig{Phases: userPhases},
			wantDefaulted:  false,
			wantFirstPhase: "custom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, defaulted := reset.EnsureResetConfig(tc.input)
			if defaulted != tc.wantDefaulted {
				t.Errorf("defaulted = %v, want %v", defaulted, tc.wantDefaulted)
			}
			if got == nil {
				t.Fatal("EnsureResetConfig returned nil")
			}
			if len(got.Phases) == 0 {
				t.Fatal("EnsureResetConfig returned empty phases")
			}
			if got.Phases[0].Name != tc.wantFirstPhase {
				t.Errorf("first phase = %q, want %q", got.Phases[0].Name, tc.wantFirstPhase)
			}
			if !tc.wantDefaulted && got != tc.input {
				t.Error("EnsureResetConfig should return input unchanged when populated")
			}
		})
	}
}
