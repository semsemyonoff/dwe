// Package cli wires up the dwe cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cmdBridge "github.com/semsemyonoff/dwe/internal/cli/bridge"
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	cmdCommand "github.com/semsemyonoff/dwe/internal/cli/command"
	"github.com/semsemyonoff/dwe/internal/cli/completion"
	cmdCompose "github.com/semsemyonoff/dwe/internal/cli/compose"
	cmdDeploy "github.com/semsemyonoff/dwe/internal/cli/deploy"
	cmdDocker "github.com/semsemyonoff/dwe/internal/cli/docker"
	cmdDocs "github.com/semsemyonoff/dwe/internal/cli/docs"
	cmdInfo "github.com/semsemyonoff/dwe/internal/cli/info"
	cmdLifecycle "github.com/semsemyonoff/dwe/internal/cli/lifecycle"
	cmdLogs "github.com/semsemyonoff/dwe/internal/cli/logs"
	cmdPrompt "github.com/semsemyonoff/dwe/internal/cli/prompt"
	cmdRender "github.com/semsemyonoff/dwe/internal/cli/render"
	cmdScaffold "github.com/semsemyonoff/dwe/internal/cli/scaffold"
	cmdService "github.com/semsemyonoff/dwe/internal/cli/service"
	cmdShell "github.com/semsemyonoff/dwe/internal/cli/shell"
	cmdSnapshot "github.com/semsemyonoff/dwe/internal/cli/snapshot"
	cmdStatus "github.com/semsemyonoff/dwe/internal/cli/status"
	cmdTest "github.com/semsemyonoff/dwe/internal/cli/test"
	cmdValidate "github.com/semsemyonoff/dwe/internal/cli/validate"
	cmdVars "github.com/semsemyonoff/dwe/internal/cli/vars"
	cmdVersion "github.com/semsemyonoff/dwe/internal/cli/version"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/project"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
	"github.com/semsemyonoff/dwe/internal/shared/version"

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

// NewRootCmdWithFlags builds and returns the root cobra.Command together with
// the RootFlags that it owns. Use this in main.go when you need to thread
// *RootFlags into a closure (e.g. the JSON error handler). All other callers
// use NewRootCmd() and discard the flags pointer.
func NewRootCmdWithFlags() (*cobra.Command, *cmdctx.RootFlags) {
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
	root.AddCommand(cmdVersion.NewCmd(groupCore, flags))

	// Environment group: lifecycle and shell access.
	root.AddCommand(cmdLifecycle.NewRunCmd(groupEnvironment, flags))
	root.AddCommand(cmdLifecycle.NewStopCmd(groupEnvironment, flags))
	root.AddCommand(cmdLifecycle.NewRestartCmd(groupEnvironment, flags))
	root.AddCommand(cmdShell.NewCmd(groupEnvironment, flags))
	root.AddCommand(cmdStatus.NewCmd(groupEnvironment, flags))
	root.AddCommand(cmdLogs.NewCmd(groupEnvironment, flags))
	root.AddCommand(cmdPrompt.NewCmd(groupEnvironment, flags))

	// Configuration group: services, tools, rendering, validation.
	root.AddCommand(cmdScaffold.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdService.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdRender.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdValidate.NewCmd(groupConfiguration, flags))
	root.AddCommand(cmdVars.NewCmd(groupConfiguration, flags))

	// Pipelines group: deploy, reset, snapshot.
	root.AddCommand(cmdDeploy.NewCmd(groupPipelines, flags))
	root.AddCommand(cmdLifecycle.NewResetCmd(groupPipelines, flags))
	root.AddCommand(cmdSnapshot.NewCmd(groupPipelines, flags))
	root.AddCommand(cmdTest.NewCmd(groupPipelines, flags))

	// Advanced group: low-level and diagnostic commands.
	root.AddCommand(cmdCommand.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdDocker.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdCompose.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdBridge.NewCmd(groupAdvanced, flags))
	root.AddCommand(cmdDocs.NewCmd(groupAdvanced, flags))

	// Add the built-in Cobra completion command to the Advanced group,
	// then attach install/uninstall subcommands.
	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
		completionCmd.GroupID = groupAdvanced
		completion.AttachInstallUninstall(completionCmd, flags)
	}

	// Container command policy: in container context blocked commands
	// disappear from help listings and shell completion; the run-time gate
	// lives in the root PersistentPreRunE.
	applyBridgeContainerVisibility(root)

	return root, flags
}

// NewRootCmd builds and returns the root cobra.Command.
func NewRootCmd() *cobra.Command {
	root, _ := NewRootCmdWithFlags()
	return root
}

