package stack

import (
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// RunStatus renders the stack health indicator, service table, tool table,
// and optionally a compose topology tree.
// topo and topoStatus may be nil — topology section is skipped when topo is nil.
func RunStatus(w *render.Writer, cfg *config.DevboxConfig, isRunning func(projectFullName, containerName string) bool, topo map[string][]string, topoStatus map[string]ui.NodeStatus) error {
	projectFull := cfg.Project.FullName()

	names := sortedKeys(cfg.Services)
	svcRows := make([]ui.ServiceTableRow, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		running := false
		if svc.Mandatory || svc.Enabled {
			running = isRunning(projectFull, svc.Container)
		}
		svcRows = append(svcRows, ui.ServiceTableRow{
			Name:      name,
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
			Running:   running,
		})
	}

	toolData := BuildToolRows(cfg)
	toolRows := make([]ui.ToolTableRow, len(toolData))
	for i, t := range toolData {
		running := false
		if t.Enabled {
			running = isRunning(projectFull, t.Container)
		}
		toolRows[i] = ui.ToolTableRow{
			Name:      t.Name,
			Host:      t.Host,
			Port:      t.Port,
			Container: t.Container,
			Enabled:   t.Enabled,
			Running:   running,
		}
	}

	var health Health
	if HasRuntimeStatuses(topoStatus) {
		health = AggregateHealthFromTopo(topoStatus)
	} else {
		health = AggregateHealth(svcRows)
	}
	var indicator string
	switch health {
	case HealthRunning:
		indicator = ui.RenderEnabled("● running")
	case HealthPartial:
		indicator = ui.RenderPartial("◐ partial")
	default:
		indicator = ui.RenderStopped("○ stopped")
	}

	_, _ = fmt.Fprintf(w.Writer(), "Devbox: %s\n\n", indicator)
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Services"))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderServiceTable(svcRows))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Tools"))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderToolTable(toolRows))

	if topo != nil {
		categories := BuildNodeCategories(cfg)
		rendered := ui.RenderTopology(topo, topoStatus, categories)
		if rendered != "" {
			_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Topology"))
			_, _ = fmt.Fprintln(w.Writer(), rendered)
		}
	}

	return nil
}
