package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/envfile"
	"devbox-cli/internal/git"
	"devbox-cli/internal/i18n"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/notify"
	"devbox-cli/internal/preflight"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/userconfig"
)

// GitProbeFunc is a package-level variable so tests can inject stubs without
// touching a real git repository.
var GitProbeFunc = git.Probe

// GitPullFFOnlyFunc is a package-level variable so tests can inject a stub
// pull implementation without touching a real git repository.
var GitPullFFOnlyFunc = git.PullFFOnly

// PreflightFunc is a package-level variable so tests can stub preflight
// without setting up env probes / validate.yml on disk. Same pattern as
// GitProbeFunc / GitPullFFOnlyFunc.
var PreflightFunc = preflight.Run

// RunContext carries all parameters for the run (and restart) lifecycle entry points.
type RunContext struct {
	// Ctx is the parent context for preflight checks. Nil defaults to context.Background().
	Ctx        context.Context
	ConfigPath string
	NoUpdate   bool
	UpdateMode string
	Yes        bool
	// ShowInfo is called after the run phases complete, when lifecycle.yml has show_info: true.
	// Callers inject this to avoid importing the cobra info renderer into this package.
	// If nil, no info display is attempted.
	ShowInfo func() error
	// SkipNotify suppresses the end-of-run desktop notification. Set true by
	// RunRestart on its inner RunRun call so a restart fires at most one
	// notification (and the spec says restart never notifies).
	SkipNotify bool
	// SkipPreflight bypasses env probes + project checks for this run.
	SkipPreflight bool
	// ErrOut receives preflight diagnostic output. nil falls back to os.Stderr.
	ErrOut io.Writer
	// SkipClearPending suppresses the automatic ClearPendingForKind(PendingRestart)
	// that RunRestart calls on success. Set true when RunRestart is called from
	// executeTogglePlan so the executor owns the final atomic clear via ClearPendingOps.
	SkipClearPending bool
	// Translator and Locale provide i18n lookups for user commands invoked as
	// pipeline steps. When nil, NopTranslator is used (English fallback).
	Translator i18n.Translator
	Locale     string
}

// resolveUpdateMode applies CLI flag precedence on top of the lifecycle config's effective mode.
// Precedence: NoUpdate > UpdateMode flag > LifecycleRunConfig.EffectiveMode()
func resolveUpdateMode(cfg *config.LifecycleRunConfig, noUpdate bool, updateFlag string) string {
	mode := cfg.EffectiveMode()
	if updateFlag != "" {
		mode = updateFlag
	}
	if noUpdate {
		mode = "off"
	}
	return mode
}

