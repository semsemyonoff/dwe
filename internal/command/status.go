package command

import (
	"fmt"
	"path/filepath"

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
and a tools table with live container status.`,
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
			return runStatus(render.Stdout(), cfg, containerRunning)
		},
	}
}

// StackHealth represents the overall running health of the stack.
type StackHealth int

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

// runStatus renders the stack health indicator, service table, and tool table.
func runStatus(w *render.Writer, cfg *config.DevboxConfig, isRunning containerCheckFn) error {
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
		indicator = "● running"
	case StackPartial:
		indicator = "◐ partial"
	default:
		indicator = "○ stopped"
	}

	fmt.Fprintf(w.Writer(), "Stack: %s\n\n", indicator)
	fmt.Fprintln(w.Writer(), ui.RenderServiceTable(svcRows))
	fmt.Fprintln(w.Writer(), ui.RenderToolTable(toolRows))
	return nil
}
