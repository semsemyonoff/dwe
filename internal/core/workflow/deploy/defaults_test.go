package deploy_test

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
)

func TestDefaultDeployConfig_Shape(t *testing.T) {
	cfg := deploy.DefaultDeployConfig()
	if cfg == nil {
		t.Fatal("DefaultDeployConfig returned nil")
	}

	wantPhases := []struct {
		name           string
		deployServices bool
		untracked      bool
		stepNames      []string
		stepTypes      []string
		stepCmds       []string
		stepUntracked  []bool
	}{
		{
			name:           "services",
			deployServices: true,
		},
		{
			name:          "start",
			stepNames:     []string{"up"},
			stepTypes:     []string{"devbox"},
			stepCmds:      []string{"docker up --wait"},
			stepUntracked: []bool{true},
		},
		{
			name:      "post-deploy",
			untracked: true,
			stepNames: []string{"info", "success"},
			stepTypes: []string{"devbox", "builtin"},
			stepCmds:  []string{"info", "message"},
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
		if ph.DeployServices != wp.deployServices {
			t.Errorf("phase[%d].DeployServices = %v, want %v", i, ph.DeployServices, wp.deployServices)
		}
		if ph.Untracked != wp.untracked {
			t.Errorf("phase[%d].Untracked = %v, want %v", i, ph.Untracked, wp.untracked)
		}
		if len(wp.stepNames) == 0 {
			continue
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
			if j < len(wp.stepUntracked) && s.Untracked != wp.stepUntracked[j] {
				t.Errorf("phase[%d].steps[%d].Untracked = %v, want %v", i, j, s.Untracked, wp.stepUntracked[j])
			}
		}
	}
}

func TestDefaultDeployConfig_FreshAllocation(t *testing.T) {
	a := deploy.DefaultDeployConfig()
	b := deploy.DefaultDeployConfig()
	// Mutating one must not affect the other.
	a.Phases = append(a.Phases, config.DeployPhase{Name: "extra"})
	if len(b.Phases) == len(a.Phases) {
		t.Error("DefaultDeployConfig returned shared slice: mutating one affected the other")
	}
}

func TestEnsureDeployConfig(t *testing.T) {
	userPhases := []config.DeployPhase{{Name: "custom", Steps: []config.DeployStep{{Name: "step1", Type: "shell", Cmd: "echo hi"}}}}

	tests := []struct {
		name          string
		input         *config.ProjectDeployConfig
		wantDefaulted bool
		wantPhases    string // first phase name expected
	}{
		{
			name:          "nil input returns default",
			input:         nil,
			wantDefaulted: true,
			wantPhases:    "services",
		},
		{
			name:          "empty phases returns default",
			input:         &config.ProjectDeployConfig{Phases: []config.DeployPhase{}},
			wantDefaulted: true,
			wantPhases:    "services",
		},
		{
			name:          "populated input returned unchanged",
			input:         &config.ProjectDeployConfig{Phases: userPhases},
			wantDefaulted: false,
			wantPhases:    "custom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, defaulted := deploy.EnsureDeployConfig(tc.input)
			if defaulted != tc.wantDefaulted {
				t.Errorf("defaulted = %v, want %v", defaulted, tc.wantDefaulted)
			}
			if got == nil {
				t.Fatal("EnsureDeployConfig returned nil")
			}
			if len(got.Phases) == 0 {
				t.Fatal("EnsureDeployConfig returned empty phases")
			}
			if got.Phases[0].Name != tc.wantPhases {
				t.Errorf("first phase = %q, want %q", got.Phases[0].Name, tc.wantPhases)
			}
			// Populated input must be the exact same pointer.
			if !tc.wantDefaulted && got != tc.input {
				t.Error("EnsureDeployConfig should return input unchanged when populated")
			}
		})
	}
}
