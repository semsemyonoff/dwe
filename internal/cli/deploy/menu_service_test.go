package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"

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

func TestBuildMenuField(t *testing.T) {
	t.Run("without wizard", func(t *testing.T) {
		field := buildMenuField(false)
		require.Len(t, field.Options, 5)
		assert.Equal(t, string(menuRun), field.Options[0].Value)
		assert.Equal(t, string(menuRun), field.Default, "first option should be preselected")
		for _, opt := range field.Options {
			assert.NotEqual(t, string(menuWizard), opt.Value, "wizard option must be absent when showWizard is false")
		}
	})

	t.Run("with wizard", func(t *testing.T) {
		field := buildMenuField(true)
		require.Len(t, field.Options, 6)
		assert.Equal(t, string(menuWizard), field.Options[0].Value, "wizard goes first when shown")
		assert.Equal(t, string(menuWizard), field.Default, "first option should be preselected")
	})

	t.Run("option labels carry the description", func(t *testing.T) {
		field := buildMenuField(false)
		var exitLabel string
		for _, opt := range field.Options {
			if opt.Value == string(menuExit) {
				exitLabel = opt.Label
			}
		}
		assert.Contains(t, exitLabel, "leave the deploy menu")
	})
}

func TestBuildServiceField(t *testing.T) {
	t.Run("options mirror items in order", func(t *testing.T) {
		items := []deployServiceItem{
			{Name: "db", Type: "infra"},
			{Name: "worker", Type: "app"},
		}
		field := buildServiceField(items, false)
		require.Len(t, field.Options, 2)
		assert.Equal(t, "db", field.Options[0].Value)
		assert.Equal(t, "worker", field.Options[1].Value)
		assert.Nil(t, field.Validate, "no validate hook when applyGate is false")
	})

	t.Run("default selection skips a locked first item", func(t *testing.T) {
		items := []deployServiceItem{
			{Name: "db", Type: "infra", Locked: true, LockedHint: "deploy required services first"},
			{Name: "worker", Type: "app"},
		}
		field := buildServiceField(items, true)
		assert.Equal(t, "worker", field.Default, "first non-locked item should be preselected")
	})

	t.Run("default falls back to first item when all are locked", func(t *testing.T) {
		items := []deployServiceItem{
			{Name: "db", Type: "infra", Locked: true, LockedHint: "deploy required services first"},
		}
		field := buildServiceField(items, true)
		assert.Equal(t, "db", field.Default)
	})

	t.Run("locked validate hook rejects locked items", func(t *testing.T) {
		items := []deployServiceItem{
			{Name: "db", Type: "infra", Locked: true, LockedHint: "deploy required services first"},
			{Name: "worker", Type: "app"},
		}
		field := buildServiceField(items, true)
		require.NotNil(t, field.Validate)

		err := field.Validate("db")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deploy required services first")

		assert.NoError(t, field.Validate("worker"))
	})
}

func TestMapMenuSelectionErr(t *testing.T) {
	t.Run("cancelled maps to menuExit with no error", func(t *testing.T) {
		choice, err := mapMenuSelectionErr(widgets.ErrCancelled)
		require.NoError(t, err)
		assert.Equal(t, menuExit, choice)
	})

	t.Run("other errors are wrapped", func(t *testing.T) {
		choice, err := mapMenuSelectionErr(errors.New("boom"))
		require.Error(t, err)
		assert.Equal(t, menuChoice(""), choice)
		assert.Contains(t, err.Error(), "menu selection")
		assert.Contains(t, err.Error(), "boom")
	})
}

func TestMapServiceSelectionErr(t *testing.T) {
	t.Run("cancelled passes through unchanged", func(t *testing.T) {
		name, err := mapServiceSelectionErr(widgets.ErrCancelled)
		assert.Empty(t, name)
		assert.ErrorIs(t, err, widgets.ErrCancelled)
	})

	t.Run("other errors are wrapped", func(t *testing.T) {
		name, err := mapServiceSelectionErr(errors.New("boom"))
		require.Error(t, err)
		assert.Empty(t, name)
		assert.Contains(t, err.Error(), "service selection")
		assert.Contains(t, err.Error(), "boom")
	})
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
	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldSelect := selectMenuItemFn
	oldPicker := selectDeployServiceFn
	oldRunFn := runDeployRunFn
	oldBuild := buildDeployItemsFn
	oldIsInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		selectMenuItemFn = oldSelect
		selectDeployServiceFn = oldPicker
		runDeployRunFn = oldRunFn
		buildDeployItemsFn = oldBuild
		widgets.IsInteractiveFn = oldIsInteractive
	})

	buildDeployItemsFn = func(baseDir string, cfg *config.DweConfig, state *journal.ProjectState) ([]deployServiceItem, error) {
		return []deployServiceItem{{Name: "web", Type: "app", Mandatory: true}}, nil
	}

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

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
		return "", widgets.ErrCancelled
	}

	runDeployRunFn = func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deployRunOpts) error {
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
