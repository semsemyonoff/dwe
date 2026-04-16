package command

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newToolCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Manage optional tools",
		Long: `List, enable, or disable optional tool services (adminer, redis_insight, mailpit).

Enabling or disabling a tool writes the change to devbox/local.yml and regenerates .env.
Use 'devbox up' to start newly enabled tools.`,
		Example: `  devbox tools list
  devbox tools enable adminer
  devbox tools disable mailpit`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newToolListCmd(flags))
	cmd.AddCommand(newToolEnableCmd(flags))
	cmd.AddCommand(newToolDisableCmd(flags))
	return cmd
}

func newToolListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all tools and their status",
		Long:    `Show all optional tools with their host, port, enabled state, and running status.`,
		Example: "  devbox tools list",
		Args:    cobra.NoArgs,
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

// runToolList prints the tool list as a styled Lipgloss table.
func runToolList(w *render.Writer, cfg *config.DevboxConfig, isRunning containerCheckFn) error {
	toolData := buildToolRows(cfg)
	projectFull := cfg.Project.FullName()

	rows := make([]ui.ToolTableRow, len(toolData))
	for i, t := range toolData {
		running := false
		if t.Enabled {
			running = isRunning(projectFull, t.Container)
		}
		rows[i] = ui.ToolTableRow{
			Name:      t.Name,
			Host:      t.Host,
			Port:      t.Port,
			Container: t.Container,
			Enabled:   t.Enabled,
			Running:   running,
		}
	}

	_, _ = fmt.Fprintln(w.Writer(), ui.RenderToolTable(rows))
	return nil
}

func newToolEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <tool>",
		Short: "Enable an optional tool (writes to devbox/local.yml)",
		Long: `Enable an optional tool by writing tools.<name>.enabled = true to devbox/local.yml.

Available tools: adminer, redis_insight, mailpit.
The .env file is regenerated automatically after the change.`,
		Example:           "  devbox tools enable adminer",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: toolNameCompletion,
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
		Long: `Disable an optional tool by writing tools.<name>.enabled = false to devbox/local.yml.

Available tools: adminer, redis_insight, mailpit.
The .env file is regenerated automatically after the change.`,
		Example:           "  devbox tools disable adminer",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: toolNameCompletion,
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

// toolNameCompletion completes tool names from the known tools set.
func toolNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(knownTools))
	for name := range knownTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
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
