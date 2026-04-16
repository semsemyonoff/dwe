package command

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show stack health and services/tools status",
		Long: `Display the running status of the entire devbox stack.

Shows a health indicator (running/partial/stopped), a services table,
a tools table with live container status, and a compose topology tree.`,
		Example:      "  devbox status",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.configPath, cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			projectName, err := resolveProjectName(flags.configPath, cfg)
			if err != nil {
				return err
			}
			composeFiles := cfg.ComposeFiles()

			// Fetch compose topology (best-effort; nil on failure).
			topo := fetchComposeTopology(composeFiles, projectName)
			var topoStatus map[string]ui.NodeStatus
			if topo == nil {
				// Fallback: parse compose files directly without docker.
				topo = parseTopologyFromFiles(composeFiles)
				// No running status in fallback mode — nodes appear without annotation.
			} else {
				topoStatus = composeNodeStatuses(composeFiles, projectName)
				// Mark topology services that have no container yet as stopped.
				// docker compose ps --all only lists services with existing containers;
				// services that have never been started are absent from that output.
				if topoStatus != nil {
					for name := range topo {
						if _, ok := topoStatus[name]; !ok {
							topoStatus[name] = ui.NodeStopped
						}
					}
				}
			}

			// Augment topology with disabled services/tools as isolated nodes.
			topo, topoStatus = augmentWithDisabled(cfg, topo, topoStatus)

			isRunning := func(_, container string) bool {
				return containerRunning(projectName, container)
			}
			return runStatus(render.Stdout(), cfg, isRunning, topo, topoStatus)
		},
	}
}

// StackHealth represents the overall running health of the stack.
type StackHealth int

// StackHealth constants indicate how many active services are running.
const (
	StackStopped StackHealth = iota // no enabled/mandatory services are running
	StackPartial                    // some enabled/mandatory services are running
	StackRunning                    // all enabled/mandatory services are running
)

// aggregateHealth computes the overall stack health from service rows.
// Only mandatory or enabled services count toward the total.
func aggregateHealth(rows []ui.ServiceTableRow) StackHealth {
	active := 0
	running := 0
	for _, r := range rows {
		if r.Mandatory || r.Enabled {
			active++
			if r.Running {
				running++
			}
		}
	}
	if active == 0 || running == 0 {
		return StackStopped
	}
	if running < active {
		return StackPartial
	}
	return StackRunning
}

// aggregateHealthFromTopo computes stack health from topology node statuses.
// All non-disabled nodes are treated as active (including infrastructure containers
// such as nginx, db, redis that are not tracked in cfg.Services). Returns StackStopped
// when topoStatus is empty.
func aggregateHealthFromTopo(topoStatus map[string]ui.NodeStatus) StackHealth {
	active := 0
	running := 0
	for _, status := range topoStatus {
		if status == ui.NodeDisabled {
			continue
		}
		active++
		if status == ui.NodeRunning {
			running++
		}
	}
	if active == 0 || running == 0 {
		return StackStopped
	}
	if running < active {
		return StackPartial
	}
	return StackRunning
}

// runStatus renders the stack health indicator, service table, tool table,
// and optionally a compose topology tree.
// topo and topoStatus may be nil — topology section is skipped when topo is nil.
func runStatus(w *render.Writer, cfg *config.DevboxConfig, isRunning containerCheckFn, topo map[string][]string, topoStatus map[string]ui.NodeStatus) error {
	projectFull := cfg.Project.FullName()

	// Build service rows.
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

	// Build tool rows.
	toolData := buildToolRows(cfg)
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

	// Stack health indicator: prefer topology status (covers all compose services including
	// infrastructure containers like nginx, db, redis). Fall back to svcRows-only when
	// topology is unavailable (docker not running or compose files missing).
	var health StackHealth
	if len(topoStatus) > 0 {
		health = aggregateHealthFromTopo(topoStatus)
	} else {
		health = aggregateHealth(svcRows)
	}
	var indicator string
	switch health {
	case StackRunning:
		indicator = ui.RenderEnabled("● running")
	case StackPartial:
		indicator = ui.RenderPartial("◐ partial")
	default:
		indicator = ui.RenderStopped("○ stopped")
	}

	_, _ = fmt.Fprintf(w.Writer(), "Stack: %s\n\n", indicator)
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderServiceTable(svcRows))
	_, _ = fmt.Fprintln(w.Writer(), ui.RenderToolTable(toolRows))

	// Topology tree (optional).
	if topo != nil {
		rendered := ui.RenderTopology(topo, topoStatus)
		if rendered != "" {
			_, _ = fmt.Fprintln(w.Writer(), rendered)
		}
	}

	return nil
}

