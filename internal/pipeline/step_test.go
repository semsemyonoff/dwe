package pipeline

import (
	"testing"

	"devbox-cli/internal/config"
)

func TestStepBadge_cmdStep(t *testing.T) {
	s := config.DeployStep{Run: "echo hello"}
	if got := stepBadge(s); got != "[run]" {
		t.Errorf("got %q, want [run]", got)
	}
}

func TestStepBadge_commandStep(t *testing.T) {
	s := config.DeployStep{Command: "services.main.migrate"}
	if got := stepBadge(s); got != "[command]" {
		t.Errorf("got %q, want [command]", got)
	}
}

func TestStepBadge_allTypes(t *testing.T) {
	if got := stepBadge(config.DeployStep{Run: "x"}); got != "[run]" {
		t.Errorf("got %q want [run]", got)
	}
	if got := stepBadge(config.DeployStep{Command: "x"}); got != "[command]" {
		t.Errorf("got %q want [command]", got)
	}
	if got := stepBadge(config.DeployStep{Devbox: "docker down"}); got != "[devbox]" {
		t.Errorf("got %q want [devbox]", got)
	}
	if got := stepBadge(config.DeployStep{Builtin: "service_configs_copy"}); got != "[builtin]" {
		t.Errorf("got %q want [builtin]", got)
	}
}
