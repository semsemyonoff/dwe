package command

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
	"devbox-cli/internal/render"
	"devbox-cli/internal/templates/ide"

	"github.com/spf13/cobra"
)

// newRenderIDECmd creates the `devbox render ide [service]` command.
// It generates IDE-specific config files into each service directory.
// When a service name is provided only that service is processed;
// otherwise all services matching the IDE selection policy are processed.
func newRenderIDECmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ide [service]",
		Short: "Generate IDE configs from template packs",
		Long: `Generate IDE-specific config files for each enabled service from a template pack.

The command reads manifest.yml from the chosen template pack
(devbox/templates/ide/<pack-name>/) and renders each declared entry into the
corresponding location within the service directory. For example:
  manifest.yml: render: [{from: settings.json.tmpl, to: .vscode/settings.json}]
  → services/main/.vscode/settings.json

A manifest.yml is required; packs without one produce an error with a migration
hint. See docs/reference/render/ide.md for the manifest schema and migration.

Template pack resolution (explicit is strict; implicit chain: service-name → default):
  1. If render.ide.template is set in the service config, use that pack (explicit, strict)
  2. Otherwise, try devbox/templates/ide/<service-name>/
  3. If not found, use devbox/templates/ide/default/
  4. If none exist, skip with a warning (implicit missing pack)

Services that participate in IDE rendering:
  - Type 'app' (default) has render.ide.enabled: true by default
  - Other types require explicit render.ide.enabled: true in the config

When a service name is given, it is treated as a hub anchor: if multiple
services share its dir (e.g. main and main-debug both point to services/main),
the IDE collision-policy winner (deepest extends) is rendered. This means
'render ide main' renders main-debug whenever main-debug is enabled.`,
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
			var serviceNames []string
			if len(args) == 1 {
				// Explicit service argument: validate thoroughly, then resolve
				// hub-anchor semantics — if multiple services share this dir,
				// the IDE collision-policy winner (deepest extends) renders.
				name := args[0]
				if err := validateExplicitIDEArg(name, cfg.Services); err != nil {
					return err
				}

				winner := resolveIDEHubAnchor(name, cfg.Services)
				if winner != name {
					w.Info(fmt.Sprintf("ide [%s] — resolved to %s (hub %s)", name, winner, filepath.Clean(cfg.Services[name].Dir)))
				}
				serviceNames = []string{winner}
			} else {
				// No explicit service: use selection policy
				selected, skipped := ide.SelectServices(cfg.Services)
				serviceNames = selected

				// Emit warnings only for actionable skips; policy-based skips
				// (service-disabled, ide-disabled, ide-policy) are expected and not reported.
				for _, skip := range skipped {
					switch skip.Reason {
					case "empty-dir":
						w.Warning(fmt.Sprintf("ide [%s] — skipped (service has no dir or dir is project root)", skip.Name))
					case "lost-collision":
						w.Warning(fmt.Sprintf("ide [%s] — skipped (dir %s rendered by %s)", skip.Name, skip.Dir, skip.Winner))
					}
				}

				if len(serviceNames) == 0 {
					w.Info("no services match the IDE rendering policy")
					return nil
				}
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

// resolveIDEHubAnchor treats name as a hub anchor and returns the IDE collision
// winner among services that share name's Dir. Applies the same gating used by
// selectIDEServices (Enabled + IDERenderEnabled), then picks the deepest extends
// chain (ties broken lexicographically). Returns name unchanged when there are
// no qualifying siblings. The caller must have already validated name via
// validateExplicitIDEArg, so name itself is guaranteed to qualify.
func resolveIDEHubAnchor(name string, services map[string]config.ServiceConfig) string {
	svc := services[name]
	cleanDir := filepath.Clean(svc.Dir)

	var candidates []string
	for n, s := range services {
		if filepath.Clean(s.Dir) != cleanDir {
			continue
		}
		if !s.Enabled {
			continue
		}
		if enabled, _ := s.IDERenderEnabledExplicit(); !enabled {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) <= 1 {
		return name
	}

	sort.Strings(candidates) // tie-break: lexicographically first among deepest
	var deepest string
	maxDepth := -1
	for _, c := range candidates {
		d, _ := ide.ExtendsDepth(services, c)
		if d > maxDepth {
			maxDepth = d
			deepest = c
		}
	}
	return deepest
}

// validateExplicitIDEArg validates the explicit service argument for `devbox render ide <service>`.
// Checks in priority order: not-found → disabled → no-dir → IDE policy.
// Returns nil when the service is valid and renderable.
func validateExplicitIDEArg(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render IDE files", name)
	}
	enabled, explicit := svc.IDERenderEnabledExplicit()
	if !enabled {
		if explicit {
			return fmt.Errorf("service %q has render.ide.enabled: false", name)
		}
		return fmt.Errorf("service %q (type: %s) does not participate in IDE rendering by default; set render.ide.enabled: true to opt in", name, svc.Type)
	}
	return nil
}

// renderIDEConfigs generates IDE config files for a single service using the
// manifest-driven pack flow (parity with `render ai`).
func renderIDEConfigs(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	if strings.TrimSpace(svc.Dir) == "" {
		return fmt.Errorf("service %q has no dir; cannot render IDE files", name)
	}

	data := ide.TemplateData{
		Project:    cfg.Project,
		Service:    name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
	}

	serviceDir := filepath.Join(projectRoot, svc.Dir)
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	absDir, err := filepath.Abs(serviceDir)
	if err != nil {
		return fmt.Errorf("resolve service dir: %w", err)
	}
	relDir, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return fmt.Errorf("service dir %q escapes project root", svc.Dir)
	}
	if relDir == "." || relDir == ".." || strings.HasPrefix(relDir, ".."+string(filepath.Separator)) {
		return fmt.Errorf("service dir %q escapes project root", svc.Dir)
	}
	if err := pathsafe.CheckNoSymlinks(absRoot, absDir, "service dir"); err != nil {
		return err
	}

	packDir, packName, found, err := ide.ResolveTemplatePack(svc, absRoot, name)
	if err != nil {
		return err
	}
	if !found {
		w.Warning(fmt.Sprintf("ide [%s] — skipped (no template pack found)", name))
		return nil
	}

	m, err := ide.LoadManifest(packDir)
	if err != nil {
		return fmt.Errorf("ide pack %q: %w; IDE packs now require a manifest.yml — see docs/reference/render/ide.md for the migration", packName, err)
	}
	if err := ide.ValidateManifest(m, absRoot, packName, absDir); err != nil {
		return fmt.Errorf("invalid ide manifest: %w", err)
	}

	for _, entry := range m.Render {
		fromOverride, err := ide.RenderTemplateFile(absRoot, packName, entry.From, data, entry.To, absDir, absRoot)
		if err != nil {
			return err
		}
		if fromOverride {
			w.Info(fmt.Sprintf("using local override: devbox/templates/ide/%s.local/%s", packName, entry.From))
		}
		w.Success(fmt.Sprintf("ide → %s", filepath.Join(svc.Dir, entry.To)))
	}

	for _, entry := range m.Symlinks {
		if err := ide.EnsureRelativeSymlink(entry.Link, entry.To, absDir, absRoot); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ide → %s ⇒ %s", filepath.Join(svc.Dir, entry.Link), entry.To))
	}

	return nil
}
