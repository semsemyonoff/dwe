package command

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"

	"github.com/spf13/cobra"
)

// section identifies one of the renderable status sections used by the
// default `status` command and by the per-section subcommands.
type section int

const (
	sectionServices section = iota + 1
	sectionTools
	sectionDaemons
	sectionDeploy
	sectionTopology
	sectionGit
)

// defaultSectionOrder is the single source of truth for both the default
// status orchestrator order and the --no-* flag set.
var defaultSectionOrder = []section{
	sectionServices,
	sectionTools,
	sectionDaemons,
	sectionDeploy,
	sectionTopology,
	sectionGit,
}

// statusContext bundles everything a status subcommand needs. Built lazily
// per command execution via loadStatusContext — never via PersistentPreRunE
// (which would shadow the root hook; see CLAUDE.md).
type statusContext struct {
	Cfg         *config.DevboxConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.DeployConfig
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
func loadStatusContext(flags *rootFlags, errW io.Writer) (*statusContext, error) {
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	statePath := filepath.Join(flags.ProjectRoot(), journal.DefaultRelPath)
	state, err := journal.Load(statePath)
	if err != nil {
		// Corrupt/unreadable state: warn then degrade gracefully — deploy section is skipped.
		_, _ = fmt.Fprintf(errW, "warning: deploy state unreadable (%v); deploy section suppressed\n", err)
		state = nil
	}
	reg, _ := usercommands.LoadRegistryFromConfigPath(flags.configPath) // nil-tolerant: LoadTrackedServices skips gate validation on error
	tracked, svcDeploys, err := deploy.LoadTrackedServices(cfg, reg, flags.ProjectRoot())
	if err != nil {
		return nil, fmt.Errorf("loading tracked services: %w", err)
	}
	projectName, dockerCfg, err := stack.ResolveProjectAndDocker(flags.configPath, cfg)
	if err != nil {
		return nil, err
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
		ConfigPath:  flags.configPath,
	}, nil
}

// normalisedDockerCfg returns DockerCfg with the standard
// "missing file → empty value" normalisation applied, mirroring
// build_context.go:63-70. Always non-nil.
func (sc *statusContext) normalisedDockerCfg() *config.DockerConfig {
	if sc.DockerCfg == nil {
		return &config.DockerConfig{}
	}
	return sc.DockerCfg
}

// statusInput projects a statusContext into stack.StatusInput.
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
	noServices bool
	noTools    bool
	noDaemons  bool
	noDeploy   bool
	noTopology bool
	noGit      bool
}

func (f *noSectionFlags) isSuppressed(s section) bool {
	switch s {
	case sectionServices:
		return f.noServices
	case sectionTools:
		return f.noTools
	case sectionDaemons:
		return f.noDaemons
	case sectionDeploy:
		return f.noDeploy
	case sectionTopology:
		return f.noTopology
	case sectionGit:
		return f.noGit
	}
	return false
}

func newStatusCmd(flags *rootFlags) *cobra.Command {
	noFlags := &noSectionFlags{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stack health and per-section status (read-only)",
		Long: `Display the running status of the entire devbox stack.

The default view prints a health indicator followed by all sections in order:
services, tools, deploy, topology, git workspace. Each section is also
addressable as 'status <section>'; --no-<section> flags suppress sections
in the default view.`,
		Example: `  devbox status
  devbox status services
  devbox status deploy main
  devbox status --no-git --no-topology`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return renderDefaultStatus(cmd, sc, noFlags)
		},
	}
	cmd.Flags().BoolVar(&noFlags.noServices, "no-services", false, "suppress the services section")
	cmd.Flags().BoolVar(&noFlags.noTools, "no-tools", false, "suppress the tools section")
	cmd.Flags().BoolVar(&noFlags.noDaemons, "no-daemons", false, "suppress the daemons section")
	cmd.Flags().BoolVar(&noFlags.noDeploy, "no-deploy", false, "suppress the deploy section")
	cmd.Flags().BoolVar(&noFlags.noTopology, "no-topology", false, "suppress the topology section")
	cmd.Flags().BoolVar(&noFlags.noGit, "no-git", false, "suppress the git workspace section")

	cmd.AddCommand(newStatusServicesCmd(flags))
	cmd.AddCommand(newStatusToolsCmd(flags))
	cmd.AddCommand(newStatusDaemonsCmd(flags))
	cmd.AddCommand(newStatusDeployCmd(flags))
	cmd.AddCommand(newStatusTopologyCmd(flags))
	cmd.AddCommand(newStatusGitCmd(flags))
	return cmd
}

// renderDefaultStatus renders the default (no-arg) status output:
// health line followed by each non-suppressed section.
func renderDefaultStatus(cmd *cobra.Command, sc *statusContext, no *noSectionFlags) error {
	in := sc.statusInput()
	out := cmd.OutOrStdout()
	errW := cmd.ErrOrStderr()

	_, _ = fmt.Fprintln(out, stack.RenderHealth(in))
	_, _ = fmt.Fprintln(out)

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

// renderSection writes one section's output to out and any warnings to errW.
func renderSection(ctx context.Context, out, errW io.Writer, in stack.StatusInput, sc *statusContext, s section) error {
	switch s {
	case sectionServices:
		body, errs := stack.RenderServices(in)
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
	case sectionDaemons:
		rows, errs := stack.CollectDaemons(ctx, sc.Cfg, sc.normalisedDockerCfg())
		body, _ := stack.RenderDaemons(rows)
		writeNonEmpty(out, body)
		if len(errs) > 0 {
			_, _ = fmt.Fprintf(errW, "warning: %d daemon row(s) failed to render\n", len(errs))
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

func newStatusServicesCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "services",
		Short:        "Show only the services section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionServices)
		},
	}
}

func newStatusToolsCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "tools",
		Short:        "Show only the tools section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionTools)
		},
	}
}

func newStatusTopologyCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "topology",
		Short:        "Show only the topology section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionTopology)
		},
	}
}

func newStatusGitCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "git",
		Short:        "Show only the git workspace section",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return renderSection(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), sc.statusInput(), sc, sectionGit)
		},
	}
}

func newStatusDeployCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy [service]",
		Short: "Show deploy status (table) or per-service deploy detail",
		Long: `With no argument, shows the deploy status table for all tracked services.
With a service name, shows the per-phase/step deploy breakdown for that service.`,
		Example:           "  devbox status deploy\n  devbox status deploy main",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: trackedServiceCompletion(flags),
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := loadStatusContext(flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				writeNonEmpty(cmd.OutOrStdout(), stack.RenderDeployStatus(sc.statusInput()))
				return nil
			}
			return stack.RenderServiceDeployDetail(cmd.OutOrStdout(), sc.State, sc.Tracked, args[0])
		},
	}
}

// trackedServiceCompletion returns shell completion names for the deploy
// subcommand's optional service argument. Follows the completion contract
// from CLAUDE.md (bypasses PersistentPreRunE).
func trackedServiceCompletion(flags *rootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := completionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, _ := usercommands.LoadRegistryFromConfigPath(configPath)
		tracked, _, err := deploy.LoadTrackedServices(cfg, reg, filepath.Dir(configPath))
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		sort.Strings(tracked)
		return tracked, cobra.ShellCompDirectiveNoFileComp
	}
}
