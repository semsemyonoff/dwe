package command

import (
	"fmt"
	"sort"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

// serviceRow holds a single row of the services topology table.
type serviceRow struct {
	Name      string
	Type      string
	Dir       string
	Container string
}

// toolRow holds a single row of the tools topology table.
type toolRow struct {
	Name    string
	Enabled bool
	Port    int
	Host    string
}

func newServicesCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "services",
		Short: "Print topology: services table and tools table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return runServices(render.Stdout(), cfg)
		},
		SilenceUsage: true,
	}
}

// runServices renders the services and tools topology tables to w.
func runServices(w *render.Writer, cfg *config.DevboxConfig) error {
	services := buildServiceRows(cfg)
	tools := buildToolRows(cfg)

	w.TableHeader("Services")
	w.Definition("NAME", "TYPE / DIR / CONTAINER", 2, "")
	for _, s := range services {
		container := s.Container
		if container == "" {
			container = "-"
		}
		w.Definition(s.Name, fmt.Sprintf("%s  %s  %s", s.Type, s.Dir, container), 2, "")
	}

	w.TableSubheader("Tools")
	w.Definition("NAME", "ENABLED / PORT / HOST", 2, "")
	for _, t := range tools {
		enabled := "no"
		if t.Enabled {
			enabled = "yes"
		}
		w.Definition(t.Name, fmt.Sprintf("%s  %d  %s", enabled, t.Port, t.Host), 2, "")
	}

	w.TableHeader("")
	return nil
}

// buildServiceRows returns an ordered list of service rows from cfg.
func buildServiceRows(cfg *config.DevboxConfig) []serviceRow {
	rows := make([]serviceRow, 0, len(cfg.Services))
	// Stable order: iterate over known service names in insertion order isn't
	// guaranteed with maps; sort by name for deterministic output.
	names := sortedKeys(cfg.Services)
	for _, name := range names {
		svc := cfg.Services[name]
		rows = append(rows, serviceRow{
			Name:      name,
			Type:      svc.Type,
			Dir:       svc.Dir,
			Container: svc.Container,
		})
	}
	return rows
}

// buildToolRows returns the fixed tool rows with enabled state, port, and host.
func buildToolRows(cfg *config.DevboxConfig) []toolRow {
	return []toolRow{
		{
			Name:    "adminer",
			Enabled: cfg.Tools.Adminer.Enabled,
			Port:    cfg.Runtime.Ports.Adminer,
			Host:    cfg.Runtime.Hosts.Adminer,
		},
		{
			Name:    "redis_insight",
			Enabled: cfg.Tools.RedisInsight.Enabled,
			Port:    cfg.Runtime.Ports.RedisInsight,
			Host:    cfg.Runtime.Hosts.RedisInsight,
		},
		{
			Name:    "mailpit",
			Enabled: cfg.Tools.Mailpit.Enabled,
			Port:    cfg.Runtime.Ports.Mailpit,
			Host:    cfg.Runtime.Hosts.Mailpit,
		},
	}
}

// sortedKeys returns the map keys sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
