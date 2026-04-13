package command

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

//go:embed templates/ide/devcontainer.json.tpl
var devcontainerTpl string

//go:embed templates/ide/vscode_launch.json.tpl
var vscodeLaunchTpl string

//go:embed templates/ide/vscode_settings.json.tpl
var vscodeSettingsTpl string

// ideTemplateData is passed to IDE config templates.
type ideTemplateData struct {
	Project    config.ProjectConfig
	Service    string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
	IDE        config.IDEConfig
}

// newRenderIDECmd creates the `devbox render ide [service]` command.
// It generates IDE-specific config files into each service directory.
// When a service name is provided only that service is processed;
// otherwise all services in config are processed.
func newRenderIDECmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ide [service]",
		Short: "Generate IDE configs into service directories",
		Long: `Generate IDE-specific config files for each enabled editor.

For each service directory (services/<name>/):
  - devcontainer:  .devcontainer/devcontainer.json
  - vscode:        .vscode/launch.json, .vscode/settings.json

Enabled editors are controlled by the ide: section in devbox/defaults.yml.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			projectRoot := filepath.Dir(flags.configPath)
			w := render.Stdout()

			// Determine which services to process.
			serviceNames := sortedKeys(cfg.Services)
			if len(args) == 1 {
				name := args[0]
				if _, ok := cfg.Services[name]; !ok {
					return fmt.Errorf("service %q not found in config", name)
				}
				serviceNames = []string{name}
			}

			for _, name := range serviceNames {
				svc := cfg.Services[name]
				if err := renderIDEConfigs(projectRoot, name, svc, cfg, w); err != nil {
					return fmt.Errorf("service %s: %w", name, err)
				}
			}
			return nil
		},
	}
}

// renderIDEConfigs generates IDE config files for a single service.
func renderIDEConfigs(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	data := ideTemplateData{
		Project:    cfg.Project,
		Service:    name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		IDE:        cfg.IDE,
	}

	serviceDir := filepath.Join(projectRoot, svc.Dir)

	if cfg.IDE.Devcontainer.Enabled {
		dest := filepath.Join(serviceDir, ".devcontainer", "devcontainer.json")
		if err := renderIDETemplate(devcontainerTpl, "devcontainer.json", data, dest); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ide [devcontainer] → %s", dest))
	}

	if cfg.IDE.JetBrains.Enabled {
		w.Warning("ide [jetbrains] — not yet implemented, skipping")
	}

	if cfg.IDE.VSCode.Enabled {
		launchDest := filepath.Join(serviceDir, ".vscode", "launch.json")
		if err := renderIDETemplate(vscodeLaunchTpl, "launch.json", data, launchDest); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ide [vscode]       → %s", launchDest))

		settingsDest := filepath.Join(serviceDir, ".vscode", "settings.json")
		if err := renderIDETemplate(vscodeSettingsTpl, "settings.json", data, settingsDest); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ide [vscode]       → %s", settingsDest))
	}

	return nil
}

// renderIDETemplate executes a Go template string against data and writes the
// result to dest, creating parent directories as needed.
func renderIDETemplate(tplStr, name string, data ideTemplateData, dest string) error {
	t, err := template.New(name).Parse(tplStr)
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template %s: %w", name, err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dest, err)
	}

	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}
