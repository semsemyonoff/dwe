package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/git"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// gitProbeFunc and gitPullFFOnlyFunc are package-level variables so tests can
// inject stubs without touching a real git repository.
var gitProbeFunc = git.Probe
var gitPullFFOnlyFunc = git.PullFFOnly

func newRunCmd(flags *rootFlags) *cobra.Command {
	var noUpdate bool
	var updateMode string
	var yes bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the project (full lifecycle: update probe → up → wait → info)",
		Long: `Run the full project lifecycle driven by devbox/lifecycle.yml.

Execution order: optional git update probe → before-run hooks → docker up → docker wait
→ after-run hooks → optional info display → final ready message.

Use 'devbox up' for a bare Docker Compose start without hooks or the update probe.
Use 'devbox docker up' for the low-level compose control plane.`,
		Example: `  devbox run
  devbox run --no-update
  devbox run --update auto`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, flags, noUpdate, updateMode, yes)
		},
	}

	cmd.Flags().BoolVar(&noUpdate, "no-update", false, "disable git update probe regardless of lifecycle.yml config")
	cmd.Flags().StringVar(&updateMode, "update", "", "override update probe mode (prompt|auto|check|off)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	return cmd
}

// resolveUpdateMode applies CLI flag precedence on top of the lifecycle config's effective mode.
// Precedence: --no-update > --update <mode> > LifecycleRunConfig.EffectiveMode()
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

func runRun(cmd *cobra.Command, flags *rootFlags, noUpdate bool, updateMode string, yes bool) error {
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
	if lifecycleCfg.Run == nil {
		return fmt.Errorf("lifecycle.yml has no `run:` section — see devbox/lifecycle.example.yml")
	}

	effectiveMode := resolveUpdateMode(lifecycleCfg.Run, noUpdate, updateMode)

	// Run the git update probe (fetch only when mode is not off).
	fetch := effectiveMode != "off"
	status, err := gitProbeFunc(workDir, fetch)
	if err != nil {
		return fmt.Errorf("git probe: %w", err)
	}
	action, msg := git.Decide(status, git.UpdateMode(effectiveMode), ui.IsInteractiveFn(os.Stdin))

	w := render.Stdout()
	var pulled bool
	switch action {
	case git.ActionWarn:
		w.Warning(msg)
	case git.ActionPullAuto:
		moved, pullErr := gitPullFFOnlyFunc(workDir)
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
			moved, pullErr := gitPullFFOnlyFunc(workDir)
			if pullErr != nil {
				w.Warning(fmt.Sprintf("git pull --ff-only failed: %v", pullErr))
			} else {
				pulled = moved
			}
		}
	default:
		// git.ActionSkip: nothing to do.
	}

	// If pull moved HEAD, reload all configs from disk.
	if pulled {
		cfg, err = config.LoadConfig(flags.configPath)
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

	reg, err := loadCommandRegistry(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	// Execute lifecycle run phases using the (possibly reloaded) config.
	if err := runLifecyclePhases(cfg, reg, workDir, lifecycleCfg.Run.Phases, "run", "run", yes); err != nil {
		return err
	}

	// Show info dashboard if configured.
	if lifecycleCfg.Run.ShowInfo {
		if infoErr := runInfo(cmd, flags); infoErr != nil {
			w.Warning(fmt.Sprintf("info display failed: %v", infoErr))
		}
	}

	w.Success(lifecycleCfg.Run.FinalMessage)
	return nil
}