// RunRun executes the full run lifecycle driven by devbox/lifecycle.yml.
func RunRun(ctx RunContext) (err error) {
	workDir := filepath.Dir(ctx.ConfigPath)

	// Install notifier defer before any error-returning step so an early
	// config-load failure still fires a "run failed" notification.
	// projectName stays empty until main config load succeeds and is read
	// by the defer through closure capture.
	var projectName string
	if !ctx.SkipNotify {
		start := time.Now()
		ucfg, ucfgErr := userconfig.Load(workDir)
		if ucfgErr != nil {
			slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
			ucfg = nil
		}
		n := newNotifier(ucfg)
		defer func() {
			// Preflight-blocked and lock-held are not run failures —
			// suppress the notification.
			if errors.As(err, new(*preflight.Error)) {
				return
			}
			if errors.As(err, new(*lock.ProjectLockHeldError)) {
				return
			}
			n.Notify(context.Background(), notify.Event{
				Kind:      notify.OpRun,
				Operation: "run",
				Outcome:   notify.OutcomeFromErr(err),
				Duration:  time.Since(start),
				Err:       err,
				Project:   projectName,
			})
		}()
	}

	cfg, err := config.LoadConfig(ctx.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	projectName = cfg.Project.Name

	// Render .env before any other action so preflight checks
	// (type: command), lifecycle phases, and user commands see fresh values.
	// Mirrors the implicit render-env step at the head of the deploy pipeline.
	if err := renderAndSourceDotEnv(cfg, workDir); err != nil {
		return err
	}

	// Hoist registry load ahead of preflight so type: command checks can
	// dispatch. nil-tolerant: load failure does not abort — preflight will
	// surface unknown-command diagnostics for any checks that referenced it.
	reg, regErr := usercommands.LoadRegistryFromConfigPath(ctx.ConfigPath)
	if regErr != nil {
		reg = nil
	}

	lifecyclePath := filepath.Join(workDir, "devbox", "lifecycle.yml")

	if ctx.UpdateMode != "" && !config.ValidUpdateMode(ctx.UpdateMode) {
		return fmt.Errorf("invalid --update mode %q: must be one of: on, off", ctx.UpdateMode)
	}

	errOut := ctx.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}
	pfCtx := ctx.Ctx
	if pfCtx == nil {
		pfCtx = context.Background()
	}
	if err := PreflightFunc(pfCtx, cfg, reg, workDir, "run", ctx.SkipPreflight, errOut); err != nil {
		return err
	}

	// Acquire deploy + snapshot project locks AFTER preflight (preflight may
	// invoke user type:command checks that must not hold operation locks).
	// Locks are released on function exit.
	releaseLocks, err := lock.AcquireProjectLocks(workDir)
	if err != nil {
		return err
	}
	defer releaseLocks()

	// Surface a deferred registry load failure now that preflight is past —
	// only fail when the registry was non-empty-relevant (i.e. it actually
	// failed, not just absent). LoadRegistryFromConfigPath returns nil error
	// when the commands dir is missing, so any error here is a real fault.
	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Load lifecycle config after preflight so env diagnostics (docker daemon
	// not running, binary missing, etc.) are surfaced even when lifecycle.yml
	// is absent or malformed — consistent with how RunStop orders things.
	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no lifecycle.yml — see devbox/lifecycle.example.yml")
		}
		return fmt.Errorf("loading lifecycle config: %w", err)
	}
	if lifecycleCfg.Run == nil {
		return fmt.Errorf("lifecycle.yml has no `run:` section — see devbox/lifecycle.example.yml")
	}

	effectiveMode := resolveUpdateMode(lifecycleCfg.Run, ctx.NoUpdate, ctx.UpdateMode)

	w := render.Stdout()
	var pulled bool
	if effectiveMode != "off" {
		status, err := GitProbeFunc(config.GitBin(cfg), workDir, true)
		if err != nil {
			return fmt.Errorf("git probe: %w", err)
		}
		action, msg := git.Decide(status, git.UpdateMode(effectiveMode), ui.IsInteractiveFn(os.Stdin))
		switch action {
		case git.ActionWarn:
			w.Warning(msg)
		case git.ActionPullAuto:
			moved, pullErr := GitPullFFOnlyFunc(config.GitBin(cfg), workDir)
			if pullErr != nil {
				w.Warning(fmt.Sprintf("git pull --ff-only failed: %v", pullErr))
			} else {
				pulled = moved
			}
		case git.ActionPullPrompt:
			confirmed, confirmErr := ui.RunConfirm(
				fmt.Sprintf("Update available: %s — pull now?", msg),
				"Pull", "Skip",
			)
			if confirmErr == nil && confirmed {
				moved, pullErr := GitPullFFOnlyFunc(config.GitBin(cfg), workDir)
				if pullErr != nil {
					w.Warning(fmt.Sprintf("git pull --ff-only failed: %v", pullErr))
				} else {
					pulled = moved
				}
			} else if confirmErr != nil && !errors.Is(confirmErr, ui.ErrCancelled) {
				w.Warning(fmt.Sprintf("confirmation prompt failed: %v — skipping update", confirmErr))
			}
		default:
			// git.ActionSkip: nothing to do.
		}
	}

	// If pull moved HEAD, reload all configs from disk.
	if pulled {
		cfg, err = config.LoadConfig(ctx.ConfigPath)
		if err != nil {
			return fmt.Errorf("reloading config after pull: %w", err)
		}
		projectName = cfg.Project.Name
		// Re-render .env against the post-pull config so phases below see
		// any changes that came in with the update.
		if err := renderAndSourceDotEnv(cfg, workDir); err != nil {
			return err
		}
		lifecycleCfg, err = config.LoadLifecycleConfig(lifecyclePath)
		if err != nil {
			return fmt.Errorf("reloading lifecycle config after pull: %w", err)
		}
		if lifecycleCfg.Run == nil {
			return fmt.Errorf("lifecycle.yml has no `run:` section after pull reload")
		}
		reg, err = usercommands.LoadRegistryFromConfigPath(ctx.ConfigPath)
		if err != nil {
			return fmt.Errorf("reloading command registry after pull: %w", err)
		}
	}

	// Gate: ensure all tracked services are deployed.
	tracked, _, err := deploy.LoadTrackedServices(cfg, reg, workDir)
	if err != nil {
		return fmt.Errorf("loading tracked services: %w", err)
	}

	if len(tracked) > 0 {
		statePath := filepath.Join(workDir, journal.DefaultRelPath)
		state, err := journal.Load(statePath)
		if err != nil {
			return fmt.Errorf("loading deploy state: %w", err)
		}

		for _, svcName := range tracked {
			svcState, ok := state.Services[svcName]
			if !ok || svcState.Status != journal.StatusDeployed {
				dge := &deploymentGateError{service: svcName}
				render.Stdout().Error(dge.Error())
				return dge
			}
		}
	}

	if err := RunPhases(cfg, reg, workDir, lifecycleCfg.Run.Phases, "run", "run", ctx.Yes, lifecycleCfg.Run.LogEnabled(), ctx.Translator, ctx.Locale); err != nil {
		return err
	}

	if lifecycleCfg.Run.ShowInfo && ctx.ShowInfo != nil {
		if infoErr := ctx.ShowInfo(); infoErr != nil {
			w.Warning(fmt.Sprintf("info display failed: %v", infoErr))
		}
	}

	w.Success(lifecycleCfg.Run.FinalMessage)
	return nil
}

