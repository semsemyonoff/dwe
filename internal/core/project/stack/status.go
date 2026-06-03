package stack

import (
	"maps"
	"os/exec"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// ContainerCheckFn reports whether the named container is running. Callers
// close over the compose project name when constructing the callback so the
// stack/render layer never needs to thread it through. The callback shape
// lets tests stub Docker state without spawning processes.
type ContainerCheckFn func(containerName string) bool

// StatusInput bundles the data needed to render the full project status view.
// Topo/TopoStatus may be nil — topology section is then skipped.
// State may be nil — deploy status section is then skipped (useful for tests
// that exercise the stack rendering without a project state file).
type StatusInput struct {
	Cfg        *config.DweConfig
	IsRunning  ContainerCheckFn
	Topo       map[string][]string
	TopoStatus map[string]render.NodeStatus
	State      *journal.ProjectState
	SvcDeploys map[string]*config.ServiceDeployConfig
	Tracked    []string
}

// HealthIndicator returns just the health indicator glyph and state (e.g., "● running")
// without the "DWE: " prefix. Returns "" when no config is loaded.
func HealthIndicator(in StatusInput) string {
	if in.Cfg == nil {
		return ""
	}
	return formatHealthIndicator(HealthFromStatusInput(in))
}

// RenderHealth returns the "DWE: ●/◐/○ ..." indicator line (no trailing newline).
func RenderHealth(in StatusInput) string {
	return "DWE: " + HealthIndicator(in)
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
	rows := collectRowsByType(in.Cfg, in.IsRunning, &t)
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
	b.WriteString(render.SectionTitle(title))
	b.WriteByte('\n')
	b.WriteString(render.ServicesTable(rows, extraCols, withDirCol))
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
	rendered := render.Topology(in.Topo, in.TopoStatus, categories)
	if rendered == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(render.SectionTitle("Topology"))
	b.WriteByte('\n')
	b.WriteString(rendered)
	b.WriteByte('\n')
	return b.String()
}

// buildServiceTemplateData prepares the template data map for a service row's
// custom status columns. See docs/reference/config/services/fields.md for the contract.
func buildServiceTemplateData(cfg *config.DweConfig, svc config.ServiceConfig) map[string]any {
	return map[string]any{
		"ServiceCfg": svc,
		"Globals":    rawSubtree(cfg, "globals"),
		"Raw":        cfg.Raw,
	}
}

func rawSubtree(cfg *config.DweConfig, key string) any {
	if cfg == nil || cfg.Raw == nil {
		return nil
	}
	return cfg.Raw[key]
}

// collectRowsByType returns rows for services matching the given type. When
// filter is nil, all services are returned (used by health aggregation).
func collectRowsByType(cfg *config.DweConfig, isRunning ContainerCheckFn, filter *config.ServiceType) []render.ServiceTableRow {
	if cfg == nil {
		return nil
	}
	names := slices.Sorted(maps.Keys(cfg.Services))
	rows := make([]render.ServiceTableRow, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		if filter != nil && svc.Type != *filter {
			continue
		}
		running := false
		if svc.Required || svc.Enabled {
			running = isRunning(svc.Container)
		}
		rows = append(rows, render.ServiceTableRow{
			Name:      name,
			Icon:      svc.DisplayIcon(),
			Dir:       svc.Dir,
			Container: svc.Container,
			Hosts:     svc.Hosts,
			Ports:     svc.PortNumbers(),
			Mandatory: svc.Required,
			Enabled:   svc.Enabled,
			Running:   running,
		})
	}
	return rows
}

// formatHealthIndicator renders a Health value as its glyph + label string.
// Stays unexported: callers should aggregate via HealthFromStatusInput and
// then call HealthIndicator (which handles the nil-Cfg case).
func formatHealthIndicator(health Health) string {
	switch health {
	case HealthRunning:
		return styles.RenderEnabled("● running")
	case HealthPartial:
		return styles.RenderPartial("◐ partial")
	default:
		return styles.RenderStopped("○ stopped")
	}
}

// CollectServiceRows returns the ServiceTableRow slice for services matching
// the given type. When filter is nil, all services are included.
// The caller is responsible for any custom-column rendering.
func CollectServiceRows(in StatusInput, filter *config.ServiceType) []render.ServiceTableRow {
	if in.Cfg == nil {
		return nil
	}
	return collectRowsByType(in.Cfg, in.IsRunning, filter)
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
