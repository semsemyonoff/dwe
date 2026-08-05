// Package source groups builtins that materialise external source code into
// the project tree: currently source_clone.
package source

import "github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

// Builtins returns the source builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"source_clone": {Impl: Clone{}, Kind: spec.KindAction},
	}
}
