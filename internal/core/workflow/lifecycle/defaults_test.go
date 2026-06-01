package lifecycle

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// --- DefaultRunConfig shape tests ---

func TestDefaultRunConfig_Shape(t *testing.T) {
	cfg := DefaultRunConfig()
	if cfg == nil {
		t.Fatal("DefaultRunConfig() returned nil")
	}

	// update.mode: off
	if cfg.Update == nil {
		t.Fatal("Update must be non-nil")
	}
	if cfg.Update.Mode != "off" {
		t.Errorf("Update.Mode = %q, want %q", cfg.Update.Mode, "off")
	}

	// show_info: true
	if !cfg.ShowInfo {
		t.Error("ShowInfo must be true")
	}

	// final_message
	if cfg.FinalMessage != "Project is ready for work!" {
		t.Errorf("FinalMessage = %q, want %q", cfg.FinalMessage, "Project is ready for work!")
	}

	// single phase named "start"
	if len(cfg.Phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(cfg.Phases))
	}
	phase := cfg.Phases[0]
	if phase.Name != "start" {
		t.Errorf("phase Name = %q, want %q", phase.Name, "start")
	}
	if phase.Description != "Start containers and wait for health" {
		t.Errorf("phase Description = %q", phase.Description)
	}

	// single step in the phase
	if len(phase.Steps) != 1 {
		t.Fatalf("expected 1 step in start phase, got %d", len(phase.Steps))
	}
	step := phase.Steps[0]
	if step.Name != "up" {
		t.Errorf("step Name = %q, want %q", step.Name, "up")
	}
	if step.Type != "devbox" {
		t.Errorf("step Type = %q, want %q", step.Type, "devbox")
	}
	if step.Cmd != "docker up --wait" {
		t.Errorf("step Cmd = %q, want %q", step.Cmd, "docker up --wait")
	}
	if step.Description != "Start all containers and wait until healthy" {
		t.Errorf("step Description = %q", step.Description)
	}
}

func TestDefaultRunConfig_ReturnsFreshAlloc(t *testing.T) {
	a := DefaultRunConfig()
	b := DefaultRunConfig()
	if a == b {
		t.Error("DefaultRunConfig() must return a new allocation each call")
	}
	a.FinalMessage = "mutated"
	if b.FinalMessage == "mutated" {
		t.Error("mutating one result must not affect another")
	}
}

// --- EnsureRunConfig tests ---

func TestEnsureRunConfig(t *testing.T) {
	populated := &config.LifecycleConfig{
		Run: &config.LifecycleRunConfig{
			FinalMessage: "custom",
			Phases: []config.DeployPhase{
				{Name: "custom-phase"},
			},
		},
	}

	tests := []struct {
		name      string
		input     *config.LifecycleConfig
		wantDef   bool
		wantPhase string
	}{
		{
			name:      "nil input returns default",
			input:     nil,
			wantDef:   true,
			wantPhase: "start",
		},
		{
			name:      "LifecycleConfig with nil Run returns default",
			input:     &config.LifecycleConfig{Run: nil, Stop: &config.LifecycleStopConfig{}},
			wantDef:   true,
			wantPhase: "start",
		},
		{
			name:      "populated Run section returned verbatim",
			input:     populated,
			wantDef:   false,
			wantPhase: "custom-phase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, defaulted := EnsureRunConfig(tt.input)
			if got == nil {
				t.Fatal("EnsureRunConfig returned nil cfg")
			}
			if defaulted != tt.wantDef {
				t.Errorf("defaulted = %v, want %v", defaulted, tt.wantDef)
			}
			if len(got.Phases) == 0 {
				t.Fatal("result must have at least one phase")
			}
			if got.Phases[0].Name != tt.wantPhase {
				t.Errorf("first phase name = %q, want %q", got.Phases[0].Name, tt.wantPhase)
			}
		})
	}
}
