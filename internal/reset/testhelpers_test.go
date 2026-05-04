package reset_test

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
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
	return config.DeployStep{Name: name, Run: cmd, Description: name + " description"}
}

func commandStep(name, id string) config.DeployStep {
	return config.DeployStep{Name: name, Command: id, Description: name + " description"}
}
