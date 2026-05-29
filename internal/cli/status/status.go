// Package status hosts the `devbox status` command tree.
package status

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/stack"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/ui/statustui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// Test seams for TTY detection and TUI dispatch.
// Tests override these via assignment to suppress TUI or verify dispatch logic.
var (
	isTerminalFn   = term.IsTerminal
	runStatusTUIFn = statustui.Run
)

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
	Cfg         *config.DevboxConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.ServiceDeployConfig
	ProjectName string
	DockerCfg   *config.DockerConfig
	Topo        map[string][]string
	TopoStatus  map[string]ui.NodeStatus
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
	topo, topoStatus := stack.ResolveTopology(cfg, dockerCfg, projectName)
	dockerBin := config.DockerBin(cfg)
	isRunning := func(_, container string) bool {
		return stack.ContainerRunning(projectName, container, dockerBin)
	}
	return &statusContext{
		Cfg:         cfg,
		State:       state,
		Tracked:     tracked,
		SvcDeploys:  svcDeploys,
		ProjectName: projectName,
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

// NewCmd creates the `devbox status` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	noFlags := &noSectionFlags{}
	var noTUI bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stack health and per-section status (read-only)",
		Long: `Display the running status of the entire devbox stack.

The default view prints a health indicator followed by all sections in order:
apps, tools, infra, deploy, topology, git workspace, daemons. Each section is
also addressable as 'status <section>'; --no-<section> flags suppress sections
in the default view.`,
		Example: `  devbox status
  devbox status apps
  devbox status deploy main
  devbox status --no-git --no-topology`,
		GroupID:      groupID,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
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
					ProjectName: sc.ProjectName,
					DockerCfg:   sc.DockerCfg,
					Topo:        sc.Topo,
					TopoStatus:  sc.TopoStatus,
					IsRunning:   sc.IsRunning,
					ProjectRoot: sc.ProjectRoot,
				}
				return runStatusTUIFn(cmd.Context(), deps)
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

	cmd.AddCommand(newStatusAppsCmd(flags))
	cmd.AddCommand(newStatusToolsCmd(flags))
	cmd.AddCommand(newStatusInfraCmd(flags))
	cmd.AddCommand(newStatusDaemonsCmd(flags))
	cmd.AddCommand(newStatusDeployCmd(flags))
	cmd.AddCommand(newStatusTopologyCmd(flags))
	cmd.AddCommand(newStatusGitCmd(flags))
	return cmd
}

func renderDefaultStatus(cmd *cobra.Command, sc *statusContext, no *noSectionFlags) error {
	in := sc.statusInput()
	out := cmd.OutOrStdout()
	errW := cmd.ErrOrStderr()

	_, _ = fmt.Fprintln(out, stack.RenderHealth(in))
	_, _ = fmt.Fprintln(out)

	if sc.State != nil {
		writeNonEmpty(out, ui.RenderPendingBanner(sc.State.Pending))
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
		rows, collectErrs := stack.CollectDaemons(ctx, sc.Cfg, sc.normalisedDockerCfg())
		body, renderErrs := stack.RenderDaemons(rows)
		writeNonEmpty(out, body)
		if n := len(collectErrs) + len(renderErrs); n > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d daemon row(s) failed to render\n", n)
		}
	case sectionDeploy:
		writeNonEmpty(out, stack.RenderDeployStatus(in))
	case sectionTopology:
		writeNonEmpty(out, stack.RenderTopology(in))
	case sectionGit:
		rows := stack.CollectGitWorkspace(ctx, sc.Cfg, sc.ProjectRoot)
		if len(rows) == 0 {
			return nil
		}
		_, _ = fmt.Fprintln(out, ui.RenderSectionTitle("Git Workspace"))
		_, _ = fmt.Fprintln(out, ui.RenderGitWorkspace(rows))
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
