// Package services groups the service_* builtin implementations:
// service_configs_copy, service_configs_check, and service_dirs_ensure.
package services

import "github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

// Builtins returns the services builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"service_configs_copy":  {Impl: ConfigsCopy{}, Kind: spec.KindAction},
		"service_configs_check": {Impl: ConfigsCheck{}, Kind: spec.KindAction},
		"service_dirs_ensure":   {Impl: DirsEnsure{}, Kind: spec.KindAction},
	}
}
