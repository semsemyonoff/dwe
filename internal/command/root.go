// Package command wires up the devbox-cli cobra command tree.
package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/project"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/version"

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

// rootFlags holds flags shared across all commands.
type rootFlags struct {
	configPath  string
	projectRoot string
	stylesCfg   *config.StylesConfig // populated by PersistentPreRunE before any command runs
}

// ProjectRoot returns the resolved project root directory. Falls back to
// filepath.Dir(configPath) so tests that construct rootFlags directly without
// going through PersistentPreRunE (which sets projectRoot) still work.
func (f *rootFlags) ProjectRoot() string {
	if f.projectRoot != "" {
		return f.projectRoot
	}
	if f.configPath != "" {
		return filepath.Dir(f.configPath)
	}
	return ""
}

// NewRootCmd builds and returns the root cobra.Command.
func NewRootCmd() *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "devbox",
		Short: "devbox-cli — local development environment toolkit",
		Long: `devbox-cli is the core engine for the devbox local development environment.

It provides config validation, rendering, topology inspection, and project info display.
Run 'devbox' with no arguments to display a compact project summary and available commands.
Run 'devbox info' for the full info dashboard.`,
		// PersistentPreRunE resolves the project root before any subcommand runs.
		// It walks upward from cwd (discovery mode) or uses the explicit -c path,
		// validates schema_version, and populates flags.configPath / flags.projectRoot.
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
				configArg = flags.configPath
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
					flags.configPath = loc.ConfigPath
					flags.projectRoot = loc.Root
					flags.stylesCfg = applyStyles(flags.projectRoot, cmd.ErrOrStderr())
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
						flags.stylesCfg = applyStyles("", cmd.ErrOrStderr())
						return nil
					}
					return err
				}
				// Explicit bad path or schema error — always fatal.
				return err
			}

			flags.configPath = resolved.ConfigPath
			flags.projectRoot = resolved.Root
			flags.stylesCfg = applyStyles(flags.projectRoot, cmd.ErrOrStderr())
			return nil
		},
		// Running 'devbox' with no subcommand shows project summary + help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, flags)
		},
		// Suppress cobra's default "Run 'devbox --help' for more information" footer.
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(
		&flags.configPath,
		"config", "c",
		"",
		"path to devbox.yml (default: auto-discover from cwd upward)",
	)

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

	// Configuration group: services, tools, rendering, validation.
	addCmd(root, groupConfiguration, newServiceCmd(flags))
	addCmd(root, groupConfiguration, newRenderCmd(flags))
	addCmd(root, groupConfiguration, newValidateCmd(flags))

	// Pipelines group: deploy, reset, snapshot.
	addCmd(root, groupPipelines, newDeployCmd(flags))
	addCmd(root, groupPipelines, newResetCmd(flags))
	addCmd(root, groupPipelines, newSnapshotCmd(flags))

	// Advanced group: low-level and diagnostic commands.
	addCmd(root, groupAdvanced, newCommandCmd(flags))
	addCmd(root, groupAdvanced, newDockerCmd(flags))
	addCmd(root, groupAdvanced, newComposeCmd(flags))
	addCmd(root, groupAdvanced, newDocsCmd(flags))

	// Add the built-in Cobra completion command to the Advanced group.
	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
		completionCmd.GroupID = groupAdvanced
	}

	// Internal: hidden Make-compatibility command.
	printCmd := newPrintCmd()
	printCmd.Hidden = true
	root.AddCommand(printCmd)

	return root
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
func runRoot(cmd *cobra.Command, flags *rootFlags) error {
	stylesCfg := flags.stylesCfg // already applied by PersistentPreRunE

	if flags.projectRoot == "" {
		// No project found via discovery — skip summary, show help only.
		return cmd.Help()
	}

	cfg, err := config.LoadConfig(flags.configPath)
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
		statePath := filepath.Join(flags.projectRoot, journal.DefaultRelPath)
		state, err := journal.Load(statePath)
		if err == nil && state != nil {
			// Get tracked services to know the total.
			// Tolerate registry load failures (e.g. command-file syntax errors)
			// so that the root summary remains visible even when commands are broken.
			reg, _ := usercommands.LoadRegistryFromConfigPath(flags.configPath)
			tracked, _, err := deploy.LoadTrackedServices(cfg, reg, flags.projectRoot)
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

		// Print compact project summary followed by a blank separator line.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderSummary(cfg, deploySummary))
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	case errors.Is(err, os.ErrNotExist):
		// Config file not found — not an error, just skip the summary.
	default:
		// Config exists but could not be parsed — surface the error.
		return err
	}

	// Always show help regardless of whether config loaded.
	return cmd.Help()
}
