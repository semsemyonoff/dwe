// Package env groups the environment-related predicate builtins:
// env_keys_present and executable_in_path.
package env

import "github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"

// Builtins returns the env builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"env_keys_present":   {Impl: KeysPresent{}, Kind: spec.KindPredicate},
		"executable_in_path": {Impl: ExecutableInPath{}, Kind: spec.KindPredicate},
	}
}
