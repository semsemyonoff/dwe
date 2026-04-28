package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newStopCmd(flags *rootFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the project (full lifecycle: before-stop hooks → docker down → after-stop hooks)",
		Long: `Stop the project driven by devbox/lifecycle.yml.

Execution order: before-stop hooks → docker down → after-stop hooks → final message.

Use 'devbox down' for a bare Docker Compose stop-and-remove without hooks.
Use 'devbox docker stop' for the low-level compose stop (no container removal).`,
		Example:      `  devbox stop`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(flags, yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	return cmd
}

func runStop(flags *rootFlags, yes bool) error {
	workDir := filepath.Dir(flags.configPath)

	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	lifecyclePath := filepath.Join(workDir, "devbox", "lifecycle.yml")
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no lifecycle.yml — see devbox/lifecycle.example.yml")
		}
		return fmt.Errorf("loading lifecycle config: %w", err)
	}
	if lifecycleCfg.Stop == nil {
		return fmt.Errorf("lifecycle.yml has no `stop:` section — see devbox/lifecycle.example.yml")
	}

	reg, err := loadCommandRegistry(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	if err := runLifecyclePhases(cfg, reg, workDir, lifecycleCfg.Stop.Phases, "stop", "stop", yes, lifecycleCfg.Stop.LogEnabled()); err != nil {
		return err
	}

	render.Stdout().Success(lifecycleCfg.Stop.FinalMessage)
	return nil
}
