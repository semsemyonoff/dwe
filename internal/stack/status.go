package stack

import (
	"fmt"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
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

// RenderHealth returns the "Devbox: ●/◐/○ ..." indicator line (no trailing newline).
func RenderHealth(in StatusInput) string {
	svcRows := collectServiceRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	indicator := selectHealthIndicator(svcRows, in.TopoStatus)
	return fmt.Sprintf("Devbox: %s", indicator)
}

// RenderServices returns the Services section title + table as a single
// string, plus the slice of per-row custom-column template errors. Empty
// string when no rows.
func RenderServices(in StatusInput) (string, []error) {
	rows := collectServiceRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	if len(rows) == 0 {
		return "", nil
	}
	extraCols := BuildCustomColumns(in.Cfg, KindService)
	var errs []error
	if len(extraCols) > 0 {
		for i, row := range rows {
			svc := in.Cfg.Services[row.Name]
			data := buildServiceTemplateData(in.Cfg, svc)
			cells, cellErrs := RenderCustomCells(svc.Status, data)
			if len(cellErrs) > 0 {
				errs = append(errs, cellErrs...)
			}
			rows[i].Extras = cells
		}
	}
	var b strings.Builder
	b.WriteString(ui.RenderSectionTitle("Services"))
	b.WriteByte('\n')
	b.WriteString(ui.RenderServiceTable(rows, extraCols))
	b.WriteByte('\n')
	return b.String(), errs
}

// RenderTools returns the Tools section title + table as a single string,
// plus per-row custom-column template errors. Empty string when no rows.
func RenderTools(in StatusInput) (string, []error) {
	toolRows := collectToolRows(in.Cfg, in.IsRunning, in.Cfg.Project.FullName())
	if len(toolRows) == 0 {
		return "", nil
	}
	extraCols := BuildCustomColumns(in.Cfg, KindTool)
	var errs []error
	if len(extraCols) > 0 {
		for i, row := range toolRows {
			tool := in.Cfg.Services[row.Name]
			data := buildToolTemplateData(in.Cfg, tool)
			cells, cellErrs := RenderCustomCells(tool.Status, data)
			if len(cellErrs) > 0 {
				errs = append(errs, cellErrs...)
			}
			toolRows[i].Extras = cells
		}
	}
	var b strings.Builder
	b.WriteString(ui.RenderSectionTitle("Tools"))
	b.WriteByte('\n')
	b.WriteString(ui.RenderToolTable(toolRows, extraCols))
	b.WriteByte('\n')
	return b.String(), errs
}

// RenderTopology returns the Topology section, or an empty string when
// topology data is absent or rendering produces nothing.
func RenderTopology(in StatusInput) string {
	if in.Topo == nil {
		return ""
	}
	categories := BuildNodeCategories(in.Cfg)
	rendered := ui.RenderTopology(in.Topo, in.TopoStatus, categories)
	if rendered == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(ui.RenderSectionTitle("Topology"))
	b.WriteByte('\n')
	b.WriteString(rendered)
	b.WriteByte('\n')
	return b.String()
}

// buildServiceTemplateData prepares the template data map for a service row's
// custom status columns. See docs/reference/config/services.md for the contract.
func buildServiceTemplateData(cfg *config.DevboxConfig, svc config.ServiceConfig) map[string]any {
	return map[string]any{
		"ServiceCfg": svc,
		"Globals":    rawSubtree(cfg, "globals"),
		"Raw":        cfg.Raw,
	}
}

// buildToolTemplateData prepares the template data map for a tool row's
// custom status columns. The Tool key is kept for backwards-compatibility
// with existing status: templates that reference {{ .Tool.Container }}.
func buildToolTemplateData(cfg *config.DevboxConfig, tool config.ServiceConfig) map[string]any {
	return map[string]any{
		"Tool":       tool,
		"ServiceCfg": tool,
		"Globals":    rawSubtree(cfg, "globals"),
		"Raw":        cfg.Raw,
	}
}

func rawSubtree(cfg *config.DevboxConfig, key string) any {
	if cfg == nil || cfg.Raw == nil {
		return nil
	}
	return cfg.Raw[key]
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
