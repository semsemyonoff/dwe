package command

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/envfile"
	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/render"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newToolCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Toggle tools (interactive) or enable/disable individually",
		Long: `Open an interactive multi-select form to enable or disable optional tools.

Changes are written to devbox/local.yml and .env is regenerated.
Use 'devbox run' to start newly enabled tools.

For a read-only view, run 'devbox status tools'.`,
		Example: `  devbox tools
  devbox tools enable adminer
  devbox tools disable mailpit`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolsToggle(cmd, flags)
		},
	}
	cmd.AddCommand(newToolEnableCmd(flags))
	cmd.AddCommand(newToolDisableCmd(flags))
	return cmd
}

// runToolsToggle opens the interactive multi-select toggle form for tools.
// Non-TTY returns ErrInteractiveRequired. No-togglable short-circuits.
func runToolsToggle(cmd *cobra.Command, flags *rootFlags) error {
	applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if !ui.IsInteractiveFn(cmd.InOrStdin()) {
		return fmt.Errorf("%w: tools: interactive toggle requires a TTY; use 'devbox status tools' for read-only view", ErrInteractiveRequired)
	}

	rows := stack.BuildToolRows(cfg)
	if len(rows) == 0 {
		return fmt.Errorf("nothing to toggle, see 'devbox status tools'")
	}

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

	toolSelections := make([]localconfig.ToolSelection, len(rows))
	for i, row := range rows {
		toolSelections[i] = localconfig.ToolSelection{Name: row.Name, Enabled: row.Enabled}
	}
	toEnable, toDisable := localconfig.DiffToolSelection(toolSelections, result.Kept)
	if len(toEnable) == 0 && len(toDisable) == 0 {
		return nil
	}

	if err := applyToolTogglesBatch(cfg, flags.configPath, toEnable, toDisable); err != nil {
		return err
	}

	var parts []string
	if len(toEnable) > 0 {
		parts = append(parts, "enabled: "+strings.Join(toEnable, ", "))
	}
	if len(toDisable) > 0 {
		parts = append(parts, "disabled: "+strings.Join(toDisable, ", "))
	}
	render.NewWriter(cmd.OutOrStdout()).Success(strings.Join(parts, "; "))

	envPath, err := envfile.Regenerate(flags.configPath)
	if err != nil {
		return err
	}
	render.NewWriter(cmd.OutOrStdout()).Info(fmt.Sprintf(".env regenerated → %s", envPath))
	return nil
}

func newToolEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable [tool]",
		Short: "Enable an optional tool (writes to devbox/local.yml)",
		Long: `Enable an optional tool by writing tools.<name>.enabled = true to devbox/local.yml.

Available tools are configured in devbox/tools.yml; run 'devbox status tools' to list them.
The .env file is regenerated automatically after the change.

When no tool name is given, an interactive selector shows all currently
disabled tools.`,
		Example:           "  devbox tools enable adminer",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: toolCompletion(flags, completeToolDisabled),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				if !ui.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no tool name given; pass a tool name or run in an interactive terminal")
				}
				name, err = pickToolToEnable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setToolEnabled(cfg, flags.configPath, name, true)
		},
		SilenceUsage: true,
	}
}

func newToolDisableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable [tool]",
		Short: "Disable an optional tool (writes to devbox/local.yml)",
		Long: `Disable an optional tool by writing tools.<name>.enabled = false to devbox/local.yml.

Available tools are configured in devbox/tools.yml; run 'devbox status tools' to list them.
The .env file is regenerated automatically after the change.

When no tool name is given, an interactive selector shows all currently
enabled tools.`,
		Example:           "  devbox tools disable adminer",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: toolCompletion(flags, completeToolEnabled),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				if !ui.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no tool name given; pass a tool name or run in an interactive terminal")
				}
				name, err = pickToolToDisable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setToolEnabled(cfg, flags.configPath, name, false)
		},
		SilenceUsage: true,
	}
}

// pickToolToEnable returns the name of a disabled tool to enable.
func pickToolToEnable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []stack.ToolRow
	for _, row := range stack.BuildToolRows(cfg) {
		if !row.Enabled {
			candidates = append(candidates, row)
		}
	}
	return pickToolCandidates(candidates, "disabled", "Select a tool to enable:", selector)
}

// pickToolToDisable returns the name of an enabled tool to disable.
func pickToolToDisable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []stack.ToolRow
	for _, row := range stack.BuildToolRows(cfg) {
		if row.Enabled {
			candidates = append(candidates, row)
		}
	}
	return pickToolCandidates(candidates, "enabled", "Select a tool to disable:", selector)
}

func pickToolCandidates(rows []stack.ToolRow, statusLabel, title string, selector selectToggleFn) (string, error) {
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
	if idx < 0 || idx >= len(rows) {
		return "", fmt.Errorf("selector returned invalid index %d for %d candidates", idx, len(rows))
	}
	return rows[idx].Name, nil
}

func toolNameSet(cfg *config.DevboxConfig) map[string]bool {
	set := make(map[string]bool, len(cfg.Tools))
	for name := range cfg.Tools {
		set[name] = true
	}
	return set
}

// tool completion filters.
type toolFilter int

const (
	completeToolDisabled toolFilter = iota
	completeToolEnabled
)

func toolCompletion(flags *rootFlags, filter toolFilter) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := completionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for name, tool := range cfg.Tools {
			switch filter {
			case completeToolDisabled:
				if !tool.Enabled {
					names = append(names, name)
				}
			case completeToolEnabled:
				if tool.Enabled {
					names = append(names, name)
				}
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func applyToolTogglesBatch(cfg *config.DevboxConfig, configPath string, toEnable, toDisable []string) error {
	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	local, err := localconfig.LoadLocalYAML(localPath)
	if err != nil {
		return err
	}

	knownTools := toolNameSet(cfg)
	if err := localconfig.ApplyToolTogglesToYAML(knownTools, local, toEnable, toDisable); err != nil {
		return err
	}

	return localconfig.WriteLocalYAML(localPath, local)
}

func setToolEnabled(cfg *config.DevboxConfig, configPath string, name string, enabled bool) error {
	var toEnable, toDisable []string
	if enabled {
		toEnable = []string{name}
	} else {
		toDisable = []string{name}
	}
	if err := applyToolTogglesBatch(cfg, configPath, toEnable, toDisable); err != nil {
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
	envPath, err := envfile.Regenerate(configPath)
	if err != nil {
		return err
	}
	render.Stdout().Info(fmt.Sprintf(".env regenerated → %s", envPath))
	return nil
}
