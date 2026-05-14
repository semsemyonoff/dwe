package command

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status [service]",
		Short: "Show stack health and services/tools status",
		Long: `Display the running status of the entire devbox stack.

Shows a health indicator (running/partial/stopped), a services table,
a tools table with live container status, a compose topology tree,
and deploy status per service.

If a service name is provided, shows per-phase/step deploy breakdown for that service.`,
		Example:      "  devbox status\n  devbox status main",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Load deploy state
			statePath := filepath.Join(flags.ProjectRoot(), journal.DefaultRelPath)
			state, err := journal.Load(statePath)
			if err != nil {
				return fmt.Errorf("loading deploy state: %w", err)
			}

			// Load tracked services
			tracked, svcDeploys, err := deploy.LoadTrackedServices(cfg, flags.ProjectRoot())
			if err != nil {
				return fmt.Errorf("loading tracked services: %w", err)
			}

			// If a service is specified, show per-phase breakdown
			if len(args) > 0 {
				serviceName := args[0]
				return renderServiceDeployDetail(cmd.OutOrStdout(), state, cfg, svcDeploys, tracked, serviceName)
			}

			projectName, dockerCfg, err := stack.ResolveProjectAndDocker(flags.configPath, cfg)
			if err != nil {
				return err
			}
			composeFiles := cfg.ComposeFiles()

			var processEnv []string
			if dockerCfg != nil {
				processEnv = docker.MergeEnv(dockerCfg.ProcessEnv)
			}

			dockerBin := config.DockerBin(cfg)
			topo := stack.FetchComposeTopology(composeFiles, projectName, processEnv, dockerBin)
			var topoStatus map[string]ui.NodeStatus
			if topo == nil {
				topo = stack.ParseTopologyFromFiles(composeFiles)
			} else {
				topoStatus = stack.ComposeNodeStatuses(composeFiles, projectName, processEnv, dockerBin)
				if topoStatus != nil {
					for name := range topo {
						if _, ok := topoStatus[name]; !ok {
							topoStatus[name] = ui.NodeStopped
						}
					}
				}
			}

			topo, topoStatus = stack.AugmentWithDisabled(cfg, topo, topoStatus)

			if dockerCfg != nil && len(dockerCfg.Topology.Hidden) > 0 {
				topo, topoStatus = stack.RemoveHiddenNodes(topo, topoStatus, dockerCfg.Topology.Hidden)
			}

			isRunning := func(_, container string) bool {
				return containerRunning(projectName, container, dockerBin)
			}

			// Render existing stack status
			if err := stack.RunStatus(render.Stdout(), cfg, isRunning, topo, topoStatus); err != nil {
				return err
			}

			// Render deploy status
			return renderDeployStatus(cmd.OutOrStdout(), state, cfg, svcDeploys, tracked)
		},
	}
}

// renderDeployStatus builds and renders the deploy status table.
func renderDeployStatus(w io.Writer, state *journal.ProjectState, cfg *config.DevboxConfig, svcDeploys map[string]*config.DeployConfig, tracked []string) error {
	view, err := buildDeployStatusView(state, cfg, svcDeploys, tracked)
	if err != nil {
		return err
	}

	if len(view.Rows) == 0 {
		return nil
	}

	rw := render.NewWriter(w)
	_, _ = fmt.Fprintf(rw.Writer(), "%s\n", ui.RenderSectionTitle("Deploy Status"))

	// Convert view rows to UI rows
	uiRows := make([]ui.DeployStatusRow, len(view.Rows))
	for i, row := range view.Rows {
		uiRows[i] = ui.DeployStatusRow{
			Service:         row.Service,
			Status:          string(row.Status),
			ConfigDelta:     string(row.ConfigDelta),
			PrevHashShort:   row.PrevHashShort,
			CurrHashShort:   row.CurrHashShort,
			LastFailedPhase: row.LastFailedPhase,
			LastFailedStep:  row.LastFailedStep,
		}
	}

	_, _ = fmt.Fprintln(rw.Writer(), ui.RenderDeployStatus(uiRows))
	return nil
}

