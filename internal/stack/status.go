package stack

import (
	"maps"
	"os/exec"
	"slices"
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

// HealthIndicator returns just the health indicator glyph and state (e.g., "● running")
// without the "Devbox: " prefix.
func HealthIndicator(in StatusInput) string {
	if in.Cfg == nil {
		return ""
	}
	rows := collectRowsByType(in.Cfg, in.IsRunning, in.Cfg.Project.FullName(), nil)
	return selectHealthIndicator(rows, in.TopoStatus)
}

// RenderHealth returns the "Devbox: ●/◐/○ ..." indicator line (no trailing newline).
func RenderHealth(in StatusInput) string {
	return "Devbox: " + HealthIndicator(in)
}

// RenderApps returns the Apps section (services with type=app) title + table.
// Returns ("", nil) when no apps are configured.
func RenderApps(in StatusInput) (string, []error) {
	return renderTypeSection(in, config.ServiceTypeApp, "Apps", true)
}

// RenderTools returns the Tools section (services with type=tool) title + table.
// Returns ("", nil) when no tools are configured.
func RenderTools(in StatusInput) (string, []error) {
	return renderTypeSection(in, config.ServiceTypeTool, "Tools", false)
}

// RenderInfra returns the Infra section (services with type=infra) title + table.
// Returns ("", nil) when no infra services are configured.
func RenderInfra(in StatusInput) (string, []error) {
	return renderTypeSection(in, config.ServiceTypeInfra, "Infra", false)
}

func renderTypeSection(in StatusInput, t config.ServiceType, title string, withDirCol bool) (string, []error) {
	rows := collectRowsByType(in.Cfg, in.IsRunning, in.Cfg.Project.FullName(), &t)
	if len(rows) == 0 {
		return "", nil
	}
	extraCols := BuildCustomColumns(in.Cfg, t)
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
	b.WriteString(ui.RenderSectionTitle(title))
	b.WriteByte('\n')
	b.WriteString(ui.RenderServicesTable(rows, extraCols, withDirCol))
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

func rawSubtree(cfg *config.DevboxConfig, key string) any {
	if cfg == nil || cfg.Raw == nil {
		return nil
	}
	return cfg.Raw[key]
}

// collectRowsByType returns rows for services matching the given type. When
// filter is nil, all services are returned (used by health aggregation).
func collectRowsByType(cfg *config.DevboxConfig, isRunning ContainerCheckFn, projectFull string, filter *config.ServiceType) []ui.ServiceTableRow {
	if cfg == nil {
		return nil
	}
	names := slices.Sorted(maps.Keys(cfg.Services))
	rows := make([]ui.ServiceTableRow, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		if filter != nil && svc.Type != *filter {
			continue
		}
		running := false
		if svc.Mandatory || svc.Enabled {
			running = isRunning(projectFull, svc.Container)
		}
		rows = append(rows, ui.ServiceTableRow{
			Name:      name,
			Icon:      svc.DisplayIcon(),
			Dir:       svc.Dir,
			Container: svc.Container,
			Hosts:     svc.Hosts,
			Ports:     svc.Ports,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
			Running:   running,
		})
	}
	return rows
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
