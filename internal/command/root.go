// Package command wires up the devbox-cli cobra command tree.
package command

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/command/cmdctx"
	cmdDeploy "devbox-cli/internal/command/deploy"
	cmdRender "devbox-cli/internal/command/render"
	cmdService "devbox-cli/internal/command/service"
	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/project"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/version"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/userconfig"

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
	addCmd(root, groupCore, newInfoCmd(flags))
	addCmd(root, groupCore, newVersionCmd())

	// Environment group: lifecycle and shell access.
	addCmd(root, groupEnvironment, newRunCmd(flags))
	addCmd(root, groupEnvironment, newStopCmd(flags))
	addCmd(root, groupEnvironment, newRestartCmd(flags))
	addCmd(root, groupEnvironment, newShellCmd(flags))
	addCmd(root, groupEnvironment, newStatusCmd(flags))
	addCmd(root, groupEnvironment, newPromptCmd(flags))

	// Configuration group: services, tools, rendering, validation.
	root.AddCommand(cmdService.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdRender.NewCmd(groupConfiguration, flags))
	root.AddCommand(newValidateCmd(groupConfiguration, flags))

	// Pipelines group: deploy, reset, snapshot.
	root.AddCommand(cmdDeploy.NewCmd(groupPipelines, flags))
	addCmd(root, groupPipelines, newResetCmd(flags))
	addCmd(root, groupPipelines, newSnapshotCmd(flags))

	// Advanced group: low-level and diagnostic commands.
	addCmd(root, groupAdvanced, newCommandCmd(flags))
	addCmd(root, groupAdvanced, newDockerCmd(flags))
	addCmd(root, groupAdvanced, newComposeCmd(flags))
	addCmd(root, groupAdvanced, newDocsCmd(flags))

	// Add the built-in Cobra completion command to the Advanced group,
	// then attach install/uninstall subcommands.
	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
		completionCmd.GroupID = groupAdvanced
		completionCmd.AddCommand(newInstallCompletionCmd())
		completionCmd.AddCommand(newUninstallCompletionCmd())
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
	ucfg, err := userconfig.Load(flags.Root)
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

// addCmd assigns a group ID to cmd and adds it to parent.
func addCmd(parent *cobra.Command, groupID string, cmd *cobra.Command) {
	cmd.GroupID = groupID
	parent.AddCommand(cmd)
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