// fetchComposeTopology runs `docker compose config` with the given compose files
// and parses the service dependency graph. Returns nil on any error (docker not
// available, no compose files, etc.) so callers can degrade gracefully.
func fetchComposeTopology(composeFiles []string, projectName string) map[string][]string {
	if len(composeFiles) == 0 {
		return nil
	}
	args := buildComposeArgs(projectName, composeFiles, "config")
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil
	}
	deps, err := ui.ParseComposeTopology(out)
	if err != nil {
		return nil
	}
	return deps
}

// parseTopologyFromFiles builds a dependency map by reading and parsing compose
// YAML files directly, without invoking docker. Used as a fallback when docker
// is not available. Each file is parsed independently; services are merged across files.
func parseTopologyFromFiles(composeFiles []string) map[string][]string {
	if len(composeFiles) == 0 {
		return nil
	}
	result := make(map[string][]string)
	for _, f := range composeFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		deps, err := ui.ParseComposeTopology(data)
		if err != nil {
			continue
		}
		// Later files replace earlier deps for the same service, matching
		// Docker Compose override semantics (depends_on is not additive
		// across files; a later file can clear or replace it).
		maps.Copy(result, deps)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// composeNodeStatuses runs `docker compose ps` with the given compose files and
// returns a map of compose service name → NodeStatus. Returns nil on any error.
func composeNodeStatuses(composeFiles []string, projectName string) map[string]ui.NodeStatus {
	if len(composeFiles) == 0 {
		return nil
	}
	// "docker compose ps --format {{.Service}} --filter status=running" lists
	// only running service names. We collect those, then mark all others stopped.
	runningArgs := buildComposeArgs(projectName, composeFiles, "ps", "--format", "{{.Service}}", "--filter", "status=running")
	runningOut, err := exec.Command("docker", runningArgs...).Output()
	if err != nil {
		return nil
	}

	running := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(runningOut)), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			running[s] = true
		}
	}

	// Get all service names from ps (any state).
	allArgs := buildComposeArgs(projectName, composeFiles, "ps", "--format", "{{.Service}}", "--all")
	allOut, err := exec.Command("docker", allArgs...).Output()
	if err != nil {
		// Fall back: only mark known running services.
		result := make(map[string]ui.NodeStatus, len(running))
		for name := range running {
			result[name] = ui.NodeRunning
		}
		return result
	}

	result := make(map[string]ui.NodeStatus)
	for line := range strings.SplitSeq(strings.TrimSpace(string(allOut)), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if running[s] {
			result[s] = ui.NodeRunning
		} else {
			result[s] = ui.NodeStopped
		}
	}
	return result
}

// disabledNodes returns the compose service names for services and tools that
// are neither mandatory nor enabled in the current config.
func disabledNodes(cfg *config.DevboxConfig) []string {
	var names []string
	for _, svc := range cfg.Services {
		if !svc.Mandatory && !svc.Enabled && svc.Container != "" {
			names = append(names, svc.Container)
		}
	}
	for _, t := range buildToolRows(cfg) {
		if !t.Enabled && t.Container != "" {
			names = append(names, t.Container)
		}
	}
	sort.Strings(names)
	return names
}

// augmentWithDisabled adds disabled services and tools as isolated nodes to topo
// and marks them NodeDisabled in the status map. If topo is nil, it is initialised
// to an empty map so disabled nodes are still shown.
func augmentWithDisabled(cfg *config.DevboxConfig, topo map[string][]string, topoStatus map[string]ui.NodeStatus) (map[string][]string, map[string]ui.NodeStatus) {
	disabled := disabledNodes(cfg)
	if len(disabled) == 0 {
		return topo, topoStatus
	}
	if topo == nil {
		topo = make(map[string][]string)
	}
	if topoStatus == nil {
		topoStatus = make(map[string]ui.NodeStatus)
	}
	for _, name := range disabled {
		if _, exists := topo[name]; !exists {
			topo[name] = nil
		}
		topoStatus[name] = ui.NodeDisabled
	}
	return topo, topoStatus
}

// buildComposeArgs constructs `["compose", ["-p", projectName,] "-f", file..., command, extraArgs...]`.
func buildComposeArgs(projectName string, composeFiles []string, command string, extraArgs ...string) []string {
	args := []string{"compose"}
	if projectName != "" {
		args = append(args, "-p", projectName)
	}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, command)
	args = append(args, extraArgs...)
	return args
}
