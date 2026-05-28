// Package command wires up the devbox-cli cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	cmdCommand "devbox-cli/internal/cli/command"
	"devbox-cli/internal/cli/completion"
	cmdCompose "devbox-cli/internal/cli/compose"
	cmdDeploy "devbox-cli/internal/cli/deploy"
	cmdDocker "devbox-cli/internal/cli/docker"
	cmdDocs "devbox-cli/internal/cli/docs"
	cmdInfo "devbox-cli/internal/cli/info"
	cmdLifecycle "devbox-cli/internal/cli/lifecycle"
	cmdPrompt "devbox-cli/internal/cli/prompt"
	cmdRender "devbox-cli/internal/cli/render"
	cmdService "devbox-cli/internal/cli/service"
	cmdShell "devbox-cli/internal/cli/shell"
	cmdSnapshot "devbox-cli/internal/cli/snapshot"
	cmdStatus "devbox-cli/internal/cli/status"
	cmdValidate "devbox-cli/internal/cli/validate"
	cmdVersion "devbox-cli/internal/cli/version"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/project"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/ui/statusview"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/version"

	"github.com/spf13/cobra"
)

// Command group IDs.
const (
	groupCore          = "core"
	groupEnvironment   = "environment"
	groupConfiguration = "configuration"
	groupPipelines     = "pipelines"
	groupAdvanced      = "advanced"
)

const defaultLocale = "en"

// NewRootCmd builds and returns the root cobra.Command.
func NewRootCmd() *cobra.Command {
	flags := &cmdctx.RootFlags{}

	root := initRootCmd(flags)

	// Define command groups for organized help output.
	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core Commands:"},
		&cobra.Group{ID: groupEnvironment, Title: "Environment Commands:"},
		&cobra.Group{ID: groupConfiguration, Title: "Configuration Commands:"},
		&cobra.Group{ID: groupPipelines, Title: "Pipeline Commands:"},
		&cobra.Group{ID: groupAdvanced, Title: "Advanced Commands:"},
	)

	// Core group: project info and version.
	root.AddCommand(cmdInfo.NewCmd(groupCore, flags))
	root.AddCommand(cmdVersion.NewCmd(groupCore))

	// Environment group: lifecycle and shell access.
	root.AddCommand(cmdLifecycle.NewRunCmd(groupEnvironment, flags))
	root.AddCommand(cmdLifecycle.NewStopCmd(groupEnvironment, flags))
	root.AddCommand(cmdLifecycle.NewRestartCmd(groupEnvironment, flags))
	root.AddCommand(cmdShell.NewCmd(groupEnvironment, flags))
	root.AddCommand(cmdStatus.NewCmd(groupEnvironment, flags))
	root.AddCommand(cmdPrompt.NewCmd(groupEnvironment, flags))

	// Configuration group: services, tools, rendering, validation.
	root.AddCommand(cmdService.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdRender.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdValidate.NewCmd(groupConfiguration, flags))

	// Pipelines group: deploy, reset, snapshot.
	root.AddCommand(cmdDeploy.NewCmd(groupPipelines, flags))
	root.AddCommand(cmdLifecycle.NewResetCmd(groupPipelines, flags))
	root.AddCommand(cmdSnapshot.NewCmd(groupPipelines, flags))

	// Advanced group: low-level and diagnostic commands.
	root.AddCommand(cmdCommand.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdDocker.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdCompose.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdDocs.NewCmd(groupAdvanced, flags))

	// Add the built-in Cobra completion command to the Advanced group,
	// then attach install/uninstall subcommands.
	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
		completionCmd.GroupID = groupAdvanced
		completion.AttachInstallUninstall(completionCmd, flags)
	}

	return root
}

func initRootCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devbox",
		Short: "devbox — local development environment toolkit",
		Long: `devbox is the core engine for the devbox local development environment.

It provides config validation, rendering, topology inspection, and project info display.`,
		// PersistentPreRunE resolves the project root before any subcommand runs.
		// It walks upward from cwd (discovery mode) or uses the explicit -c path,
		// validates schema_version, and populates flags.ConfigPath / flags.Root.
		// Commands that work without a project (version, completion, print, docs) are
		// allowed through when no project is found via discovery.
		// The validate command bypasses schema validation so it can report schema errors
		// as diagnostics instead of aborting before the validators run.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Detect whether --config/-c was explicitly supplied.
			// Read from root to be unambiguous: PersistentPreRunE receives the leaf command.
			explicit := cmd.Root().PersistentFlags().Lookup("config").Changed

			var configArg string
			if explicit {
				configArg = flags.ConfigPath
			}

			// For validate commands, use Locate (no schema check) instead of Resolve.
			if isValidateCommand(cmd) {
				loc, found, err := project.Locate(configArg)
				if err != nil {
					// project.Locate never returns ErrNotFound as an error (discovery miss
					// returns (zero, false, nil)); propagate any real error immediately.
					return err
				}
				if found {
					flags.ConfigPath = loc.ConfigPath
					flags.Root = loc.Root
					flags.StylesCfg = applyStyles(flags.Root, cmd.ErrOrStderr())

					resolveLocalization(flags)
					return nil
				}
				// Locate miss — validate always requires a project.
				return project.ErrNotFound
			}

			// Normal commands: use Resolve (with schema validation).
			resolved, err := project.Resolve(configArg)
			if err != nil {
				if errors.Is(err, project.ErrNotFound) {
					// Discovery miss — only allowlisted commands proceed without a project.
					if allowedWithoutProject(cmd) {
						flags.StylesCfg = applyStyles("", cmd.ErrOrStderr())
						flags.I18n, _ = i18n.Load("")
						if flags.I18n == nil {
							flags.I18n = &i18n.Store{}
						}
						flags.Locale = defaultLocale
						return nil
					}
					return err
				}
				// Explicit bad path or schema error — always fatal.
				return err
			}

			flags.ConfigPath = resolved.ConfigPath
			flags.Root = resolved.Root
			flags.StylesCfg = applyStyles(flags.Root, cmd.ErrOrStderr())

			resolveLocalization(flags)
			return nil
		},
		// Running 'devbox' with no subcommand shows project summary + help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, flags)
		},
	}
	cmd.PersistentFlags().StringVarP(
		&flags.ConfigPath,
		"config", "c",
		"",
		"path to devbox.yml (default: auto-discover from cwd upward)",
	)

	return cmd
}

