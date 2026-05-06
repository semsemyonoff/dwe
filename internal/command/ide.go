package command

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

// skippedService carries information about a service that was skipped during IDE rendering.
type skippedService struct {
	Name   string // service name
	Reason string // "disabled-by-policy" | "empty-dir" | "lost-collision"
	Dir    string // set for "lost-collision" only
	Winner string // set for "lost-collision" only (name of the winning service)
}

// extendsDepth computes the depth of a service's extends chain.
// Returns (depth, capped): depth is the number of hops to the root;
// capped is true if depth hit the 32-hop limit (defense-in-depth cycle guard).
func extendsDepth(services map[string]config.ServiceConfig, name string) (int, bool) {
	const maxDepth = 32
	depth := 0
	current := name
	for {
		if depth >= maxDepth {
			return maxDepth, true
		}
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			return depth, false
		}
		current = svc.Extends
		depth++
	}
}

// selectIDEServices filters and resolves IDE-enabled services.
// It returns a list of selected service names (sorted lexicographically) and
// a list of services that were skipped with reason-specific context.
//
// Selection logic (in order):
// 1. Gate on both flags: services where svc.Enabled==false or svc.IDERenderEnabled()==false are dropped.
// 2. Normalize Dir: services with empty (after TrimSpace) Dir are dropped.
// 3. Group by filepath.Clean(Dir) and resolve collisions: when multiple services
//    share the same Dir, the deepest extends chain wins; ties are broken lexicographically.
func selectIDEServices(services map[string]config.ServiceConfig) (selected []string, skipped []skippedService) {
	var allSkipped []skippedService

	// Step A: gate on both Enabled and IDERenderEnabled.
	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if !svc.Enabled || !svc.IDERenderEnabled() {
			reason := "disabled-by-policy"
			allSkipped = append(allSkipped, skippedService{Name: name, Reason: reason})
			continue
		}
		enabled[name] = svc
	}

	// Step B: drop services with empty Dir.
	dirNormalized := make(map[string]config.ServiceConfig)
	for name, svc := range enabled {
		if strings.TrimSpace(svc.Dir) == "" {
			allSkipped = append(allSkipped, skippedService{Name: name, Reason: "empty-dir"})
			continue
		}
		dirNormalized[name] = svc
	}

	// Step C: group by filepath.Clean(Dir) and resolve collisions.
	dirGroups := make(map[string][]string)
	for name, svc := range dirNormalized {
		cleanDir := filepath.Clean(svc.Dir)
		dirGroups[cleanDir] = append(dirGroups[cleanDir], name)
	}

	// For each group, pick the winner (deepest extends chain; tie-break by name).
	selectedSet := make(map[string]bool)
	for dir, names := range dirGroups {
		if len(names) == 1 {
			selectedSet[names[0]] = true
			continue
		}

		// Multiple services share this dir: find the deepest extends chain.
		sort.Strings(names) // tie-break: lexicographically first among deepest
		var deepest string
		maxDepth := -1
		for _, name := range names {
			depth, _ := extendsDepth(dirNormalized, name)
			if depth > maxDepth {
				maxDepth = depth
				deepest = name
			}
		}

		selectedSet[deepest] = true
		for _, name := range names {
			if name != deepest {
				allSkipped = append(allSkipped, skippedService{
					Name:   name,
					Reason: "lost-collision",
					Dir:    dir,
					Winner: deepest,
				})
			}
		}
	}

	// Collect selected names and sort.
	for name := range selectedSet {
		selected = append(selected, name)
	}
	sort.Strings(selected)

	// Sort skipped by name for determinism.
	sort.Slice(allSkipped, func(i, j int) bool {
		return allSkipped[i].Name < allSkipped[j].Name
	})
	skipped = allSkipped

	return selected, skipped
}

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

Enabled editors are controlled by the ide: section in devbox/defaults.yml.
Templates are read from devbox/templates/ide/ in the project root.`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: serviceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			projectRoot := flags.ProjectRoot()
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

// loadIDETemplate reads a template from devbox/templates/ide/<name>.tpl
// relative to the project root.
func loadIDETemplate(projectRoot, templateName string) (string, error) {
	path := filepath.Join(projectRoot, "devbox", "templates", "ide", templateName+".tpl")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read ide template %s: %w", templateName, err)
	}
	return string(data), nil
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
		tpl, err := loadIDETemplate(projectRoot, "devcontainer.json")
		switch {
		case errors.Is(err, os.ErrNotExist):
			w.Warning("ide [devcontainer] — template not found, skipping (add devbox/templates/ide/devcontainer.json.tpl)")
		case err != nil:
			return err
		default:
			dest := filepath.Join(serviceDir, ".devcontainer", "devcontainer.json")
			if err := renderIDETemplate(tpl, "devcontainer.json", data, dest); err != nil {
				return err
			}
			w.Success(fmt.Sprintf("ide [devcontainer] → %s", dest))
		}
	}

	if cfg.IDE.JetBrains.Enabled {
		w.Warning("ide [jetbrains] — not yet implemented, skipping")
	}

	if cfg.IDE.VSCode.Enabled {
		launchTpl, err := loadIDETemplate(projectRoot, "vscode_launch.json")
		switch {
		case errors.Is(err, os.ErrNotExist):
			w.Warning("ide [vscode] — template not found, skipping (add devbox/templates/ide/vscode_launch.json.tpl)")
		case err != nil:
			return err
		default:
			launchDest := filepath.Join(serviceDir, ".vscode", "launch.json")
			if err := renderIDETemplate(launchTpl, "launch.json", data, launchDest); err != nil {
				return err
			}
			w.Success(fmt.Sprintf("ide [vscode]       → %s", launchDest))

			settingsTpl, err := loadIDETemplate(projectRoot, "vscode_settings.json")
			switch {
			case errors.Is(err, os.ErrNotExist):
				w.Warning("ide [vscode] — template not found, skipping (add devbox/templates/ide/vscode_settings.json.tpl)")
			case err != nil:
				return err
			default:
				settingsDest := filepath.Join(serviceDir, ".vscode", "settings.json")
				if err := renderIDETemplate(settingsTpl, "settings.json", data, settingsDest); err != nil {
					return err
				}
				w.Success(fmt.Sprintf("ide [vscode]       → %s", settingsDest))
			}
		}
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
