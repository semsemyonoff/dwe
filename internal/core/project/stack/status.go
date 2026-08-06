package stack

import (
	"maps"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// ContainerCheckFn reports whether the given compose service's container is
// running. Callers close over the compose project name when constructing the
// callback so the stack/render layer never needs to thread it through. The
// argument is the compose service name — which in dwe is svc.Container (the
// value compose stamps into the com.docker.compose.service label), NOT a guessed
// container name — see ServiceRunning. The callback shape lets tests stub Docker
// state without spawning processes.
type ContainerCheckFn func(composeService string) bool

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
	// Width is the render width budget for the Render*(in) convenience
	// wrappers (0 = resolve from the sink — i.e. fall back to the
	// stdout-probing render.* entry points). It exists as the override
	// channel for a caller that already knows its own width, but has no
	// production setter today: cli/status leaves it 0, and the status TUI
	// does not use these wrappers at all — it goes through the collect/render
	// split (CollectApps + RenderAppsRows(sec, width)), which takes the width
	// as a parameter.
	Width int
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

// ServiceSection bundles a services section's collected rows and its stable
// custom-column order — the extra-column names, in the deterministic order
// BuildCustomColumns derives from config. Rendering needs both: the render
// half has no config to re-derive the column order from, so Collect* carries
// it alongside the rows.
type ServiceSection struct {
	Rows      []render.ServiceTableRow
	ExtraCols []string
}

// CollectApps collects the Apps section's rows (services with type=app),
// including the IsRunning probe and custom status-column evaluation. Callers
// that only need to render — the status TUI — pass the result to
// RenderAppsRows without re-probing Docker.
func CollectApps(in StatusInput) (ServiceSection, []error) {
	return collectServiceSection(in, config.ServiceTypeApp)
}

// CollectTools collects the Tools section's rows (services with type=tool).
// See CollectApps.
func CollectTools(in StatusInput) (ServiceSection, []error) {
	return collectServiceSection(in, config.ServiceTypeTool)
}

// CollectInfra collects the Infra section's rows (services with type=infra).
// See CollectApps.
func CollectInfra(in StatusInput) (ServiceSection, []error) {
	return collectServiceSection(in, config.ServiceTypeInfra)
}

func collectServiceSection(in StatusInput, t config.ServiceType) (ServiceSection, []error) {
	rows := collectRowsByType(in.Cfg, in.IsRunning, &t)
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
	return ServiceSection{Rows: rows, ExtraCols: extraCols}, errs
}

// RenderAppsRows renders a previously-collected Apps ServiceSection as the
// section title + table, at the given width (0 = unbounded). It never probes
// Docker or a sink — the pure counterpart to CollectApps, used by the status
// TUI which already knows its panel width.
func RenderAppsRows(sec ServiceSection, width int) string {
	return renderServiceSectionAt(sec, "Apps", true, width)
}

// RenderToolsRows is RenderAppsRows for the Tools section. See CollectTools.
func RenderToolsRows(sec ServiceSection, width int) string {
	return renderServiceSectionAt(sec, "Tools", false, width)
}

// RenderInfraRows is RenderAppsRows for the Infra section. See CollectInfra.
func RenderInfraRows(sec ServiceSection, width int) string {
	return renderServiceSectionAt(sec, "Infra", false, width)
}

func renderServiceSectionAt(sec ServiceSection, title string, withDirCol bool, width int) string {
	if len(sec.Rows) == 0 {
		return ""
	}
	return wrapSection(title, render.ServicesTableAt(sec.Rows, sec.ExtraCols, withDirCol, width), width)
}

// RenderApps returns the Apps section (services with type=app) title + table.
// Returns ("", nil) when no apps are configured. Thin collect-then-render
// wrapper kept for cli/ callers; see CollectApps / RenderAppsRows for the split.
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
	sec, errs := collectServiceSection(in, t)
	if len(sec.Rows) == 0 {
		return "", errs
	}
	if in.Width > 0 {
		return wrapSection(title, render.ServicesTableAt(sec.Rows, sec.ExtraCols, withDirCol, in.Width), in.Width), errs
	}
	return wrapSection(title, render.ServicesTable(sec.Rows, sec.ExtraCols, withDirCol), 0), errs
}

// wrapSection renders a status section as a SectionTitle line followed by the
// body, each terminated with a newline. It is the shared envelope used by the
// services / topology / deploy / daemons section renderers.
//
// width is the same budget the body was rendered at (0 = resolve the title bar
// from the terminal, matching the sink-probing body renderers). Passing it on
// is what keeps the "── Title ──…" rule inside a fixed sub-region such as a
// status-TUI panel: render.SectionTitle alone always sizes the bar from the
// ambient terminal, so a narrow panel would carry a title wider than its own
// viewport.
func wrapSection(title, body string, width int) string {
	var b strings.Builder
	b.WriteString(render.SectionTitleAt(title, width))
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	return b.String()
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
	// Width 0 on purpose: render.Topology draws an indented dependency tree,
	// not a width-aware table, so there is no panel budget the body honors and
	// none for the title to match either.
	return wrapSection("Topology", rendered, 0)
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
		if (svc.Required || svc.Enabled) && isRunning != nil {
			// svc.Container is the compose service name (com.docker.compose.service);
			// it defaults to the folder key but may be overridden in service.yml.
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

// ServiceRunning reports whether the compose service composeService has at
// least one running container in the project projectName. composeService is the
// com.docker.compose.service label value — in dwe that is svc.Container (which
// defaults to the folder key but may be overridden in service.yml), NOT a
// guessed "<project>-<container>" name. It delegates to
// docker.ServiceContainerName, which matches on the com.docker.compose.project /
// com.docker.compose.service labels (correct under any container_name override
// and compose's default "<project>-<service>-<index>" naming) and excludes
// ephemeral one-off `compose run` containers so a service is not reported
// running on the strength of a transient `dwe shell --mode run` container alone.
// dockerBin is the Docker-compatible binary (e.g. "docker", "podman");
// processEnv carries DOCKER_HOST / DOCKER_CONTEXT overrides from docker.yml
// process_env so the probe targets the same daemon as lifecycle commands.
func ServiceRunning(projectName, composeService, dockerBin string, processEnv []string) bool {
	name, err := docker.ServiceContainerName(dockerBin, processEnv, projectName, composeService, true)
	return err == nil && name != ""
}
