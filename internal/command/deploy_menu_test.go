package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/ui"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDeployMenu_NonTTY_PrintsHelpAndExits(t *testing.T) {
	cmd := &cobra.Command{}
	flags := &rootFlags{configPath: "/tmp/nonexistent"}

	// Stub stdin as non-TTY
	oldIsInteractive := ui.IsInteractiveFn
	defer func() { ui.IsInteractiveFn = oldIsInteractive }()
	ui.IsInteractiveFn = func(stdin io.Reader) bool { return false }

	err := runDeployMenu(cmd, flags)
	if err == nil {
		t.Fatal("expected error for non-TTY")
	}

	// Should be a usageError with exit code 2
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("expected usageError, got %T", err)
	}
}


func TestRunDeployMenu_MenuDispatch_Run(t *testing.T) {
	tmpdir := t.TempDir()

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &rootFlags{configPath: filepath.Join(devboxDir, "devbox.yml")}

	// Override test seams
	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldRunDeployRunFn := runDeployRunFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runDeployRunFn = oldRunDeployRunFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply) (menuChoice, error) {
		return menuRun, nil
	}

	runDeployCalled := false
	runDeployRunFn = func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts deployRunOpts) error {
		runDeployCalled = true
		assert.Equal(t, "", opts.ServiceName)
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.NoError(t, err)
	require.True(t, runDeployCalled, "runDeployRun should have been called")
}

func TestRunDeployMenu_MenuDispatch_Exit(t *testing.T) {
	tmpdir := t.TempDir()

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &rootFlags{configPath: filepath.Join(devboxDir, "devbox.yml")}

	// Override test seams
	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply) (menuChoice, error) {
		return menuExit, nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.NoError(t, err)
}

func TestIsEmptyLocal(t *testing.T) {
	cases := []struct {
		name     string
		input    map[string]interface{}
		expected bool
	}{
		{"empty map", map[string]interface{}{}, true},
		{"nil map", nil, true},
		{"single key", map[string]interface{}{"services": map[string]interface{}{}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isEmptyLocal(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}