func resolveLocalization(flags *cmdctx.RootFlags) {
	var cfgLang string
	ucfg, err := userpkg.Load(flags.Root)
	if err != nil {
		slog.Warn("userconfig load failed; locale falls through to $LANG/en", "err", err)
	} else if ucfg != nil {
		cfgLang = ucfg.Language
	}

	store, err := i18n.Load(flags.Root)
	if err != nil {
		slog.Warn("i18n load failed; UI strings will use English fallbacks", "err", err)
		store = &i18n.Store{}
	}
	if store == nil {
		store = &i18n.Store{}
	}
	flags.I18n = store
	flags.Locale = store.ClampLocale(i18n.ResolveLocale("", cfgLang, os.Getenv("LANG")))
}

// allowedWithoutProject returns true for commands that can run without a project.
// These commands are allowed through when no devbox.yml is found via upward discovery.
// Note: explicit -c /bad/path is still fatal even for these commands — the allowlist
// only catches project.ErrNotFound (discovery miss), not os.ErrNotExist or schema errors.
func allowedWithoutProject(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "devbox" ||
		path == "devbox version" ||
		path == "devbox prompt" ||
		strings.HasPrefix(path, "devbox completion") ||
		strings.HasPrefix(path, "devbox print") ||
		strings.HasPrefix(path, "devbox docs")
}

// isValidateCommand returns true if cmd is the validate command or a descendant.
// The validate command is special: it must bypass schema validation so it can
// report schema errors as diagnostics instead of aborting before the validator runs.
func isValidateCommand(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "devbox validate" || strings.HasPrefix(path, "devbox validate ")
}

// applyStyles loads devbox/styles.yml from projectRoot, applies the palette to ui,
// and warns on error. projectRoot is the directory containing devbox.yml; an empty
// string means no project was found and styles fall back to defaults silently.
// errW is used for warning output so that Cobra stderr redirection is respected.
// Returns the loaded config (never nil).
func applyStyles(projectRoot string, errW io.Writer) *config.StylesConfig {
	if projectRoot == "" {
		// No project root — apply defaults silently.
		stylesCfg := &config.StylesConfig{}
		ui.ApplyStyles(stylesCfg)
		return stylesCfg
	}
	stylesPath := filepath.Join(projectRoot, "devbox", "styles.yml")
	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	ui.ApplyStyles(stylesCfg)
	if err != nil {
		render.NewWriter(errW).Warning("styles.yml: " + err.Error())
	}
	return stylesCfg
}

// runRoot is the handler for `devbox` with no subcommand.
// It prints an ASCII header and compact project summary (when a config is
// found), followed by the Cobra/Fang help output.
func runRoot(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	stylesCfg := flags.StylesCfg // already applied by PersistentPreRunE

	if flags.Root == "" {
		// No project found via discovery — skip summary, show help only.
		return cmd.Help()
	}

	cfg, err := config.LoadConfig(flags.ConfigPath)
	switch {
	case err == nil:
		// Always render the branded identity line; the ASCII art block inside
		// the helper is gated by header.lines.
		header := ui.BrandHeader{
			Project: cfg.Project.FullName(),
			Version: version.Version,
		}
		if stylesCfg != nil {
			header.Tagline = stylesCfg.Header.Tagline
			header.Lines = stylesCfg.Header.Lines
			header.Font = stylesCfg.Header.Font
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), ui.RenderBrandHeader(header))
		_, _ = fmt.Fprintln(cmd.OutOrStdout())

		// Load deploy state and build deploy summary.
		var deploySummary *statusview.DeploySummary
		var pending *journal.PendingApply
		statePath := filepath.Join(flags.Root, journal.DefaultRelPath)
		state, err := journal.Load(statePath)
		if err == nil && state != nil {
			pending = state.Pending
			// Get tracked services to know the total.
			// Tolerate registry load failures (e.g. command-file syntax errors)
			// so that the root summary remains visible even when commands are broken.
			reg, _ := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			tracked, _, err := deploy.LoadTrackedServices(cfg, reg, flags.Root)
			if err == nil && len(tracked) > 0 {
				// Count how many tracked services are deployed.
				deployedCount := 0
				for _, svcName := range tracked {
					if svc, ok := state.Services[svcName]; ok && svc != nil && svc.Status == journal.StatusDeployed {
						deployedCount++
					}
				}
				// Build summary view.
				deploySummary = &statusview.DeploySummary{
					Deployed:      deployedCount,
					Total:         len(tracked),
					ProjectStatus: state.Project.Status,
				}
			}
		}
		// Silently skip deploy summary if state load fails — not critical for summary.

		// Print compact project summary; if pending work is recorded, surface
		// the warning banner directly underneath so the user sees deferred work
		// before scanning the help output.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderSummary(cfg, deploySummary))
		if banner := ui.RenderPendingBanner(pending); banner != "" {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), banner)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	case errors.Is(err, os.ErrNotExist):
		// Config file not found — not an error, just skip the summary.
	default:
		// Config exists but could not be parsed — surface the error.
		return err
	}

	return cmd.Help()
}
