package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/usercommands"
)

// StopContext carries all parameters for the stop lifecycle entry point.
type StopContext struct {
	ConfigPath string
	Yes        bool
}

// RunStop executes the full stop lifecycle.
//
// lifecycle.yml is optional for stop: when absent, only the synthetic
// _auto_reap_daemons phase runs, followed by the default final message.
// When present, the auto-reap phase is prepended to the user-defined phases.
func RunStop(ctx StopContext) error {
	workDir := filepath.Dir(ctx.ConfigPath)

	cfg, err := config.LoadConfig(ctx.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	lifecyclePath := filepath.Join(workDir, "devbox", "lifecycle.yml")
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("loading lifecycle config: %w", err)
	}

	stopCfg := EnsureStopConfig(lifecycleCfg)

	reg, err := usercommands.LoadRegistryFromConfigPath(ctx.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	if err := RunPhases(cfg, reg, workDir, stopCfg.Phases, "stop", "stop", ctx.Yes, stopCfg.LogEnabled()); err != nil {
		return err
	}

	render.Stdout().Success(stopCfg.FinalMessage)
	return nil
}
