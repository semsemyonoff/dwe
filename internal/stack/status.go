package stack

import (
	"fmt"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// ContainerCheckFn reports whether a container with the given name is running
// under the given compose project. The callback shape lets tests stub Docker
// state without spawning processes.
type ContainerCheckFn func(projectFullName, containerName string) bool

// StatusInput bundles the data needed to render the full project status view.
// Topo/TopoStatus may be nil — topology section is then skipped.
// State may be nil — deploy status section is then skipped (useful for tests
// that exercise the stack rendering without a project state file).
type StatusInput struct {
	Cfg        *config.DevboxConfig
	IsRunning  ContainerCheckFn
	Topo       map[string][]string
	TopoStatus map[string]ui.NodeStatus
	State      *journal.ProjectState
	SvcDeploys map[string]*config.DeployConfig
	Tracked    []string
}

// RunStatus renders the full stack status view. The body is purely the
// section ordering — to change the layout, reorder the calls below.
func RunStatus(w *render.Writer, in StatusInput) error {
	RenderHealth(w, in)
	RenderDeployStatus(w, in)
	RenderServices(w, in)
	RenderTools(w, in)
	RenderTopology(w, in)
	return nil
}

// RenderHealth writes the "Devbox: ●/◐/○ ..." indicator line.
func RenderHealth(w *render.Writer, in StatusInput) {
	svcRows := collectServiceRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	indicator := selectHealthIndicator(svcRows, in.TopoStatus)
	_, _ = fmt.Fprintf(w.Writer(), "Devbox: %s\n\n", indicator)
}

// RenderServices writes the Services section title and the services table.
func RenderServices(w *render.Writer, in StatusInput) {
	svcRows := collectServiceRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Services"))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderServiceTable(svcRows, nil))
}

// RenderTools writes the Tools section title and the tools table.
func RenderTools(w *render.Writer, in StatusInput) {
	toolRows := collectToolRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Tools"))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderToolTable(toolRows, nil))
}

// RenderTopology writes the Topology section, or a no-op when topology data
// is absent or rendering produces nothing.
func RenderTopology(w *render.Writer, in StatusInput) {
	if in.Topo == nil {
		return
	}
	categories := BuildNodeCategories(in.Cfg)
	rendered := ui.RenderTopology(in.Topo, in.TopoStatus, categories)
	if rendered == "" {
		return
	}
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderSectionTitle("Topology"))
	_, _ = fmt.Fprintln(w.Writer(), rendered)
}

func collectServiceRows(cfg *config.DevboxConfig, isRunning ContainerCheckFn, projectFull string) []ui.ServiceTableRow {
	names := sortedKeys(cfg.Services)
	rows := make([]ui.ServiceTableRow, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		running := false
		if svc.Mandatory || svc.Enabled {
			running = isRunning(projectFull, svc.Container)
		}
		rows = append(rows, ui.ServiceTableRow{
			Name:      name,
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
			Running:   running,
		})
	}
	return rows
}

func collectToolRows(cfg *config.DevboxConfig, isRunning ContainerCheckFn, projectFull string) []ui.ToolTableRow {
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
	return toolRows
}

func selectHealthIndicator(svcRows []ui.ServiceTableRow, topoStatus map[string]ui.NodeStatus) string {
	var health Health
	if HasRuntimeStatuses(topoStatus) {
		health = AggregateHealthFromTopo(topoStatus)
	} else {
		health = AggregateHealth(svcRows)
	}
	switch health {
	case HealthRunning:
		return ui.RenderEnabled("● running")
	case HealthPartial:
		return ui.RenderPartial("◐ partial")
	default:
		return ui.RenderStopped("○ stopped")
	}
}

// ContainerRunning checks if a Docker container is running by full container name.
// Uses docker inspect to get an exact name match (docker ps name filter uses substring
// matching against the full /name path which is not portable across Docker versions).
// dockerBin is the Docker-compatible binary (e.g. "docker", "podman").
func ContainerRunning(projectFullName, containerName, dockerBin string) bool {
	fullName := projectFullName + "-" + containerName
	out, err := exec.Command(
		dockerBin, "inspect", //nolint:gosec
		"--format", "{{.State.Status}}",
		fullName,
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "running"
}