// RunRestart runs the full stop lifecycle then the full run lifecycle with NoUpdate forced to true.
func RunRestart(ctx RunContext) error {
	stopCtx := StopContext{
		Ctx:           ctx.Ctx,
		ConfigPath:    ctx.ConfigPath,
		Yes:           ctx.Yes,
		SkipPreflight: ctx.SkipPreflight,
		ErrOut:        ctx.ErrOut,
	}
	if err := RunStop(stopCtx); err != nil {
		return err
	}
	ctx.NoUpdate = true
	ctx.UpdateMode = ""
	// Restart never notifies — the inner run leg is part of a composite
	// operation, not a user-invoked run. Spec: restart fires zero
	// notifications.
	ctx.SkipNotify = true
	if err := RunRun(ctx); err != nil {
		return err
	}
	// On successful restart, clear the pending restart entry from the journal
	// unless the caller (e.g. executeTogglePlan) owns the final clear itself.
	// Restart-kind covers the whole stack; any pending deploy op for specific services
	// is a separate op and must survive (the restart did not redeploy those services).
	if !ctx.SkipClearPending {
		workDir := filepath.Dir(ctx.ConfigPath)
		statePath := filepath.Join(workDir, journal.DefaultRelPath)
		if clearErr := journal.ClearPendingForKind(statePath, journal.PendingRestart); clearErr != nil {
			slog.Warn("clearing pending restart state after success", "err", clearErr)
		}
	}
	return nil
}

// renderAndSourceDotEnv regenerates devbox/.env from the current config and
// loads its key=value pairs into the process environment, so commands run by
// preflight checks and lifecycle phases observe the freshly-rendered values.
// Mirrors the implicit render-env step at the head of the deploy pipeline.
func renderAndSourceDotEnv(cfg *config.DevboxConfig, workDir string) error {
	envPath := filepath.Join(workDir, ".env")
	if err := envfile.Write(cfg, envPath); err != nil {
		return fmt.Errorf("rendering .env: %w", err)
	}
	if err := deploy.SourceDotEnv(envPath); err != nil {
		return fmt.Errorf("sourcing .env: %w", err)
	}
	return nil
}

// deploymentGateError is returned when the run gate detects an undeployed tracked service.
// It implements ExitCode() int to signal a specific exit code.
type deploymentGateError struct {
	service string
}

func (e *deploymentGateError) Error() string {
	return fmt.Sprintf("service %q must be deployed — run `devbox deploy run` first", e.service)
}

func (e *deploymentGateError) ExitCode() int {
	return 2
}
