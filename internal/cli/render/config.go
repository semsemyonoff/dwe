package render

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	configpack "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// newConfigCmd creates the `dwe render config [service]` command.
//
// It renders each service's config files from a config template pack into the
// service hub dir (svc.Dir), replaying any harvested ${generated.<name>} values
// from the generated-value store (.dwe/generated.yml). When a service name is
// given only that service is processed; otherwise every enabled service that
// resolves a config pack is processed in DeployOrder.
//
// With --harvest the command performs a harvest-only pass: it reads each
// declared generated: field's on-disk file, extracts the value via the field's
// regex, and write-if-absent stores it — NO render runs. This bootstraps an
// existing project's already-committed secrets into the store.
//
// The command is read-only with respect to project locks: it runs no preflight
// and acquires no locks, matching the ide/ai/git renderers.
func newConfigCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var harvest bool

	cmd := &cobra.Command{
		Use:   "config [service]",
		Short: "Render service config files from template packs",
		Long: `Render service config files from a config template pack into each service directory.

The command reads manifest.yml from the chosen template pack
(workspace/templates/config/<pack-name>/) and renders each declared entry into
the corresponding location within the service hub dir. Authors target the app
tree by writing 'to: src/...':
  manifest.yml: render: [{from: env.tmpl, to: src/.env}]
  → services/main/src/.env

Unlike ide/ai/git, config templates use the ${...} shorthand (e.g.
${services.main.ports.http}, ${databases.magento}) and the ${generated.<name>}
namespace, which replays service-minted secrets harvested into the
generated-value store (.dwe/generated.yml).

Template pack resolution (explicit is strict; implicit chain walks the extends chain):
  1. If render.config.template is set in the service config, use that pack (explicit, strict)
  2. Otherwise, try workspace/templates/config/<service-name>/
  3. Then walk the service's extends chain — try workspace/templates/config/<ancestor>/ for each ancestor
  4. Then use workspace/templates/config/default/
  5. If none exist, skip (config rendering is opt-in)

With --harvest the command does NOT render. Instead it reads each declared
generated: field's output file, extracts the value, and write-if-absent stores
it into .dwe/generated.yml — for bootstrapping an existing project's
already-committed values before they stop being committed.`,
		Example: `  dwe render config
  dwe render config main
  dwe render config main --harvest`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: cmdctx.ServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
			if err != nil {
				return err
			}

			projectRoot := flags.ProjectRoot()
			w := render.Stdout()

			storePath := filepath.Join(projectRoot, generatedstore.DefaultRelPath)
			store, err := generatedstore.Load(storePath)
			if err != nil {
				return fmt.Errorf("load generated store: %w", err)
			}

			// Determine which services to process. An explicit argument is
			// validated thoroughly; no argument means every enabled service in
			// DeployOrder (deterministic), each skipped silently when it has no
			// config pack / no generated fields.
			explicit := len(args) == 1
			var serviceNames []string
			if explicit {
				name := args[0]
				if err := validateExplicitConfigArg(name, cfg.Services); err != nil {
					return err
				}
				serviceNames = []string{name}
			} else {
				serviceNames = config.DeployOrder(cfg, []string{"app", "tool", "infra"})
			}

			if harvest {
				return harvestConfigs(projectRoot, cfg, serviceNames, store, w, explicit)
			}
			return renderConfigs(projectRoot, cfg, serviceNames, store, w, explicit)
		},
	}

	cmd.Flags().BoolVar(&harvest, "harvest", false,
		"harvest declared generated values from on-disk files into the store (no render)")
	return cmd
}

// renderConfigs renders config packs for the selected services, reporting each
// written file. Services without a config pack are skipped: for an explicit
// argument the skip is surfaced as a warning; for the all-services pass it is
// silent (config rendering is opt-in). An empty all-services pass emits an info
// line so the user is not left with no output.
func renderConfigs(projectRoot string, cfg *config.DweConfig, names []string, store *generatedstore.Store, w *render.Writer, explicit bool) error {
	rendered := 0
	for _, name := range names {
		res, err := configpack.RenderConfigs(projectRoot, cfg, name, store)
		if err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if !res.Found {
			if explicit {
				warnNoPack(w, "config", cfg.Services, name)
			}
			continue
		}
		for _, f := range res.Rendered {
			if f.FromOverride {
				w.Info(fmt.Sprintf("using local override: workspace/templates/config/%s.local/%s", res.Pack, f.To))
			}
			w.Success(fmt.Sprintf("config → %s", f.Rel))
			rendered++
		}
	}
	if rendered == 0 && !explicit {
		w.Info("no services resolve a config template pack")
	}
	return nil
}

// harvestConfigs runs a harvest-only pass over the selected services, reporting
// each declared generated field. Services that declare no generated: fields are
// skipped silently in the all-services pass; an explicit argument with no fields
// gets an info line. An empty all-services pass emits an info line.
func harvestConfigs(projectRoot string, cfg *config.DweConfig, names []string, store *generatedstore.Store, w *render.Writer, explicit bool) error {
	harvested := 0
	for _, name := range names {
		res, err := configpack.HarvestGenerated(projectRoot, cfg, name, store)
		if err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if len(res.Fields) == 0 {
			if explicit {
				w.Info(fmt.Sprintf("config harvest [%s] — no generated fields declared", name))
			}
			continue
		}
		for _, f := range res.Fields {
			if f.Wrote {
				w.Success(fmt.Sprintf("config harvest → %s.%s stored", name, f.Field))
			} else {
				w.Info(fmt.Sprintf("config harvest → %s.%s already present (kept)", name, f.Field))
			}
			harvested++
		}
	}
	if harvested == 0 && !explicit {
		w.Info("no services declare generated fields to harvest")
	}
	return nil
}

// validateExplicitConfigArg validates the explicit service argument for
// `dwe render config <service>`. Config rendering has no per-kind enabled
// policy (it is gated solely by whether a pack resolves), so the checks are
// not-found → disabled → no-dir.
func validateExplicitConfigArg(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return fmt.Errorf("service %q has no dir; cannot render config files", name)
	}
	return nil
}
