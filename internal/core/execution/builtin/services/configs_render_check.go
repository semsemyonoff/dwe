package services

import (
	"context"
	"fmt"
	"strings"

	configpack "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

// ConfigsRenderCheck implements the service_configs_render builtin's companion
// check: it verifies the declared config-pack render targets exist on disk.
//
// Its primary purpose is structural: pairing it on the render step via check:
// forces that step to re-run on every deploy (the hasCheck → Run lever in
// journal/decision.go), so template edits and store clears always take effect —
// exactly mirroring service_configs_copy + service_configs_check. A service with
// no resolvable config pack has nothing to check and is a no-op.
type ConfigsRenderCheck struct{}

// Validate checks the with-params for service_configs_render_check.
func (ConfigsRenderCheck) Validate(with map[string]any) error {
	service := spec.GetStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_configs_render_check: missing required param 'service'")
	}
	return nil
}

// Describe returns a human-readable plan line for service_configs_render_check.
func (ConfigsRenderCheck) Describe(with map[string]any) string {
	service := spec.GetStringParam(with, "service", "")
	return fmt.Sprintf("builtin: service_configs_render_check(service=%s)", service)
}

// Run verifies that every config-pack render target exists; missing files error.
func (ConfigsRenderCheck) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	serviceName := spec.GetStringParam(with, "service", "")

	missing, found, err := configpack.CheckRendered(ectx.ProjectRoot, ectx.Config, serviceName)
	if err != nil {
		return fmt.Errorf("service_configs_render_check: %w", err)
	}
	if !found {
		return nil
	}
	if len(missing) > 0 {
		if ectx.Output != nil {
			for _, f := range missing {
				ectx.Output.Error("missing rendered config: " + f)
			}
		}
		return fmt.Errorf("missing rendered config files: %s", strings.Join(missing, ", "))
	}
	return nil
}