// ApplyHelpBranding rebuilds the root command's Long help text with accent-
// coloured emphasis on the product name "DWE" and the leading letters of its
// expansion. Call after styles.ApplyStyles so the resolved accent palette is
// in effect; safe to call anytime afterwards. Commands that consume Long as
// plain text (docs generators) should strip ANSI before writing.
func ApplyHelpBranding(root *cobra.Command) {
	if root == nil {
		return
	}
	a := styles.AccentStyle()
	root.Long = fmt.Sprintf(
		"%s (%sev %sorkspace %sngine) — CLI for managing Docker-based local development environments.\n\nIt provides config validation, rendering, topology inspection, and project info display.",
		a.Render("DWE"),
		a.Render("D"),
		a.Render("W"),
		a.Render("E"),
	)
}

// rootLongDescription is the plain-text Long help body for the root command.
// ApplyHelpBranding rebuilds this with accent-coloured emphasis after the
// styles palette is resolved; this constant is the fallback rendered when
// styles have not been loaded (e.g. during docs generation).
const rootLongDescription = `DWE (Dev Workspace Engine) — CLI for managing Docker-based local development environments.

It provides config validation, rendering, topology inspection, and project info display.`

func initRootCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dwe",
		Short: "Manage Docker-based local development environments",
		Long:  rootLongDescription,
		// PersistentPreRunE resolves the project root before any subcommand runs.
		// It walks upward from cwd (discovery mode) or uses the explicit -c path,
		// validates the config, and populates flags.ConfigPath / flags.Root.
		// Commands allowlisted by allowedWithoutProject (init, version, prompt,
		// bridge daemon, completion, docs) are allowed through when no project is
		// found via discovery.
		// The validate command bypasses schema validation so it can report schema errors
		// as diagnostics instead of aborting before the validators run.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// (0) Configure the diagnostic sink as early as possible so that
			// any subsequent setup (config load, styles) can emit trace lines.
			// At Debug, route Go's default slog logger through the same sink so
			// existing slog.Debug records join the firehose; at lower levels the
			// handler is NOT installed, so Warn/Error behaviour is unchanged.
			lvl := levelFrom(flags.Verbose, flags.Debug, os.Getenv("DWE_DEBUG"))
			trace.Configure(os.Stderr, lvl)
			installSlogHandler(lvl)

			// (1) Validate --output before anything else so invalid values are
			// rejected cleanly before lipgloss or fang styles are applied.
			switch flags.Output {
			case "", "text", "json":
				// valid
			default:
				return cmdctx.Err("invalid_output", "unknown output format: "+flags.Output).
					WithHint("valid values: text, json")
			}
			if flags.Pretty && flags.Output != "json" {
				return cmdctx.Err("invalid_output", "--pretty requires --output json").
					WithHint("use --output json")
			}

			// (2) JSON mode side-effects: suppress ANSI sequences and cobra noise.
			if flags.Output == "json" {
				_ = os.Setenv("NO_COLOR", "1")
				cmd.Root().SilenceErrors = true
				cmd.Root().SilenceUsage = true
			}

			// (2b) Container command policy: when forked by the bridge daemon
			// (DWE_INVOKED_FROM=container) only allowlisted commands proceed
			// (default-deny). Before project resolution, so blocked commands
			// fail with the policy error regardless of project state.
			if err := bridgePolicyGate(cmd); err != nil {
				return err
			}

			// (3) Detect whether --config/-c was explicitly supplied.
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
					return cmdctx.ErrWrap("project_invalid_config", err)
				}
				if found {
					flags.ConfigPath = loc.ConfigPath
					flags.Root = loc.Root
					flags.StylesCfg = applyStyles(flags.Root, cmd.ErrOrStderr())

					resolveLocalization(flags)
					return nil
				}
				// Locate miss — validate always requires a project.
				return cmdctx.ErrWrap("project_not_found", project.ErrNotFound).
					WithHint("run from a DWE project directory or pass --config")
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
					return cmdctx.ErrWrap("project_not_found", err).
						WithHint("run from a DWE project directory or pass --config")
				}
				// Explicit bad path or schema error — always fatal.
				return cmdctx.ErrWrap("project_invalid_config", err)
			}

			flags.ConfigPath = resolved.ConfigPath
			flags.Root = resolved.Root
			flags.StylesCfg = applyStyles(flags.Root, cmd.ErrOrStderr())

			resolveLocalization(flags)
			return nil
		},
		// Running 'dwe' with no subcommand shows project summary + help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, flags)
		},
	}
	cmd.PersistentFlags().StringVar(
		&flags.ConfigPath,
		"config",
		"",
		"path to workspace.yml (default: auto-discover from cwd upward)",
	)
	cmd.PersistentFlags().StringVarP(
		&flags.Output,
		"output", "o",
		"text",
		"output format: text or json",
	)
	cmd.PersistentFlags().BoolVar(
		&flags.Pretty,
		"pretty",
		false,
		"pretty-print JSON output (only with --output json)",
	)
	cmd.PersistentFlags().BoolVarP(
		&flags.Verbose,
		"verbose", "v",
		false,
		"echo executed commands and key pipeline decisions to stderr",
	)
	cmd.PersistentFlags().BoolVar(
		&flags.Debug,
		"debug",
		false,
		"emit the full diagnostic firehose to stderr (superset of --verbose; also DWE_DEBUG=1)",
	)

	return cmd
}

