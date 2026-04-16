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
			applyStyles(flags.configPath, cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			projectName, err := resolveProjectName(flags.configPath, cfg)
			if err != nil {
				return err
			}
			isRunning := func(_, container string) bool {
				return containerRunning(projectName, container)
			}
			return runToolList(render.Stdout(), cfg, isRunning)
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
		Use:   "enable [tool]",
		Short: "Enable an optional tool (writes to devbox/local.yml)",
		Long: `Enable an optional tool by writing tools.<name>.enabled = true to devbox/local.yml.

Available tools: adminer, redis_insight, mailpit.
The .env file is regenerated automatically after the change.

When no tool name is given, an interactive selector shows all currently
disabled tools.`,
		Example:           "  devbox tools enable adminer",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: toolNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				name, err = pickToolToEnable(cfg, defaultSelectToggle)
				if err != nil {
					return err
				}
			}
			return setToolEnabled(flags.configPath, cfg, name, true)
		},
		SilenceUsage: true,
	}
}

func newToolDisableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable [tool]",
		Short: "Disable an optional tool (writes to devbox/local.yml)",
		Long: `Disable an optional tool by writing tools.<name>.enabled = false to devbox/local.yml.

Available tools: adminer, redis_insight, mailpit.
The .env file is regenerated automatically after the change.

When no tool name is given, an interactive selector shows all currently
enabled tools.`,
		Example:           "  devbox tools disable adminer",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: toolNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				name, err = pickToolToDisable(cfg, defaultSelectToggle)
				if err != nil {
					return err
				}
			}
			return setToolEnabled(flags.configPath, cfg, name, false)
		},
		SilenceUsage: true,
	}
}

// pickToolToEnable returns the name of a disabled tool to enable.
// If no disabled tools exist, returns an error.
// If exactly one disabled tool exists, it is auto-selected (no selector invoked).
// Otherwise the selector is called.
func pickToolToEnable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []toolRow
	for _, row := range buildToolRows(cfg) {
		if !row.Enabled {
			candidates = append(candidates, row)
		}
	}
	return pickToolCandidates(candidates, "disabled", "Select a tool to enable:", selector)
}

// pickToolToDisable returns the name of an enabled tool to disable.
// If no enabled tools exist, returns an error.
// If exactly one enabled tool exists, it is auto-selected (no selector invoked).
// Otherwise the selector is called.
func pickToolToDisable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []toolRow
	for _, row := range buildToolRows(cfg) {
		if row.Enabled {
			candidates = append(candidates, row)
		}
	}
	return pickToolCandidates(candidates, "enabled", "Select a tool to disable:", selector)
}

// pickToolCandidates resolves a tool name from a candidate list.
// - Empty list → error mentioning statusLabel.
// - Exactly one → auto-selected.
// - Multiple → selector is invoked.
func pickToolCandidates(rows []toolRow, statusLabel, title string, selector selectToggleFn) (string, error) {
	switch len(rows) {
	case 0:
		return "", fmt.Errorf("no %s tools found", statusLabel)
	case 1:
		return rows[0].Name, nil
	default:
		items := make([]ui.SelectorItem, len(rows))
		for i, row := range rows {
			items[i] = ui.SelectorItem{
				Label:  row.Name,
				Status: statusLabel,
			}
		}
		idx, err := selector(title, items)
		if err != nil {
			return "", err
		}
		return rows[idx].Name, nil
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
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
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
