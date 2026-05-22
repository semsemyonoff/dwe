package command

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	aipkg "devbox-cli/internal/templates/ai"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
	"devbox-cli/internal/render"
	"devbox-cli/internal/templates/manifest"

	"github.com/spf13/cobra"
)

// renderAgentsForService renders a single service's agents documentation.
// It resolves the template pack, loads and validates the manifest, and renders
// each entry in the manifest (files + symlinks).
func renderAgentsForService(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	if cfg == nil {
		return fmt.Errorf("ai: nil cfg")
	}
	// Validate that service has a directory
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render agents docs", name)
	}

	// Resolve paths
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	absHubDir := filepath.Join(absRoot, svc.Dir)

	// Validate hub dir is inside root (not equal)
	_, err = pathsafe.ContainedRel(absRoot, absHubDir)
	if err != nil {
		return fmt.Errorf("service dir escapes project root: %w", err)
	}

	// Check for symlinks in the hub dir path
	if err := pathsafe.CheckNoSymlinks(absRoot, absHubDir, "service dir"); err != nil {
		return err
	}

	// Resolve template pack
	packDir, packName, found, err := aipkg.ResolveTemplatePack(svc, absRoot, name)
	if err != nil {
		return err
	}
	if !found {
		tried := fmt.Sprintf("tried %s, default", name)
		if manifest.ValidatePackName(name) != nil {
			tried = "tried default"
		}
		w.Warning(fmt.Sprintf("ai [%s] — skipped (no template pack found; %s)", name, tried))
		return nil
	}

	// Load and validate manifest
	m, err := aipkg.LoadManifest(packDir)
	if err != nil {
		return err
	}
	if err := aipkg.ValidateManifest(m, absRoot, packName, absHubDir); err != nil {
		return fmt.Errorf("invalid agents manifest: %w", err)
	}

	// Prepare template data
	data := aipkg.TemplateData{
		Project:    cfg.Project,
		Service:    aipkg.ExtendsRoot(cfg.Services, name),
		Resolved:   name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		Services:   cfg.Services,
		Cfg:        cfg,
	}

	// Render each file in the manifest
	for _, entry := range m.Render {
		fromOverride, err := aipkg.RenderTemplateFile(absRoot, packName, entry.From, data, entry.To, absHubDir, absRoot)
		if err != nil {
			return err
		}
		if fromOverride {
			w.Info(fmt.Sprintf("using local override: devbox/templates/ai/%s.local/%s", packName, entry.From))
		}
		w.Success(fmt.Sprintf("ai → %s", filepath.Join(svc.Dir, entry.To)))
	}

	// Create each symlink in the manifest
	for _, entry := range m.Symlinks {
		if err := aipkg.EnsureRelativeSymlink(entry.Link, entry.To, absHubDir, absRoot); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ai → %s ⇒ %s", filepath.Join(svc.Dir, entry.Link), entry.To))
	}

	return nil
}

// resolveAIHubAnchor treats name as a hub anchor and returns the AI-docs
// collision winner among services that share name's Dir. Applies the same
// gating used by aipkg.SelectServices (Enabled + AIRenderEnabled), then
// picks the shallowest extends chain (ties broken lexicographically) — the
// canonical hub owner. Returns name unchanged when there are no qualifying
// siblings. The caller must have already validated name via validateExplicitAIArg.
func resolveAIHubAnchor(name string, services map[string]config.ServiceConfig) string {
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
		if !s.AIRenderEnabled() {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) <= 1 {
		return name
	}

	sort.Strings(candidates) // tie-break: lexicographically first among shallowest
	var shallowest string
	minDepth := -1
	for _, c := range candidates {
		d, _ := aipkg.ExtendsDepth(services, c)
		if minDepth == -1 || d < minDepth {
			minDepth = d
			shallowest = c
		}
	}
	return shallowest
}

// validateExplicitAIArg validates the explicit service argument for `devbox render ai <service>`.
// Checks in priority order: not-found → disabled → no-dir → AI docs policy.
// Returns nil when the service is valid and renderable.
func validateExplicitAIArg(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render agents docs", name)
	}
	if !svc.AIRenderEnabled() {
		return fmt.Errorf("service %q has render.ai.enabled: false", name)
	}
	return nil
}

// newRenderAICmd creates the `devbox render ai [service]` command.
// It generates hub-level agentic docs into each service directory.
// When a service name is provided only that service is processed;
// otherwise all services matching the agents docs selection policy are processed.
func newRenderAICmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ai [service]",
		Short: "Generate hub-level agents docs from template packs",
		Long: `Generate agents documentation files (such as AGENTS.md, CLAUDE.md) for the service hub.

The command reads manifest.yml from the chosen template pack
(devbox/templates/ai/<pack-name>/) and processes only the entries it declares:
each render entry is rendered to its destination and each symlink entry is
created. Files inside the pack that are not referenced by the manifest are
ignored.

Template pack resolution (explicit is strict; implicit chain: service-name → default):
  1. If render.ai.template is set in the service config, use that pack (explicit, strict)
  2. Otherwise, try devbox/templates/ai/<service-name>/
  3. If not found, use devbox/templates/ai/default/
  4. If none exist, skip with a warning (implicit missing pack)

Services that participate in agents docs rendering:
  - All service types have render.ai.enabled: true by default
  - Set render.ai.enabled: false to opt out

When a service name is given, it is treated as a hub anchor: if multiple
services share its dir (e.g. main and main-debug both point to services/main),
the agent-docs collision-policy winner (shallowest extends — the canonical
hub owner) is rendered. This means 'render ai main-debug' still renders the
parent 'main' identity for the shared hub.`,
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
				// the AI-docs collision-policy winner (shallowest extends, i.e.
				// canonical hub owner) renders.
				name := args[0]
				if err := validateExplicitAIArg(name, cfg.Services); err != nil {
					return err
				}

				winner := resolveAIHubAnchor(name, cfg.Services)
				if winner != name {
					w.Info(fmt.Sprintf("ai [%s] — resolved to %s (hub %s)", name, winner, filepath.Clean(cfg.Services[name].Dir)))
				}
				serviceNames = []string{winner}
			} else {
				// No explicit service: use selection policy
				selected, skipped := aipkg.SelectServices(cfg.Services)
				serviceNames = selected

				// Emit warnings only for actionable skips; policy-based skips
				// (service-disabled, ai-disabled) are expected and not reported.
				for _, skip := range skipped {
					switch skip.Reason {
					case "empty-dir":
						w.Warning(fmt.Sprintf("ai [%s] — skipped (service has no dir or dir is project root)", skip.Name))
					case "lost-collision":
						w.Warning(fmt.Sprintf("ai [%s] — skipped (dir %s rendered by %s)", skip.Name, skip.Dir, skip.Winner))
					}
				}

				if len(serviceNames) == 0 {
					w.Info("no services match the ai-docs rendering policy")
					return nil
				}
			}

			for _, name := range serviceNames {
				svc := cfg.Services[name]
				if err := renderAgentsForService(projectRoot, name, svc, cfg, w); err != nil {
					return fmt.Errorf("service %s: %w", name, err)
				}
			}
			return nil
		},
	}
}
