package pipeline

import (
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/filesgate"
)

func TestStepBadge_shellStep(t *testing.T) {
	s := config.DeployStep{Type: "shell", Cmd: "echo hello"}
	if got := stepBadge(s); got != "[shell]" {
		t.Errorf("got %q, want [shell]", got)
	}
}

func TestStepBadge_commandStep(t *testing.T) {
	s := config.DeployStep{Type: "command", Cmd: "services.main.migrate"}
	if got := stepBadge(s); got != "[command]" {
		t.Errorf("got %q, want [command]", got)
	}
}

func TestStepBadge_allTypes(t *testing.T) {
	if got := stepBadge(config.DeployStep{Type: "shell", Cmd: "x"}); got != "[shell]" {
		t.Errorf("got %q want [shell]", got)
	}
	if got := stepBadge(config.DeployStep{Type: "command", Cmd: "x"}); got != "[command]" {
		t.Errorf("got %q want [command]", got)
	}
	if got := stepBadge(config.DeployStep{Type: "devbox", Cmd: "docker down"}); got != "[devbox]" {
		t.Errorf("got %q want [devbox]", got)
	}
	if got := stepBadge(config.DeployStep{Type: "builtin", Cmd: "service_configs_copy"}); got != "[builtin]" {
		t.Errorf("got %q want [builtin]", got)
	}
}

func TestResolvePhaseSteps_filesGateThreadedThrough(t *testing.T) {
	cfg := &config.DevboxConfig{
		SchemaVersion: "2",
	}

	fg := &filesgate.FilesGate{
		State: filesgate.StateReadable,
	}

	phase := config.DeployPhase{
		Name: "setup",
		Steps: []config.DeployStep{
			{
				Name:      "test-step",
				Type:      "shell",
				Cmd:       "echo test",
				FilesGate: fg,
			},
		},
	}

	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("ResolvePhaseSteps: %v", err)
	}

	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved step, got %d", len(resolved))
	}

	if resolved[0].FilesGate != fg {
		t.Errorf("FilesGate not threaded through: expected %v, got %v", fg, resolved[0].FilesGate)
	}
	if resolved[0].FilesGate.State != filesgate.StateReadable {
		t.Errorf("FilesGate.State = %q, want readable", resolved[0].FilesGate.State)
	}
}