// levelFrom maps the diagnostic flags and DWE_DEBUG env to a trace level.
// The --debug flag or a truthy DWE_DEBUG selects Debug; --verbose selects
// Verbose; otherwise Off. The flag wins on conflict, but since either source
// only ever raises the level, an explicit --debug and a truthy DWE_DEBUG agree.
func levelFrom(verbose, debug bool, dweDebug string) trace.Level {
	if debug || debugEnvTruthy(dweDebug) {
		return trace.LevelDebug
	}
	if verbose {
		return trace.LevelVerbose
	}
	return trace.LevelOff
}

// slogState tracks the global slog handler swap so it can be reverted. The CLI
// is normally one-shot (PersistentPreRunE runs once per process), but embedded
// or repeated Execute callers may invoke commands at different levels in the
// same process; without a revert, a prior --debug run would leave the
// trace handler installed — and because it emits every record regardless of the
// trace level, slog.Debug output would keep leaking after the flag is gone.
var (
	slogPristineOnce sync.Once
	slogPristine     *slog.Logger // Go's default logger captured before any swap
	slogTraceActive  bool         // true while the trace handler is the default
)

// installSlogHandler routes Go's default slog logger through the trace sink,
// but ONLY at Debug. Returning true reports that the handler was installed.
// At Off/Verbose the Go default handler is restored if a prior call installed
// the trace handler, and otherwise left untouched — so existing slog.Warn/Error
// output reaches stderr unchanged (the no-regression contract) and a previous
// --debug run does not leak into a later non-debug run in the same process.
func installSlogHandler(lvl trace.Level) bool {
	slogPristineOnce.Do(func() { slogPristine = slog.Default() })

	if lvl == trace.LevelDebug {
		slog.SetDefault(slog.New(trace.NewSlogHandler()))
		slogTraceActive = true
		return true
	}
	if slogTraceActive {
		slog.SetDefault(slogPristine)
		slogTraceActive = false
	}
	return false
}

// debugEnvTruthy reports whether DWE_DEBUG is set to a truthy value. It is
// truthy when set and not one of the recognised falsey tokens (case-insensitive).
func debugEnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
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
// These commands are allowed through when no workspace.yml is found via upward discovery.
// Note: explicit -c /bad/path is still fatal even for these commands — the allowlist
// only catches project.ErrNotFound (discovery miss), not os.ErrNotExist or schema errors.
func allowedWithoutProject(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "dwe" ||
		path == "dwe init" ||
		path == "dwe version" ||
		path == "dwe prompt" ||
		// The daemon takes everything from --project-root; cwd-based
		// discovery must not gate it (it is spawned detached).
		path == "dwe bridge daemon" ||
		strings.HasPrefix(path, "dwe completion") ||
		strings.HasPrefix(path, "dwe docs")
}

// isValidateCommand returns true if cmd is the validate command or a descendant.
// The validate command is special: it must bypass schema validation so it can
// report schema errors as diagnostics instead of aborting before the validator runs.
func isValidateCommand(cmd *cobra.Command) bool {
	path := cmd.CommandPath()
	return path == "dwe validate" || strings.HasPrefix(path, "dwe validate ")
}

// applyStyles loads workspace/styles.yml from projectRoot, applies the palette to ui,
// and warns on error. projectRoot is the directory containing workspace.yml; an empty
// string means no project was found and styles fall back to defaults silently.
// errW is used for warning output so that Cobra stderr redirection is respected.
// Returns the loaded config (never nil).
func applyStyles(projectRoot string, errW io.Writer) *config.StylesConfig {
	if projectRoot == "" {
		// No project root — apply defaults silently.
		stylesCfg := &config.StylesConfig{}
		styles.ApplyStyles(stylesCfg)
		return stylesCfg
	}
	stylesPath := filepath.Join(projectRoot, "workspace", "styles.yml")
	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	styles.ApplyStyles(stylesCfg)
	if err != nil {
		sharedrender.NewWriter(errW).Warning("styles.yml: " + err.Error())
	}
	return stylesCfg
}

