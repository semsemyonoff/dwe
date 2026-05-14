package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newDeployStateCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Manage deploy state",
		Long: `View, clear, or repair the deploy state file at .devbox/deploy/state.yml.

The deploy state file tracks the outcome and hashes of every deployed step,
enabling idempotent deploys. Use 'show' to inspect the current state, 'clear'
to reset the state, or 'repair' to rebuild status aggregates.`,
		Example: `  devbox deploy state show
  devbox deploy state clear
  devbox deploy state repair`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDeployStateShowCmd(flags))
	cmd.AddCommand(newDeployStateClearCmd(flags))
	cmd.AddCommand(newDeployStateRepairCmd(flags))
	return cmd
}

func newDeployStateShowCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current deploy state",
		Long: `Print the contents of .devbox/deploy/state.yml in human-readable YAML format.

Shows per-step status, timestamps, action hashes, and duration metrics.
If the state file does not exist, shows a message indicating no state.`,
		Example: `  devbox deploy state show`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateShowCmd(flags)
		},
		SilenceUsage: true,
	}
	return cmd
}

func deployStateShowCmd(flags *rootFlags) error {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	state, err := journal.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Check if state file exists (Load returns a zero-value if absent)
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state found. Run 'devbox deploy run' to create state.")
		return nil
	}

	// Marshal state to YAML and print
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	_, _ = fmt.Fprint(os.Stdout, string(data))
	return nil
}

func newDeployStateClearCmd(flags *rootFlags) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear the deploy state",
		Long: `Delete the deploy state file at .devbox/deploy/state.yml.

This removes all step status records, hashes, and run metrics. The next 'devbox deploy run'
will treat all steps as needing to be executed.

In interactive mode (TTY), a confirmation prompt is shown. Use -y/--non-interactive to skip the prompt.`,
		Example: `  devbox deploy state clear
  devbox deploy state clear -y`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateClearCmd(flags, force)
		},
		SilenceUsage: true,
	}

	cmd.Flags().BoolVarP(&force, "non-interactive", "y", false, "skip confirmation prompt")
	return cmd
}

func deployStateClearCmd(flags *rootFlags, force bool) error {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Check if state file exists
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state to clear.")
		return nil
	}

	// Prompt for confirmation if not forced and interactive
	if !force && ui.IsInteractiveFn(os.Stdin) {
		confirmed, err := ui.RunConfirm(
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

func newDeployStateRepairCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair the deploy state",
		Long: `Rebuild the deploy state file's status aggregates.

This recomputes the overall project and service status fields from the individual
step records, without modifying any step-level data (hashes, timestamps, etc.).

Use this to fix inconsistencies that may arise from manual edits or unexpected
process terminations.`,
		Example: `  devbox deploy state repair`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deployStateRepairCmd(flags)
		},
		SilenceUsage: true,
	}
	return cmd
}

func deployStateRepairCmd(flags *rootFlags) error {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	state, err := journal.Load(statePath)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Check if state file exists
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		render.Stdout().Info("No deploy state to repair.")
		return nil
	}

	// Recompute status aggregates
	journal.Recompute(state)

	// Save the repaired state
	if err := journal.Save(statePath, state); err != nil {
		return fmt.Errorf("saving repaired state: %w", err)
	}

	render.Stdout().Success("Deploy state repaired.")
	return nil
}
