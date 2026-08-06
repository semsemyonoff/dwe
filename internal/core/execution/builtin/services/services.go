// Package services groups the service_* builtin implementations: the deprecated
// copy mechanism (service_configs_copy, service_configs_check), the render
// mechanism (service_configs_render, service_configs_render_check,
// service_generated_harvest), and service_dirs_ensure.
package services

import "github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

// Builtins returns the services builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"service_configs_copy":         {Impl: ConfigsCopy{}, Kind: spec.KindAction, Summary: "copy service config templates into the hub (legacy; deprecation-warned)"},
		"service_configs_check":        {Impl: ConfigsCheck{}, Kind: spec.KindAction, Summary: "verify the service's config files exist in the hub"},
		"service_configs_render":       {Impl: ConfigsRender{}, Kind: spec.KindAction, Summary: "render service config templates into the hub (modern render-based mechanism)"},
		"service_configs_render_check": {Impl: ConfigsRenderCheck{}, Kind: spec.KindAction, Summary: "re-render gate; pair as the render step's check: to force re-run on template or store change"},
		"service_generated_harvest":    {Impl: GeneratedHarvest{}, Kind: spec.KindAction, Summary: "read generated values (e.g. a minted app key) back into the generated store"},
		"service_dirs_ensure":          {Impl: DirsEnsure{}, Kind: spec.KindAction, Summary: "create the service hub directories"},
	}
}