// buildDeployStatusView assembles the view model from state and config.
func buildDeployStatusView(state *journal.ProjectState, cfg *config.DevboxConfig, svcDeploys map[string]*config.DeployConfig, tracked []string) (*statusview.DeployStatusView, error) {
	view := &statusview.DeployStatusView{
		ProjectStatus:     state.Project.Status,
		ProjectDeployedAt: state.Project.DeployedAt,
	}

	for _, serviceName := range tracked {
		svcCfg, ok := cfg.Services[serviceName]
		if !ok {
			continue
		}

		svcDeploy, ok := svcDeploys[serviceName]
		if !ok {
			svcDeploy = nil
		}

		currHash := journal.ServiceConfigHash(svcCfg, svcDeploy)
		currHashShort := currHash[:8]

		var delta statusview.ConfigDelta
		delta = statusview.ConfigDeltaOK
		var prevHashShort string

		svcState, exists := state.Services[serviceName]
		if !exists {
			delta = statusview.ConfigDeltaMissing
		} else {
			prevHashShort = svcState.ConfigHash[:8]
			if svcState.ConfigHash != currHash {
				delta = statusview.ConfigDeltaChanged
			}
		}

		row := statusview.DeployStatusRow{
			Service:       serviceName,
			Status:        journal.StatusNotDeployed,
			ConfigDelta:   delta,
			CurrHashShort: currHashShort,
			PrevHashShort: prevHashShort,
		}

		if svcState != nil {
			row.Status = svcState.Status
			if svcState.LastRun != nil && svcState.LastRun.Status != journal.StatusOk {
				// Find the last failed step
				for _, phase := range svcState.Phases {
					for stepName, step := range phase.Steps {
						if step.Status == journal.StatusFailed {
							// Keep the last failed step name
							row.LastFailedPhase = ""
							row.LastFailedStep = stepName
						}
					}
					if phase.Status == journal.StatusFailed {
						row.LastFailedPhase = ""
					}
				}
			}
		}

		view.Rows = append(view.Rows, row)
	}

	return view, nil
}

// renderServiceDeployDetail renders per-phase/step breakdown for a service.
func renderServiceDeployDetail(w io.Writer, state *journal.ProjectState, _ *config.DevboxConfig, _ map[string]*config.DeployConfig, tracked []string, serviceName string) error {
	// Verify service is tracked
	if !slices.Contains(tracked, serviceName) {
		return fmt.Errorf("service %q is not tracked (not deployed)", serviceName)
	}

	svcState, ok := state.Services[serviceName]
	if !ok {
		rw := render.NewWriter(w)
		_, _ = fmt.Fprintf(rw.Writer(), "Service %q not deployed yet\n", serviceName)
		return nil
	}

	rw := render.NewWriter(w)
	_, _ = fmt.Fprintf(rw.Writer(), "Deploy status for service %q:\n\n", serviceName)
	_, _ = fmt.Fprintf(rw.Writer(), "Overall status: %s\n", svcState.Status)
	_, _ = fmt.Fprintf(rw.Writer(), "Config hash: %s\n", svcState.ConfigHash)
	if svcState.LastRun != nil {
		_, _ = fmt.Fprintf(rw.Writer(), "Last run: %s\n", svcState.LastRun.Status)
	}
	_, _ = fmt.Fprintf(rw.Writer(), "\n%s\n", ui.RenderSectionTitle("Phases"))

	for phaseName, phase := range svcState.Phases {
		_, _ = fmt.Fprintf(rw.Writer(), "  %s: %s\n", phaseName, phase.Status)
		for stepName, step := range phase.Steps {
			_, _ = fmt.Fprintf(rw.Writer(), "    %s: %s (hash=%s, duration=%dms)\n",
				stepName, step.Status, step.ActionHash[:8], step.DurationMs)
		}
	}

	return nil
}
