package command

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	gitpkg "devbox-cli/internal/templates/git"

	"github.com/spf13/cobra"
)

// renderGitHooksForService renders all hooks for a single service.
func renderGitHooksForService(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	absHub, err := gitpkg.PrepareHub(absRoot, name, svc)
	if err != nil {
		return err
	}

	// Check for src/.git before resolving the pack so that a service with no
	// src/.git directory produces the documented "warn + skip" behavior even
	// when no template pack is configured.
	absHooks, status, err := gitpkg.ResolveGitHooksDir(absHub)
	if err != nil {
		return err
	}
	switch status {
	case gitpkg.DirMissing:
		w.Warning(fmt.Sprintf("git [%s] — skipped (no src/.git directory)", name))
		return nil
	case gitpkg.DirWorktree:
		w.Warning(fmt.Sprintf("git [%s] — skipped (src/.git is a worktree pointer; not yet supported)", name))
		return nil
	}

	packDir, packName, err := gitpkg.ResolveTemplatePack(svc, absRoot, name)
	if err != nil {
		return err
	}

	m, err := gitpkg.LoadManifest(packDir)
	if err != nil {
		return err
	}
	if err := gitpkg.ValidateManifest(m, absRoot, packName, absHooks); err != nil {
		return fmt.Errorf("invalid git manifest: %w", err)
	}

	return gitpkg.RenderHooks(gitpkg.Context{
		ProjectRoot: absRoot,
		Cfg:         cfg,
		Service:     name,
		ServiceCfg:  svc,
		PackName:    packName,
		Manifest:    m,
		HooksDir:    absHooks,
		HubDir:      absHub,
		Writer:      w,
	})
}

// resolveGitHubAnchor returns the git collision winner among services sharing
// the same Dir as name. Gating mirrors gitpkg.SelectServices (Enabled +
// GitRenderEnabled), and tie-break is deepest-extends-wins (matches IDE).
func resolveGitHubAnchor(name string, services map[string]config.ServiceConfig) string {
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
		if !s.GitRenderEnabled() {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) <= 1 {
		return name
	}

	sort.Strings(candidates)
	var deepest string
	maxDepth := -1
	for _, c := range candidates {
		d, _ := gitpkg.ExtendsDepth(services, c)
		if d > maxDepth {
			maxDepth = d
			deepest = c
		}
	}
	return deepest
}

// validateExplicitGitArg validates the explicit service argument for
// `devbox render git <service>`. Checks: not-found → disabled → no-dir → git policy.
func validateExplicitGitArg(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render git hooks", name)
	}
	enabled, explicit := svc.GitRenderEnabledExplicit()
	if !enabled {
		if explicit {
			return fmt.Errorf("service %q has git.enabled: false", name)
		}
		return fmt.Errorf("service %q (type: %s) does not participate in git-hook rendering by default; set git.enabled: true to opt in", name, svc.Type)
	}
	return nil
}

// newRenderGitCmd creates the `devbox render git [service]` command.
func newRenderGitCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "git [service]",
		Short: "Generate git hooks from template packs",
		Long: `Generate shell git hooks for each enabled service from a template pack.

The command reads manifest.yml from the chosen template pack
(devbox/templates/git/<pack-name>/) and renders each declared entry into
<svc.Dir>/src/.git/hooks/<basename>, with executable mode (0755).

Services whose src/.git is missing or is a file (worktree/submodule pointer)
are skipped with a warning.

Template pack resolution (explicit is strict; implicit chain: service-name → default):
  1. If git.template is set in the service config, use that pack (explicit, strict)
  2. Otherwise, try devbox/templates/git/<service-name>/
  3. If not found, use devbox/templates/git/default/
  4. If none exist, return an error

Services that participate in git-hook rendering:
  - Type 'app' (default) has git.enabled: true by default
  - Other types require explicit git.enabled: true in the config

When a service name is given, it is treated as a hub anchor: if multiple
services share its dir, the git collision-policy winner (deepest extends)
is rendered.`,
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

			var serviceNames []string
			if len(args) == 1 {
				name := args[0]
				if err := validateExplicitGitArg(name, cfg.Services); err != nil {
					return err
				}

				winner := resolveGitHubAnchor(name, cfg.Services)
				if winner != name {
					w.Info(fmt.Sprintf("git [%s] — resolved to %s (hub %s)", name, winner, filepath.Clean(cfg.Services[name].Dir)))
				}
				serviceNames = []string{winner}
			} else {
				selected, skipped := gitpkg.SelectServices(cfg.Services)
				serviceNames = selected

				for _, skip := range skipped {
					switch skip.Reason {
					case "empty-dir":
						w.Warning(fmt.Sprintf("git [%s] — skipped (service has no dir or dir is project root)", skip.Name))
					case "lost-collision":
						w.Warning(fmt.Sprintf("git [%s] — skipped (dir %s rendered by %s)", skip.Name, skip.Dir, skip.Winner))
					}
				}

				if len(serviceNames) == 0 {
					w.Info("no services match the git-hook rendering policy")
					return nil
				}
			}

			for _, name := range serviceNames {
				svc := cfg.Services[name]
				if err := renderGitHooksForService(projectRoot, name, svc, cfg, w); err != nil {
					return fmt.Errorf("service %s: %w", name, err)
				}
			}
			return nil
		},
	}
}
