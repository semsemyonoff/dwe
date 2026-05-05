package pipeline

import (
	"testing"

	"devbox-cli/internal/config"
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
