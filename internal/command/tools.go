package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
Use 'devbox up' to start newly enabled tools.

Use 'tools status' to display a read-only table of all tools and their current state.
Use 'tools list' for an interactive toggle form (TTY) or the same table (non-TTY).`,
		Example: `  devbox tools status
  devbox tools list
  devbox tools enable adminer
  devbox tools disable mailpit`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newToolStatusCmd(flags))
	cmd.AddCommand(newToolListCmd(flags))
	cmd.AddCommand(newToolEnableCmd(flags))
	cmd.AddCommand(newToolDisableCmd(flags))
	return cmd
}

func newToolStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show all tools and their current state (read-only table)",
		Long:    `Show all optional tools with their host, port, enabled state, and running status.`,
		Example: "  devbox tools status",
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

func newToolListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Toggle tools interactively (TTY) or show status table (non-TTY)",
		Long: `Toggle optional tools on or off using an interactive multi-select form.

On submit, newly-checked tools are enabled and newly-unchecked tools are
disabled in devbox/local.yml; .env is regenerated once.

In non-TTY mode (piped stdin or no terminal) the command falls back to printing
the read-only status table.`,
		Example: `  devbox tools list
  devbox tools list | cat   # non-TTY: prints the table`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.configPath, cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Non-TTY: fall back to the read-only table.
			if !ui.IsInteractiveFn(cmd.InOrStdin()) {
				projectName, err := resolveProjectName(flags.configPath, cfg)
				if err != nil {
					return err
				}
				isRunning := func(_, container string) bool {
					return containerRunning(projectName, container)
				}
				return runToolList(render.Stdout(), cfg, isRunning)
			}

			// TTY: build multi-select items from tool rows.
			rows := buildToolRows(cfg)
			items := make([]ui.MultiSelectItem, len(rows))
			for i, row := range rows {
				items[i] = ui.MultiSelectItem{
					Key:      row.Name,
					Label:    row.Name,
					Locked:   false,
					Selected: row.Enabled,
				}
			}

			result, err := runMultiSelect("Toggle tools:", items)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}

			toEnable, toDisable := diffToolSelection(rows, result.Kept)
			if len(toEnable) == 0 && len(toDisable) == 0 {
				return nil
			}

			baseDir := filepath.Dir(flags.configPath)
			for _, name := range toEnable {
				if err := setToolEnabledNoRegen(flags.configPath, name, true); err != nil {
					return err
				}
			}
			for _, name := range toDisable {
				if err := setToolEnabledNoRegen(flags.configPath, name, false); err != nil {
					return err
				}
			}

			// Print one-line summary of all changes.
			var parts []string
			if len(toEnable) > 0 {
				parts = append(parts, "enabled: "+strings.Join(toEnable, ", "))
			}
			if len(toDisable) > 0 {
				parts = append(parts, "disabled: "+strings.Join(toDisable, ", "))
			}
			render.Stdout().Success(strings.Join(parts, "; "))

			return regenEnv(flags.configPath, baseDir)
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
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setToolEnabled(flags.configPath, name, true)
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
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setToolEnabled(flags.configPath, name, false)
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
// - One or more → selector is always invoked.
func pickToolCandidates(rows []toolRow, statusLabel, title string, selector selectToggleFn) (string, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("no %s tools found", statusLabel)
	}
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

// setToolEnabledNoRegen writes tools.<name>.enabled = value to devbox/local.yml
// without printing or regenerating .env. Used by the batch multi-toggle path.
func setToolEnabledNoRegen(configPath string, name string, enabled bool) error {
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
	return nil
}

// setToolEnabled writes tools.<name>.enabled = value to devbox/local.yml,
// prints a confirmation, and regenerates .env.
func setToolEnabled(configPath string, name string, enabled bool) error {
	if err := setToolEnabledNoRegen(configPath, name, enabled); err != nil {
		return err
	}

	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	w := render.Stdout()
	if enabled {
		w.Success(fmt.Sprintf("tool %q enabled (written to %s)", name, localPath))
	} else {
		w.Success(fmt.Sprintf("tool %q disabled (written to %s)", name, localPath))
	}
	return regenEnv(configPath, baseDir)
}
