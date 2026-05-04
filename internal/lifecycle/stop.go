package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// StopContext carries all parameters for the stop lifecycle entry point.
type StopContext struct {
	ConfigPath string
	Yes        bool
}

// RunStop executes the full stop lifecycle driven by devbox/lifecycle.yml.
func RunStop(ctx StopContext) error {
	workDir := filepath.Dir(ctx.ConfigPath)

	cfg, err := config.LoadConfig(ctx.ConfigPath)
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

	reg, err := loadRegistry(ctx.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	if err := RunPhases(cfg, reg, workDir, lifecycleCfg.Stop.Phases, "stop", "stop", ctx.Yes, lifecycleCfg.Stop.LogEnabled()); err != nil {
		return err
	}

	render.Stdout().Success(lifecycleCfg.Stop.FinalMessage)
	return nil
}
