// Package fs groups the filesystem builtin implementations:
// file_exists and remove_paths.
package fs

import "github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

// Builtins returns the fs builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"file_exists":  {Impl: FileExists{}, Kind: spec.KindPredicate, Summary: "report whether a path exists (relative paths resolve against the project root)"},
		"remove_paths": {Impl: RemovePaths{}, Kind: spec.KindAction, Summary: "delete the declared paths inside the project root"},
	}
}
