// Package status hosts the `dwe status` command tree.
package status

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statustui"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
	"github.com/semsemyonoff/dwe/internal/shared/trace"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// Test seams for TTY detection, TUI dispatch, container probing, and the
// bridge-daemon ensure. Tests override these via assignment to suppress TUI
// or stub Docker without spawning processes (the real bridge.Ensure spawns a
// detached daemon via os.Executable() — the documented recursion hazard).
var (
	isTerminalFn     = term.IsTerminal
	runStatusTUIFn   = statustui.Run
	serviceRunningFn = stack.ServiceRunning
	bridgeEnsureFn   = bridge.Ensure
)

// ensureBridgeDaemon is the best-effort bridge-daemon ensure of the
// top-level `dwe status` (design D6): status already asserts "is the stack
// alive" cheaply, so it revives a dead daemon in passing. It acquires NO
// project locks and runs NO preflight — status stays read-only (the daemon
// pidfile flock is a separate, bridge-private lock). Errors are swallowed
// and surfaced only under --debug via trace.
func ensureBridgeDaemon(ctx context.Context, sc *statusContext) {
	if !bridge.AnyBridgeEnabled(sc.Cfg) {
		return
	}
	if _, err := bridgeEnsureFn(bridge.EnsureConfig{ProjectRoot: sc.ProjectRoot}); err != nil {
		trace.Debugf(ctx, "bridge: best-effort daemon ensure failed: %v", err)
	}
}

// section identifies one of the renderable status sections used by the
// default `status` command and by the per-section subcommands.
type section int

const (
	sectionApps section = iota + 1
	sectionTools
	sectionInfra
	sectionDeploy
	sectionTopology
	sectionGit
	sectionDaemons
)

// defaultSectionOrder is the single source of truth for both the default
// status orchestrator order and the --no-* flag set.
var defaultSectionOrder = []section{
	sectionApps,
	sectionTools,
	sectionInfra,
	sectionDeploy,
	sectionTopology,
	sectionGit,
	sectionDaemons,
}

// statusContext bundles everything a status subcommand needs. Built lazily
// per command execution via loadStatusContext — never via PersistentPreRunE
// (which would shadow the root hook; see CLAUDE.md).
type statusContext struct {
	Cfg         *config.DweConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.ServiceDeployConfig
	DockerCfg   *config.DockerConfig
	Topo        map[string][]string
	TopoStatus  map[string]render.NodeStatus
	IsRunning   stack.ContainerCheckFn
	ProjectRoot string
	ConfigPath  string
}

// loadStatusContext loads the full status context. Called from each
// subcommand's RunE. errW receives warnings (e.g. corrupt state file).
func loadStatusContext(flags *cmdctx.RootFlags, errW io.Writer) (*statusContext, error) {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	statePath := filepath.Join(flags.ProjectRoot(), journal.DefaultRelPath)
	state, err := journal.Load(statePath)
	if err != nil {
		if flags.Output != "json" {
			_, _ = fmt.Fprintf(errW, "warning: deploy state unreadable (%v); deploy section suppressed\n", err)
		}
		state = nil
	}
	reg, _ := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	tracked, svcDeploys, err := deploy.LoadTrackedServices(cfg, reg, flags.ProjectRoot())
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	projectName, dockerCfg, err := stack.ResolveProjectAndDocker(flags.ConfigPath, cfg)
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	topo, topoStatus := stack.ResolveTopology(cfg, dockerCfg, projectName, flags.ProjectRoot())
	dockerBin := config.DockerBin(cfg)
	// Thread docker.yml process_env (DOCKER_HOST / DOCKER_CONTEXT) into the probe
	// so status targets the same daemon as lifecycle commands and `dwe shell`.
	var processEnv []string
	if dockerCfg != nil {
		processEnv = docker.MergeEnv(dockerCfg.ProcessEnv)
	}
	isRunning := func(service string) bool {
		return serviceRunningFn(projectName, service, dockerBin, processEnv)
	}
	return &statusContext{
		Cfg:         cfg,
		State:       state,
		Tracked:     tracked,
		SvcDeploys:  svcDeploys,
		DockerCfg:   dockerCfg,
		Topo:        topo,
		TopoStatus:  topoStatus,
		IsRunning:   isRunning,
		ProjectRoot: flags.ProjectRoot(),
		ConfigPath:  flags.ConfigPath,
	}, nil
}

func (sc *statusContext) normalisedDockerCfg() *config.DockerConfig {
	if sc.DockerCfg == nil {
		return &config.DockerConfig{}
	}
	return sc.DockerCfg
}

