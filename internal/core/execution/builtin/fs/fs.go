// Package fs groups the filesystem builtin implementations:
// file_exists and remove_paths.
package fs

import "devbox-cli/internal/core/execution/builtin/spec"

// Builtins returns the fs builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"file_exists":  {Impl: FileExists{}, Kind: spec.KindPredicate},
		"remove_paths": {Impl: RemovePaths{}, Kind: spec.KindAction},
	}
}
