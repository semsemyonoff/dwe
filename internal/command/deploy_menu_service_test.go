package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyMandatoryGate(t *testing.T) {
	t.Run("locks optional when mandatory not deployed", func(t *testing.T) {
		in := []deployServiceItem{
			{Name: "db", Type: "infra", Mandatory: true, Deployed: false},
			{Name: "main", Type: "app", Mandatory: true, Deployed: true},
			{Name: "worker", Type: "app", Mandatory: false, Deployed: false},
		}
		out := applyMandatoryGate(in)
		require.Len(t, out, 3)
		assert.False(t, out[0].Locked)
		assert.False(t, out[1].Locked)
		assert.True(t, out[2].Locked)
		assert.NotEmpty(t, out[2].LockedHint)
	})

	t.Run("unlocks all when every mandatory is deployed", func(t *testing.T) {
		in := []deployServiceItem{
			{Name: "db", Type: "infra", Mandatory: true, Deployed: true},
			{Name: "worker", Type: "app", Mandatory: false, Deployed: false},
		}
		out := applyMandatoryGate(in)
		for _, it := range out {
			assert.False(t, it.Locked, "%s should not be locked", it.Name)
		}
	})

	t.Run("no required: nothing locked", func(t *testing.T) {
		in := []deployServiceItem{
			{Name: "a", Type: "app", Mandatory: false, Deployed: false},
			{Name: "b", Type: "app", Mandatory: false, Deployed: false},
		}
		out := applyMandatoryGate(in)
		for _, it := range out {
			assert.False(t, it.Locked)
		}
	})
}

func TestFormatDeployServiceLabel(t *testing.T) {
	cases := []struct {
		name     string
		item     deployServiceItem
		contains []string
	}{
		{
			name:     "mandatory deployed",
			item:     deployServiceItem{Name: "db", Type: "infra", Mandatory: true, Deployed: true},
			contains: []string{"✓", "infra", "mandatory", "db"},
		},
		{
			name:     "optional not deployed",
			item:     deployServiceItem{Name: "worker", Type: "app", Mandatory: false, Deployed: false},
			contains: []string{"·", "app", "optional", "not deployed", "worker"},
		},
		{
			name:     "locked optional",
			item:     deployServiceItem{Name: "worker", Type: "app", Locked: true, LockedHint: "deploy mandatory services first"},
			contains: []string{"worker", "deploy mandatory services first"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDeployServiceLabel(tc.item, len("worker"))
			for _, want := range tc.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestFormatServiceMeta(t *testing.T) {
	meta := formatServiceMeta(deployServiceItem{
		Name: "worker", Type: "app", Mandatory: false, Deployed: false,
		Locked: true, LockedHint: "deploy mandatory services first",
	})
	assert.Contains(t, meta, "optional")
	assert.Contains(t, meta, "not deployed")
	assert.Contains(t, meta, "deploy mandatory services first")
}

func TestDeployInfoRowsFrom(t *testing.T) {
	now := time.Now()
	items := []deployServiceItem{
		{Name: "db", Type: "infra", Deployed: true, DeployedAt: now},
		{Name: "worker", Type: "app", Deployed: false},
	}
	rows := deployInfoRowsFrom(items)
	require.Len(t, rows, 2)
	assert.Equal(t, "db", rows[0].Name)
	assert.Equal(t, journal.StatusDeployed, rows[0].Status)
	assert.False(t, rows[0].NotDeployed)
	assert.True(t, rows[1].NotDeployed)
}

func TestRunDeployMenu_SubmenuCancelLoopsBack(t *testing.T) {
	tmpdir := t.TempDir()
	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test\n"), 0o644))

	flags := &rootFlags{configPath: filepath.Join(devboxDir, "devbox.yml")}

	oldSelect := selectMenuItemFn
	oldPicker := selectDeployServiceFn
	oldRunFn := runDeployRunFn
	oldBuild := buildDeployItemsFn
	oldIsInteractive := ui.IsInteractiveFn
	t.Cleanup(func() {
		selectMenuItemFn = oldSelect
		selectDeployServiceFn = oldPicker
		runDeployRunFn = oldRunFn
		buildDeployItemsFn = oldBuild
		ui.IsInteractiveFn = oldIsInteractive
	})

	buildDeployItemsFn = func(baseDir string, cfg *config.DevboxConfig, state *journal.ProjectState) ([]deployServiceItem, error) {
		return []deployServiceItem{{Name: "web", Type: "app", Mandatory: true}}, nil
	}

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	calls := 0
	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		calls++
		if calls == 1 {
			return menuRunService, nil
		}
		return menuExit, nil
	}

	pickerCalled := false
	selectDeployServiceFn = func(ctx context.Context, cmd *cobra.Command, title string, items []deployServiceItem, applyGate bool) (string, error) {
		pickerCalled = true
		return "", ui.ErrCancelled
	}

	runDeployRunFn = func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts deployRunOpts) error {
		t.Fatalf("runDeployRun must not be called when picker is cancelled")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	require.NoError(t, runDeployMenu(cmd, flags))
	assert.Equal(t, 2, calls, "menu must re-show after submenu cancel")
	assert.True(t, pickerCalled)
}
