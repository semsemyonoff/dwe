package reset_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/execution/condition"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

func writeResetYML(t *testing.T, dir, content string) {
	t.Helper()
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatalf("mkdir devbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "reset.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write reset.yml: %v", err)
	}
}

func phaseWith(name string, steps ...config.DeployStep) config.DeployPhase {
	return config.DeployPhase{Name: name, Description: name + " phase", Steps: steps}
}

func cmdStep(name, cmd string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "shell", Cmd: cmd, Description: name + " description"}
}

func commandStep(name, id string) config.DeployStep {
	return config.DeployStep{Name: name, Type: "command", Cmd: id, Description: name + " description"}
}

// parseWhenString converts a legacy when string to a typed condition.
// Supports:
// - "{{...}}" → template
// - "cmd: ..." → shell
// - "dir-empty ..." → builtin
func parseWhenString(when string) *condition.Condition {
	if when == "" {
		return nil
	}
	kind, payload := condition.Classify(when)
	switch kind {
	case condition.KindTemplate:
		return &condition.Condition{Type: condition.TypeTemplate, Expr: payload}
	case condition.KindBuiltin:
		return &condition.Condition{Type: condition.TypeBuiltin, Cmd: payload}
	case condition.KindCmd:
		return &condition.Condition{Type: condition.TypeShell, Cmd: payload}
	default:
		return nil
	}
}
