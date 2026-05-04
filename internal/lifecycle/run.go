package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/git"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
)

// GitProbeFunc is a package-level variable so tests can inject stubs without
// touching a real git repository.
var GitProbeFunc = git.Probe

// GitPullFFOnlyFunc is a package-level variable so tests can inject a stub
// pull implementation without touching a real git repository.
var GitPullFFOnlyFunc = git.PullFFOnly

// RunContext carries all parameters for the run (and restart) lifecycle entry points.
type RunContext struct {
	ConfigPath string
	NoUpdate   bool
	UpdateMode string
	Yes        bool
	// ShowInfo is called after the run phases complete, when lifecycle.yml has show_info: true.
	// Callers inject this to avoid importing the cobra info renderer into this package.
	// If nil, no info display is attempted.
	ShowInfo func() error
}

// ResolveUpdateMode applies CLI flag precedence on top of the lifecycle config's effective mode.
// Precedence: NoUpdate > UpdateMode flag > LifecycleRunConfig.EffectiveMode()
func ResolveUpdateMode(cfg *config.LifecycleRunConfig, noUpdate bool, updateFlag string) string {
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
func RunRun(ctx RunContext) error {
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
	if lifecycleCfg.Run == nil {
		return fmt.Errorf("lifecycle.yml has no `run:` section — see devbox/lifecycle.example.yml")
	}

	if ctx.UpdateMode != "" && !config.ValidUpdateMode(ctx.UpdateMode) {
		return fmt.Errorf("invalid --update mode %q: must be one of: prompt, auto, check, off", ctx.UpdateMode)
	}

	effectiveMode := ResolveUpdateMode(lifecycleCfg.Run, ctx.NoUpdate, ctx.UpdateMode)

	w := render.Stdout()
	var pulled bool
	if effectiveMode != "off" {
		status, err := GitProbeFunc(workDir, true)
		if err != nil {
			return fmt.Errorf("git probe: %w", err)
		}
		action, msg := git.Decide(status, git.UpdateMode(effectiveMode), ui.IsInteractiveFn(os.Stdin))
		switch action {
		case git.ActionWarn:
			w.Warning(msg)
		case git.ActionPullAuto:
			moved, pullErr := GitPullFFOnlyFunc(workDir)
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
				moved, pullErr := GitPullFFOnlyFunc(workDir)
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
		lifecycleCfg, err = config.LoadLifecycleConfig(lifecyclePath)
		if err != nil {
			return fmt.Errorf("reloading lifecycle config after pull: %w", err)
		}
		if lifecycleCfg.Run == nil {
			return fmt.Errorf("lifecycle.yml has no `run:` section after pull reload")
		}
	}

	reg, err := loadRegistry(ctx.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	if err := RunPhases(cfg, reg, workDir, lifecycleCfg.Run.Phases, "run", "run", ctx.Yes, lifecycleCfg.Run.LogEnabled()); err != nil {
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
		ConfigPath: ctx.ConfigPath,
		Yes:        ctx.Yes,
	}
	if err := RunStop(stopCtx); err != nil {
		return err
	}
	ctx.NoUpdate = true
	ctx.UpdateMode = ""
	return RunRun(ctx)
}

// loadRegistry loads the command registry from devbox/commands/ relative to configPath.
// Returns an empty registry when the directory does not exist.
func loadRegistry(configPath string) (*usercommands.Registry, error) {
	return usercommands.LoadRegistryFromConfigPath(configPath)
}