func (sc *statusContext) statusInput() stack.StatusInput {
	return stack.StatusInput{
		Cfg:        sc.Cfg,
		IsRunning:  sc.IsRunning,
		Topo:       sc.Topo,
		TopoStatus: sc.TopoStatus,
		State:      sc.State,
		SvcDeploys: sc.SvcDeploys,
		Tracked:    sc.Tracked,
	}
}

type noSectionFlags struct {
	noApps     bool
	noTools    bool
	noInfra    bool
	noDeploy   bool
	noTopology bool
	noGit      bool
	noDaemons  bool
}

func (f *noSectionFlags) isSuppressed(s section) bool {
	switch s {
	case sectionApps:
		return f.noApps
	case sectionTools:
		return f.noTools
	case sectionInfra:
		return f.noInfra
	case sectionDeploy:
		return f.noDeploy
	case sectionTopology:
		return f.noTopology
	case sectionGit:
		return f.noGit
	case sectionDaemons:
		return f.noDaemons
	}
	return false
}

func shouldUseTUI(noTUI bool, no *noSectionFlags) bool {
	if noTUI {
		return false
	}
	if no.noApps || no.noTools || no.noInfra || no.noDeploy ||
		no.noTopology || no.noGit || no.noDaemons {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminalFn(os.Stdout.Fd())
}

// NewCmd creates the `dwe status` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	noFlags := &noSectionFlags{}
	var noTUI bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stack health and per-section status (read-only)",
		Long: `Display the running status of the entire dwe stack.

The default view prints a health indicator followed by all sections in order:
apps, tools, infra, deploy, topology, git workspace, daemons. Each section is
also addressable as 'status <section>'; --no-<section> flags suppress sections
in the default view.`,
		Example: `  dwe status
  dwe status apps
  dwe status deploy main
  dwe status --no-git --no-topology`,
		GroupID:      groupID,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// Opportunistic prompt-cache refresh. Best-effort: errors are
			// intentionally ignored — the cache is observability, not correctness.
			// Hooked at the top-level RunE only (NOT in subcommands) because only
			// the top-level performs the full aggregation.
			_ = promptcache.Write(sc.ProjectRoot, stack.HealthState(stack.HealthFromStatusInput(sc.statusInput())))
			// Best-effort bridge-daemon ensure (design D6) — top-level only,
			// mirroring the prompt-cache hook above.
			ensureBridgeDaemon(cmd.Context(), sc)
			// JSON mode: skip TUI entirely regardless of TTY state.
			if flags.Output == "json" {
				return renderStatusJSON(cmd, sc, noFlags, flags)
			}
			if shouldUseTUI(noTUI, noFlags) {
				deps := statustui.Deps{
					Cfg:         sc.Cfg,
					State:       sc.State,
					Tracked:     sc.Tracked,
					SvcDeploys:  sc.SvcDeploys,
					DockerCfg:   sc.DockerCfg,
					Topo:        sc.Topo,
					TopoStatus:  sc.TopoStatus,
					IsRunning:   sc.IsRunning,
					ProjectRoot: sc.ProjectRoot,
					Translator:  flags.I18n,
					Locale:      flags.Locale,
				}
				if err := runStatusTUIFn(cmd.Context(), deps); err != nil {
					if errors.Is(err, tui.ErrTooNarrow) {
						return renderDefaultStatus(cmd, sc, noFlags)
					}
					// A user-initiated cancel (OS SIGINT/SIGTERM, surfaced by
					// tui.Run as widgets.ErrCancelled) is a clean exit, matching
					// the sibling cmdbrowser caller.
					if errors.Is(err, widgets.ErrCancelled) {
						return nil
					}
					return err
				}
				return nil
			}
			return renderDefaultStatus(cmd, sc, noFlags)
		},
	}
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "force plain text output even on a TTY")
	cmd.Flags().BoolVar(&noFlags.noApps, "no-apps", false, "suppress the apps section")
	cmd.Flags().BoolVar(&noFlags.noTools, "no-tools", false, "suppress the tools section")
	cmd.Flags().BoolVar(&noFlags.noInfra, "no-infra", false, "suppress the infra section")
	cmd.Flags().BoolVar(&noFlags.noDeploy, "no-deploy", false, "suppress the deploy section")
	cmd.Flags().BoolVar(&noFlags.noTopology, "no-topology", false, "suppress the topology section")
	cmd.Flags().BoolVar(&noFlags.noGit, "no-git", false, "suppress the git workspace section")
	cmd.Flags().BoolVar(&noFlags.noDaemons, "no-daemons", false, "suppress the daemons section")

	cmd.AddCommand(newStatusSectionCmd(flags, "apps", "Show only the apps section", sectionApps, true))
	cmd.AddCommand(newStatusSectionCmd(flags, "tools", "Show only the tools section", sectionTools, true))
	cmd.AddCommand(newStatusSectionCmd(flags, "infra", "Show only the infra section", sectionInfra, true))
	cmd.AddCommand(newStatusSectionCmd(flags, "daemons", "Show only the daemons section", sectionDaemons, false))
	cmd.AddCommand(newStatusDeployCmd(flags))
	cmd.AddCommand(newStatusSectionCmd(flags, "topology", "Show only the topology section", sectionTopology, false))
	cmd.AddCommand(newStatusSectionCmd(flags, "git", "Show only the git workspace section", sectionGit, false))
	return cmd
}

