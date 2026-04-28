package command

import (
	"strings"
	"testing"
)

func TestDownCmdWiring(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newDownCmd(flags)

	if cmd.Use != "down" {
		t.Errorf("Use = %q, want %q", cmd.Use, "down")
	}

	if !strings.Contains(cmd.Long, "devbox stop") {
		t.Error("Long description should mention 'devbox stop' as the full lifecycle alternative")
	}

	if !strings.Contains(cmd.Long, "devbox docker stop") {
		t.Error("Long description should mention 'devbox docker stop' for raw compose stop")
	}

	// down has no explicit Args validator (accepts zero args by default cobra behavior).
	if cmd.Args != nil {
		if err := cmd.Args(cmd, nil); err != nil {
			t.Errorf("down should accept zero args, got error: %v", err)
		}
	}
}
