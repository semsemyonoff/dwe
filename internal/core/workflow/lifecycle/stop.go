package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/liveui"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// stopWaitStdoutIsTTY reports whether os.Stdout is a terminal — the gate for the
// animated port-release footer (a no-op LiveLine is used when piped). A var so
// tests can force either path.
var stopWaitStdoutIsTTY = func() bool { return term.IsTerminal(os.Stdout.Fd()) }

// newStopWaitLiveLine builds the LiveLine that renders the post-stop
// port-release wait as a spinner + live [elapsed] timer footer. On a TTY it
// writes cursor/spinner frames to os.Stdout; piped, it is disabled (every method
// a no-op) so logs stay clean. Mirrors the workflow runner's parallel LiveLine
// construction.
func newStopWaitLiveLine() *liveui.LiveLine {
	termOut := io.Writer(io.Discard)
	isTTY := stopWaitStdoutIsTTY()
	if isTTY {
		termOut = os.Stdout
	}
	return liveui.NewLiveLine(termOut, os.Stdout, isTTY)
}

// stopWaitText renders the footer text describing which host ports the stop is
// still waiting to be released. The LiveLine prepends the spinner glyph and a
// live [elapsed] stopwatch, so this returns only the descriptive tail.
func stopWaitText(waiting []env.PortWait) string {
	if len(waiting) == 1 {
		w := waiting[0]
		return fmt.Sprintf("waiting for host port %d (%s.%s) to be released…", w.HostPort, w.Service, w.PortName)
	}
	parts := make([]string, len(waiting))
	for i, w := range waiting {
		parts[i] = fmt.Sprintf("%d (%s.%s)", w.HostPort, w.Service, w.PortName)
	}
	return "waiting for host ports to be released: " + strings.Join(parts, ", ")
}

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

	cfg, err := config.LoadConfigOrWrap(ctx.ConfigPath)
	if err != nil {
		return err
	}

	// Hoist registry load ahead of preflight (nil-tolerant — preflight will
	// surface unknown-command diagnostics for any type: command checks).
	reg, regErr := loadRegistryWithVisibility(ctx.ConfigPath, cfg, workDir)

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

	lifecyclePath := filepath.Join(workDir, "workspace", "lifecycle.yml")
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

	// After `docker compose down`, Docker Desktop / OrbStack release the stack's
	// published host ports asynchronously — a beat after the containers vanish
	// from `docker ps`. Wait for that release here so the run leg of a `dwe
	// restart` (and any manual `dwe run` that follows) does not race it and
	// falsely report a self-conflict on our own just-freed port (e.g. caddy :80).
	// The wait surfaces as a spinner + live timer footer naming the port(s) still
	// pending; it is bounded by stop.port_release_timeout (default 60s, "0"
	// disables) and a timeout only warns — it never fails the stop.
	waitTimeout := config.StopPortReleaseTimeout(cfg)
	var waitLive *liveui.LiveLine
	onWait := func(waiting []env.PortWait) {
		if len(waiting) == 0 {
			return
		}
		if waitLive == nil {
			waitLive = newStopWaitLiveLine()
			waitLive.Start()
		}
		waitLive.SetText(stopWaitText(waiting))
	}
	busy := env.WaitPortsReleased(pfCtx, cfg, waitTimeout, onWait)
	if waitLive != nil {
		waitLive.Stop()
	}
	if len(busy) > 0 {
		render.Stdout().Warning(fmt.Sprintf(
			"host port(s) %s still in use after stop (waited up to %s) — a following start may report a conflict",
			formatPortList(busy), waitTimeout))
	}

	// Whole-stack stop also stops the host-bridge daemon (design D6); the
	// per-service `dwe stop <name>` path never touches it. Best-effort: the
	// daemon auto-stops once zero labeled containers remain, so a signaling
	// failure must not fail the stop.
	if _, err := BridgeStopDaemonFunc(bridge.DefaultBridgeDir(workDir)); err != nil {
		render.Stdout().Warning(fmt.Sprintf("stopping bridge daemon: %v", err))
	}

	// Stopping the full stack moots any pending restart: the next `dwe run`
	// brings everything up in its current local.yml shape, so the restart
	// reminder is no longer actionable. Pending deploy ops are NOT cleared —
	// deploy tracks artifact state and survives a stop/run cycle (the run gate
	// would catch any undeployed tracked service anyway).
	clearPendingRestart(workDir, "clearing pending restart state after stop")

	render.Stdout().Success(stopCfg.FinalMessage)
	return nil
}

// formatPortList renders a sorted port slice as a comma-separated string for the
// post-stop "still in use" warning (e.g. "80, 443").
func formatPortList(ports []int) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}
