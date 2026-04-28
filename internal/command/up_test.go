package command

import (
	"strings"
	"testing"
)

func TestUpCmdWiring(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newUpCmd(flags)

	if cmd.Use != "up [services...]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "up [services...]")
	}

	if !strings.Contains(cmd.Long, "devbox run") {
		t.Error("Long description should mention 'devbox run' as the full lifecycle alternative")
	}

	// up accepts arbitrary service name args (no Args restriction set means any args are valid).
	if cmd.Args != nil {
		if err := cmd.Args(cmd, []string{"app-main", "db"}); err != nil {
			t.Errorf("up should accept service name args, got error: %v", err)
		}
		if err := cmd.Args(cmd, nil); err != nil {
			t.Errorf("up should accept zero args, got error: %v", err)
		}
	}
}
