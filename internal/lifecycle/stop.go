package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/usercommands"
)

// StopContext carries all parameters for the stop lifecycle entry point.
type StopContext struct {
	// Ctx is the parent context for preflight checks. Nil defaults to context.Background().
	Ctx        context.Context
	ConfigPath string
	Yes        bool
	// SkipPreflight bypasses env probes + project checks for this stop.
	SkipPreflight bool
	// ErrOut receives preflight diagnostic output. nil falls back to os.Stderr.
	ErrOut io.Writer
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

	// Hoist registry load ahead of preflight (nil-tolerant — preflight will
	// surface unknown-command diagnostics for any type: command checks).
	reg, regErr := usercommands.LoadRegistryFromConfigPath(ctx.ConfigPath)
	if regErr != nil {
		reg = nil
	}

	errOut := ctx.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}
	pfCtx := ctx.Ctx
	if pfCtx == nil {
		pfCtx = context.Background()
	}
	if err := PreflightFunc(pfCtx, cfg, reg, workDir, "stop", ctx.SkipPreflight, errOut); err != nil {
		return err
	}
	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	lifecyclePath := filepath.Join(workDir, "devbox", "lifecycle.yml")
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("loading lifecycle config: %w", err)
	}

	stopCfg := EnsureStopConfig(lifecycleCfg)

	if err := RunPhases(cfg, reg, workDir, stopCfg.Phases, "stop", "stop", ctx.Yes, stopCfg.LogEnabled()); err != nil {
		return err
	}

	render.Stdout().Success(stopCfg.FinalMessage)
	return nil
}
