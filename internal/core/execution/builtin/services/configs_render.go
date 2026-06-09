package services

import (
	"context"
	"fmt"
	"path/filepath"

	configpack "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
)

// ConfigsRender implements the service_configs_render builtin: render a service's
// config files from a config template pack into its hub dir (svc.Dir), replaying
// any harvested ${generated.<name>} values from the generated-value store.
//
// Rendering is mode replace (overwrite). Config rendering is opt-in: a service
// with no resolvable config pack is a no-op. Pair this step with a
// service_configs_render_check check: so it re-runs every deploy (the
// hasCheck → Run lever), picking up template edits and store clears.
type ConfigsRender struct{}

// Validate checks the with-params for service_configs_render.
func (ConfigsRender) Validate(with map[string]any) error {
	service := spec.GetStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_configs_render: missing required param 'service'")
	}
	mode := spec.GetStringParam(with, "mode", "replace")
	if mode != "replace" {
		return fmt.Errorf("builtin service_configs_render: unsupported mode %q (only 'replace' is supported)", mode)
	}
	return nil
}

// Describe returns a human-readable plan line for service_configs_render.
func (ConfigsRender) Describe(with map[string]any) string {
	service := spec.GetStringParam(with, "service", "")
	mode := spec.GetStringParam(with, "mode", "replace")
	return fmt.Sprintf("builtin: service_configs_render(service=%s, mode=%s)", service, mode)
}

// Run renders the service's config pack, replaying stored generated values.
func (ConfigsRender) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	serviceName := spec.GetStringParam(with, "service", "")

	storePath := filepath.Join(ectx.ProjectRoot, generatedstore.DefaultRelPath)
	store, err := generatedstore.Load(storePath)
	if err != nil {
		return fmt.Errorf("service_configs_render: load generated store: %w", err)
	}

	res, err := configpack.RenderConfigs(ectx.ProjectRoot, ectx.Config, serviceName, store)
	if err != nil {
		return fmt.Errorf("service_configs_render: %w", err)
	}
	if !res.Found {
		if ectx.Output != nil {
			ectx.Output.Info(fmt.Sprintf("service_configs_render: service %q has no config pack [skipped]", serviceName))
		}
		return nil
	}
	if ectx.Output != nil {
		for _, f := range res.Rendered {
			ectx.Output.Success(fmt.Sprintf("config → %s [replace]", f.Rel))
		}
	}
	return nil
}
