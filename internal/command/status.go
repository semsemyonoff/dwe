package command

import (
	"fmt"
	"os/exec"
	"path/filepath"
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
			// Load and apply styles (graceful — missing styles.yml uses defaults).
			stylesPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "styles.yml")
			stylesCfg, _ := config.LoadStylesConfig(stylesPath)
			ui.ApplyStyles(stylesCfg)

			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Fetch compose topology (best-effort; nil on failure).
			topo := fetchComposeTopology(cfg.ComposeFiles())

			// Build node status map for topology coloring.
			var topoStatus map[string]ui.NodeStatus
			if topo != nil {
				topoStatus = composeNodeStatuses(cfg.ComposeFiles())
			}

			return runStatus(render.Stdout(), cfg, containerRunning, topo, topoStatus)
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

	// Stack health indicator.
	health := aggregateHealth(svcRows)
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
func fetchComposeTopology(composeFiles []string) map[string][]string {
	if len(composeFiles) == 0 {
		return nil
	}
	args := buildComposeArgs(composeFiles, "config")
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

// composeNodeStatuses runs `docker compose ps` with the given compose files and
// returns a map of compose service name → NodeStatus. Returns nil on any error.
func composeNodeStatuses(composeFiles []string) map[string]ui.NodeStatus {
	if len(composeFiles) == 0 {
		return nil
	}
	// "docker compose ps --format {{.Service}} --filter status=running" lists
	// only running service names. We collect those, then mark all others stopped.
	runningArgs := buildComposeArgs(composeFiles, "ps", "--format", "{{.Service}}", "--filter", "status=running")
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
	allArgs := buildComposeArgs(composeFiles, "ps", "--format", "{{.Service}}", "--all")
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

// buildComposeArgs constructs `["compose", "-f", file..., command, extraArgs...]`.
func buildComposeArgs(composeFiles []string, command string, extraArgs ...string) []string {
	args := []string{"compose"}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	args = append(args, command)
	args = append(args, extraArgs...)
	return args
}
