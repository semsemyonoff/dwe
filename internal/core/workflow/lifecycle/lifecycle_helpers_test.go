package lifecycle

import (
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

func TestClearPendingRestart(t *testing.T) {
	t.Run("clears an existing restart op", func(t *testing.T) {
		workDir := t.TempDir()
		statePath := filepath.Join(workDir, journal.DefaultRelPath)
		if err := journal.AddPendingOp(statePath, journal.PendingOp{Kind: journal.PendingRestart}, "hash1"); err != nil {
			t.Fatalf("seeding pending restart: %v", err)
		}

		clearPendingRestart(workDir, "test clear")

		st, err := journal.Load(statePath)
		if err != nil {
			t.Fatalf("loading state: %v", err)
		}
		if st != nil && st.Pending != nil {
			for _, op := range st.Pending.Operations {
				if op.Kind == journal.PendingRestart {
					t.Fatalf("restart op was not cleared: %+v", st.Pending.Operations)
				}
			}
		}
	})

	t.Run("missing state file is a no-op", func(t *testing.T) {
		// Must not panic or create the file when there is nothing to clear.
		clearPendingRestart(t.TempDir(), "test clear")
	})
}

func TestLoadRegistryWithVisibility_MissingCommandsDir(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, workDir)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	reg, regErr := loadRegistryWithVisibility(cfgPath, cfg, workDir)
	if regErr != nil {
		t.Fatalf("unexpected error for missing commands dir: %v", regErr)
	}
	if reg == nil {
		t.Fatal("expected a non-nil empty registry for missing commands dir")
	}
}
