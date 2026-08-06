package pipeline

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
	if got := stepBadge(config.DeployStep{Type: "dwe", Cmd: "docker down"}); got != "[dwe]" {
		t.Errorf("got %q want [dwe]", got)
	}
	if got := stepBadge(config.DeployStep{Type: "builtin", Cmd: "service_configs_copy"}); got != "[builtin]" {
		t.Errorf("got %q want [builtin]", got)
	}
}

func TestResolvePhaseSteps_filesGateThreadedThrough(t *testing.T) {
	cfg := &config.DweConfig{}

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

	// FilesGate is rendered into a fresh copy (never the same pointer as the
	// loaded config's step) — see render.go's renderFilesGate — so this checks
	// value equality, not identity.
	if resolved[0].FilesGate == nil || resolved[0].FilesGate.State != fg.State {
		t.Errorf("FilesGate not threaded through: expected State %v, got %v", fg.State, resolved[0].FilesGate)
	}
	if resolved[0].FilesGate.State != filesgate.StateReadable {
		t.Errorf("FilesGate.State = %q, want readable", resolved[0].FilesGate.State)
	}
}

func TestUnresolvedTemplateRefs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"no refs", "echo hello", nil},
		{"resolved known head leaves nothing", "git clone https://example.com/repo dst", nil},
		{"unknown head flagged", "echo ${HOME}", []string{"${HOME}"}},
		{"known head not flagged", "echo ${vars.x}", nil},
		// A head-only token is left literal by the renderer (it is a shell
		// variable, not a reference), so the plan reports it exactly like
		// ${HOME} rather than implying it was substituted.
		{"head-only flagged like any shell var", "curl http://${host}/", []string{"${host}"}},
		{"bare args is a reference, not a leftover", "go test ${args}", nil},
		{"mixed known and unknown", "echo ${vars.x} ${HOME}", []string{"${HOME}"}},
		{"dedupes repeats", "echo ${HOME} and ${HOME} again", []string{"${HOME}"}},
		{"preserves first-occurrence order", "echo ${PATH} then ${HOME}", []string{"${PATH}", "${HOME}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnresolvedTemplateRefs(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("UnresolvedTemplateRefs(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("UnresolvedTemplateRefs(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