// rootProjectJSON is the project summary emitted in JSON mode.
type rootProjectJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Root    string `json:"root"`
}

// rootDeploySummaryJSON is the deploy summary emitted in JSON mode.
type rootDeploySummaryJSON struct {
	Deployed int    `json:"deployed"`
	Total    int    `json:"total"`
	Status   string `json:"status"`
}

// rootPendingOpJSON is a single pending operation in JSON mode.
type rootPendingOpJSON struct {
	Kind     string   `json:"kind"`
	Services []string `json:"services,omitempty"`
}

// rootPendingJSON is the pending-apply summary emitted in JSON mode.
type rootPendingJSON struct {
	Operations []rootPendingOpJSON `json:"operations"`
	ConfigHash string              `json:"config_hash"`
	CreatedAt  string              `json:"created_at,omitempty"`
}

// rootJSON is the top-level JSON payload for `dwe` (no subcommand) in JSON mode.
type rootJSON struct {
	Project       *rootProjectJSON       `json:"project"`
	DeploySummary *rootDeploySummaryJSON `json:"deploy_summary"`
	Pending       *rootPendingJSON       `json:"pending"`
}

// runRootJSON handles `dwe` with no subcommand in JSON output mode.
// It emits a machine-readable project summary and returns without printing help text.
func runRootJSON(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	if flags.Root == "" {
		return cmdctx.WriteData(flags, cmd, rootJSON{}, func(r rootJSON) string { return "" })
	}

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}

	data := rootJSON{}

	if err == nil {
		data.Project = &rootProjectJSON{
			Name:    cfg.Project.FullName(),
			Version: version.Version,
			Root:    flags.Root,
		}

		statePath := filepath.Join(flags.Root, journal.DefaultRelPath)
		state, serr := journal.Load(statePath)
		if serr == nil && state != nil {
			if state.Pending != nil {
				ops := make([]rootPendingOpJSON, 0, len(state.Pending.Operations))
				for _, op := range state.Pending.Operations {
					ops = append(ops, rootPendingOpJSON{
						Kind:     string(op.Kind),
						Services: op.Services,
					})
				}
				p := &rootPendingJSON{
					Operations: ops,
					ConfigHash: state.Pending.ConfigHash,
				}
				if !state.Pending.CreatedAt.IsZero() {
					p.CreatedAt = state.Pending.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				data.Pending = p
			}

			reg, _ := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			tracked, _, terr := deploy.LoadTrackedServices(cfg, reg, flags.Root)
			if terr == nil && len(tracked) > 0 {
				projectStatus := ""
				if state.Project != nil {
					projectStatus = string(state.Project.Status)
				}
				data.DeploySummary = &rootDeploySummaryJSON{
					Deployed: countDeployedServices(state, tracked),
					Total:    len(tracked),
					Status:   projectStatus,
				}
			}
		}
	}

	return cmdctx.WriteData(flags, cmd, data, func(r rootJSON) string { return "" })
}

// countDeployedServices counts how many of the tracked services carry a
// StatusDeployed entry in the journal state. Shared by the text and JSON root
// summary builders.
func countDeployedServices(state *journal.ProjectState, tracked []string) int {
	n := 0
	for _, svcName := range tracked {
		if svc, ok := state.Services[svcName]; ok && svc != nil && svc.Status == journal.StatusDeployed {
			n++
		}
	}
	return n
}

// runRoot is the handler for `dwe` with no subcommand.
// It prints an ASCII header and compact project summary (when a config is
// found), followed by the Cobra/Fang help output.
func runRoot(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	if flags.Output == "json" {
		return runRootJSON(cmd, flags)
	}

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
		header := render.Brand{
			Project: cfg.Project.FullName(),
			Version: version.Version,
		}
		if stylesCfg != nil {
			header.Tagline = stylesCfg.Header.Tagline
			header.Lines = stylesCfg.Header.Lines
			header.Font = stylesCfg.Header.Font
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), render.BrandHeader(header))
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
				// Build summary view.
				var projectStatus journal.Status
				if state.Project != nil {
					projectStatus = state.Project.Status
				}
				deploySummary = &statusview.DeploySummary{
					Deployed:      countDeployedServices(state, tracked),
					Total:         len(tracked),
					ProjectStatus: projectStatus,
				}
			}
		}
		// Silently skip deploy summary if state load fails — not critical for summary.

		// Print compact project summary; if pending work is recorded, surface
		// the warning banner directly underneath so the user sees deferred work
		// before scanning the help output.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), render.Summary(cfg, deploySummary))
		if banner := render.PendingBanner(pending); banner != "" {
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
