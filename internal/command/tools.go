package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newToolCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tools",
		Short:        "Manage optional tools",
		SilenceUsage: true,
	}
	cmd.AddCommand(newToolListCmd(flags))
	cmd.AddCommand(newToolEnableCmd(flags))
	cmd.AddCommand(newToolDisableCmd(flags))
	return cmd
}

func newToolListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tools and their status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return runToolList(render.Stdout(), cfg, containerRunning)
		},
		SilenceUsage: true,
	}
}

// runToolList prints the tool list with aligned columns and colored status.
func runToolList(w *render.Writer, cfg *config.DevboxConfig, isRunning containerCheckFn) error {
	rows := buildToolRows(cfg)

	// Compute column widths.
	maxName := len("NAME")
	maxHost := len("HOST")
	maxPort := len("PORT")
	for _, t := range rows {
		if len(t.Name) > maxName {
			maxName = len(t.Name)
		}
		if len(t.Host) > maxHost {
			maxHost = len(t.Host)
		}
		port := fmt.Sprintf("%d", t.Port)
		if len(port) > maxPort {
			maxPort = len(port)
		}
	}

	statusWidth := 5 // "✔ on " / "✘ off"

	projectFull := cfg.Project.FullName()
	out := w.Writer()

	_, _ = fmt.Fprintf(out,
		"  %s%-*s  %-*s  %-*s  %-*s  %s%s\n",
		render.White, maxName, "NAME", maxHost, "HOST", maxPort, "PORT", statusWidth, "STATE", "RUNNING", render.Reset,
	)

	for _, t := range rows {
		var icon, label, statusColor string
		if t.Enabled {
			icon, label, statusColor = "✔", "on", render.Green
		} else {
			icon, label, statusColor = "✘", "off", render.Gray
		}
		statusText := icon + " " + label
		statusPad := strings.Repeat(" ", max(statusWidth-len(icon)-1-len(label), 0))

		var runStr string
		if t.Enabled {
			if isRunning(projectFull, t.Container) {
				runStr = render.Green + "running" + render.Reset
			} else {
				runStr = render.Yellow + "stopped" + render.Reset
			}
		} else {
			runStr = render.Gray + "—" + render.Reset
		}

		nameColor := render.Blue
		hostColor := render.Reset
		portStr := fmt.Sprintf("%d", t.Port)
		if !t.Enabled {
			nameColor = render.Gray
			hostColor = render.Gray
		}

		_, _ = fmt.Fprintf(out,
			"  %s%-*s%s  %s%-*s%s  %-*s  %s%s%s%s  %s\n",
			nameColor, maxName, t.Name, render.Reset,
			hostColor, maxHost, t.Host, render.Reset,
			maxPort, portStr,
			statusColor, statusText, render.Reset, statusPad,
			runStr,
		)
	}

	return nil
}

func newToolEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <tool>",
		Short: "Enable an optional tool (writes to devbox/local.yml)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return setToolEnabled(flags.configPath, cfg, args[0], true)
		},
		SilenceUsage: true,
	}
}

func newToolDisableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable <tool>",
		Short: "Disable an optional tool (writes to devbox/local.yml)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return setToolEnabled(flags.configPath, cfg, args[0], false)
		},
		SilenceUsage: true,
	}
}

// knownTools is the set of valid tool names.
var knownTools = map[string]bool{
	"adminer":       true,
	"redis_insight": true,
	"mailpit":       true,
}

// setToolEnabled writes tools.<name>.enabled = value to devbox/local.yml,
// then regenerates .env.
func setToolEnabled(configPath string, cfg *config.DevboxConfig, name string, enabled bool) error {
	if !knownTools[name] {
		return fmt.Errorf("tool %q not found", name)
	}

	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	local := make(map[string]any)
	if data, err := os.ReadFile(localPath); err == nil {
		if err := yaml.Unmarshal(data, &local); err != nil {
			return fmt.Errorf("parse %s: %w", localPath, err)
		}
		if local == nil {
			local = make(map[string]any)
		}
	}

	toolsMap, ok := local["tools"].(map[string]any)
	if !ok {
		toolsMap = make(map[string]any)
		local["tools"] = toolsMap
	}
	entry, ok := toolsMap[name].(map[string]any)
	if !ok {
		entry = make(map[string]any)
		toolsMap[name] = entry
	}
	entry["enabled"] = enabled

	data, err := yaml.Marshal(local)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}

	w := render.Stdout()
	if enabled {
		w.Success(fmt.Sprintf("tool %q enabled (written to %s)", name, localPath))
	} else {
		w.Success(fmt.Sprintf("tool %q disabled (written to %s)", name, localPath))
	}

	return regenEnv(configPath, baseDir)
}
