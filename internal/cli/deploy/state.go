package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newDeployStateCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Manage deploy state",
		Long: `View, clear, or repair the deploy state file at .dwe/deploy/state.yml.

The deploy state file tracks the outcome and hashes of every deployed step,
enabling idempotent deploys. Use 'show' to inspect the current state, 'clear'
to reset the state, or 'repair' to rebuild status aggregates.`,
		Example: `  dwe deploy state show
  dwe deploy state clear
  dwe deploy state repair`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDeployStateShowCmd(flags))
	cmd.AddCommand(newDeployStateClearCmd(flags))
	cmd.AddCommand(newDeployStateRepairCmd(flags))
	return cmd
}

func newDeployStateShowCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current deploy state",
		Long: `Print the contents of .dwe/deploy/state.yml in human-readable YAML format.

Shows per-step status, timestamps, action hashes, and duration metrics.
If the state file does not exist, shows a message indicating no state.`,
		Example: `  dwe deploy state show`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateShowCmd(flags, cmd.OutOrStdout())
		},
		SilenceUsage: true,
	}
	return cmd
}

func deployStateShowCmd(flags *cmdctx.RootFlags, out io.Writer) error {
	workDir := flags.ProjectRoot()
	statePath := filepath.Join(workDir, journal.DefaultRelPath)

	// Check existence before loading to avoid printing zero-value state.
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state found. Run 'dwe deploy run' to create state.")
		return nil
	}

	state, err := journal.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	_, _ = fmt.Fprint(out, string(data))
	return nil
}

func newDeployStateClearCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear the deploy state",
		Long: `Delete the deploy state file at .dwe/deploy/state.yml.

This removes all step status records, hashes, and run metrics. The next 'dwe deploy run'
will treat all steps as needing to be executed.

In interactive mode (TTY), a confirmation prompt is shown. Use -y/--non-interactive to skip the prompt.`,
		Example: `  dwe deploy state clear
  dwe deploy state clear -y`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateClearCmd(flags, force)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVarP(&force, "non-interactive", "y", false, "skip confirmation prompt")
	return cmd
}

func deployStateClearCmd(flags *cmdctx.RootFlags, force bool) error {
	workDir := flags.ProjectRoot()
	statePath := filepath.Join(workDir, journal.DefaultRelPath)
	lockPath := lock.DeployLockPath(workDir)

	// Acquire deploy lock to prevent concurrent deploy from overwriting the clear.
	lck, err := lock.Acquire(lockPath)
	if err != nil {
		if heldErr, ok := errors.AsType[*lock.HeldError](err); ok {
			lhe := &lock.ProjectLockHeldError{Operation: "clear state", PID: heldErr.PID}
			render.Stdout().Error(lhe.Error())
			return lhe
		}
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() { _ = lck.Release() }()

	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state to clear.")
		return nil
	}

	if !force && widgets.IsInteractiveFn(os.Stdin) {
		confirmed, err := widgets.RunConfirm(
			"Clear deploy state?",
			"Clear",
			"Cancel",
		)
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("cancelled")
		}
	}

	if err := journal.Remove(statePath); err != nil {
		return fmt.Errorf("clearing state: %w", err)
	}

	render.Stdout().Success("Deploy state cleared.")
	return nil
}

func newDeployStateRepairCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair the deploy state",
		Long: `Rebuild the deploy state file's status aggregates.

This recomputes the overall project and service status fields from the individual
step records, without modifying any step-level data (hashes, timestamps, etc.).

Use this to fix inconsistencies that may arise from manual edits or unexpected
process terminations.`,
		Example: `  dwe deploy state repair`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateRepairCmd(flags)
		},
		SilenceUsage: true,
	}
	return cmd
}

func deployStateRepairCmd(flags *cmdctx.RootFlags) error {
	workDir := flags.ProjectRoot()
	statePath := filepath.Join(workDir, journal.DefaultRelPath)
	lockPath := lock.DeployLockPath(workDir)

	// Acquire deploy lock to prevent racing with an in-progress deploy's flush.
	lck, err := lock.Acquire(lockPath)
	if err != nil {
		if heldErr, ok := errors.AsType[*lock.HeldError](err); ok {
			lhe := &lock.ProjectLockHeldError{Operation: "repair state", PID: heldErr.PID}
			render.Stdout().Error(lhe.Error())
			return lhe
		}
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() { _ = lck.Release() }()

	// Check existence before loading to avoid repairing a zero-value state.
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state to repair.")
		return nil
	}

	state, err := journal.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	journal.Recompute(state)

	if err := journal.Save(statePath, state); err != nil {
		return fmt.Errorf("saving repaired state: %w", err)
	}

	render.Stdout().Success("Deploy state repaired.")
	return nil
}