// newStatusSectionCmd builds a single-section status subcommand (apps, tools,
// infra, daemons, topology, git). All six share the same load → JSON-or-text
// dispatch; withPendingBanner selects whether the pending-ops banner is emitted
// before the section body in text mode (the app/tool/infra sections show it,
// the daemons/topology/git sections do not). Routing through renderSection keeps
// the per-section formatting identical to the default orchestrator.
func newStatusSectionCmd(flags *cmdctx.RootFlags, use, short string, sec section, withPendingBanner bool) *cobra.Command {
	return &cobra.Command{
		Use:          use,
		Short:        short,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if flags.Output == "json" {
				return renderStatusSectionJSON(cmd, sc, sec, flags)
			}
			if withPendingBanner && sc.State != nil {
				writeNonEmpty(cmd.OutOrStdout(), render.PendingBanner(sc.State.Pending))
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sec)
		},
	}
}

func renderDefaultStatus(cmd *cobra.Command, sc *statusContext, no *noSectionFlags) error {
	in := sc.statusInput()
	out := cmd.OutOrStdout()
	errW := cmd.ErrOrStderr()

	_, _ = fmt.Fprintln(out, stack.RenderHealth(in))
	_, _ = fmt.Fprintln(out)

	if sc.State != nil {
		writeNonEmpty(out, render.PendingBanner(sc.State.Pending))
	}

	for _, s := range defaultSectionOrder {
		if no.isSuppressed(s) {
			continue
		}
		if err := renderSection(cmd.Context(), out, errW, in, sc, s); err != nil {
			return err
		}
	}
	return nil
}

func renderSection(ctx context.Context, out, errW io.Writer, in stack.StatusInput, sc *statusContext, s section) error {
	switch s {
	case sectionApps:
		body, errs := stack.RenderApps(in)
		writeNonEmpty(out, body)
		if len(errs) > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
		}
	case sectionTools:
		body, errs := stack.RenderTools(in)
		writeNonEmpty(out, body)
		if len(errs) > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
		}
	case sectionInfra:
		body, errs := stack.RenderInfra(in)
		writeNonEmpty(out, body)
		if len(errs) > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d custom status expression(s) failed to render\n", len(errs))
		}
	case sectionDaemons:
		rows, collectErrs := stack.CollectDaemons(ctx, sc.Cfg, sc.normalisedDockerCfg(), sc.ProjectRoot)
		body, renderErrs := stack.RenderDaemons(rows)
		writeNonEmpty(out, body)
		if n := len(collectErrs) + len(renderErrs); n > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d daemon row(s) failed to render\n", n)
		}
	case sectionDeploy:
		writeNonEmpty(out, stack.DeployStatus(in))
	case sectionTopology:
		writeNonEmpty(out, stack.RenderTopology(in))
	case sectionGit:
		rows := stack.CollectGitWorkspace(ctx, sc.Cfg, sc.ProjectRoot)
		if len(rows) == 0 {
			return nil
		}
		_, _ = fmt.Fprintln(out, render.SectionTitle("Git Workspace"))
		_, _ = fmt.Fprintln(out, render.GitWorkspace(rows))
		failed := 0
		for _, r := range rows {
			if r.Err != nil {
				failed++
			}
		}
		if failed > 0 {
			_, _ = fmt.Fprintf(errW, "warning: git status failed for %d service(s)\n", failed)
		}
	}
	return nil
}

func writeNonEmpty(w io.Writer, s string) {
	if s == "" {
		return
	}
	_, _ = fmt.Fprint(w, s)
	if !strings.HasSuffix(s, "\n") {
		_, _ = fmt.Fprintln(w)
	}
}
