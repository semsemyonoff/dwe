package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/shared/lock"
	"devbox-cli/internal/shared/render"
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
	// Translator and Locale provide i18n lookups for user commands invoked as
	// pipeline steps. When nil, NopTranslator is used (English fallback).
	Translator i18n.Translator
	Locale     string
	// OnDefaultUsed is called when the stop pipeline was absent (file missing
	// or no stop: section) and the built-in default was used. CLI uses this to
	// emit the info line on stderr.
	OnDefaultUsed func(DefaultedPipeline)
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

	// Acquire deploy + snapshot project locks AFTER preflight (preflight may
	// invoke user type:command checks that must not hold operation locks).
	// Locks are released on function exit.
	releaseLocks, err := lock.AcquireProjectLocks(workDir)
	if err != nil {
		return err
	}
	defer releaseLocks()

	lifecyclePath := filepath.Join(workDir, "devbox", "lifecycle.yml")
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("loading lifecycle config: %w", err)
	}

	stopCfg, defaulted := EnsureStopConfig(lifecycleCfg)
	if defaulted && ctx.OnDefaultUsed != nil {
		ctx.OnDefaultUsed(DefaultedStop)
	}

	if err := RunPhases(cfg, reg, workDir, stopCfg.Phases, "stop", "stop", ctx.Yes, stopCfg.LogEnabled(), ctx.Translator, ctx.Locale); err != nil {
		return err
	}

	// Stopping the full stack moots any pending restart: the next `devbox run`
	// brings everything up in its current local.yml shape, so the restart
	// reminder is no longer actionable. Pending deploy ops are NOT cleared —
	// deploy tracks artifact state and survives a stop/run cycle (the run gate
	// would catch any undeployed tracked service anyway).
	statePath := filepath.Join(workDir, journal.DefaultRelPath)
	if clearErr := journal.ClearPendingForKind(statePath, journal.PendingRestart); clearErr != nil {
		slog.Warn("clearing pending restart state after stop", "err", clearErr)
	}

	render.Stdout().Success(stopCfg.FinalMessage)
	return nil
}
