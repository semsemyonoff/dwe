package config

import (
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// registryFrom extracts the concrete *registry.Registry from the validation
// context, returning nil when no registry is available (assertion miss or a
// typed-nil pointer). Registry-dependent validators tolerate a nil registry and
// skip the dependent checks.
func registryFrom(ctx validate.Context) *registry.Registry {
	reg, _ := ctx.CommandRegistry.(*registry.Registry)
	return reg
}

// resolveServices returns the services map from ctx.Cfg when present, otherwise
// loads it from disk. ok is false only when the map could not be resolved (load
// error), signalling the caller to skip silently — other validators surface the
// underlying load error.
func resolveServices(ctx validate.Context) (services map[string]config.ServiceConfig, ok bool) {
	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		return ctx.Cfg.Services, true
	}
	loaded, err := config.LoadServices(ctx.ProjectRoot)
	if err != nil {
		return nil, false
	}
	return loaded, true
}
