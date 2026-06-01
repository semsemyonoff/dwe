// Package spec implements require-spec resolution and validation for files_gate directives.
package spec

import (
	"fmt"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
)

// ResolveRequireIDs expands a require spec into a sorted list of file IDs.
// The only place that expands required/all/<id>/[<ids>] into concrete IDs against
// the target command's files: map.
//
// Behavior:
// - RequireRequired: IDs where (access == read && required) || access == read_write
// - RequireAll: IDs where access is read or read_write
// - RequireList: exact list after validation that all IDs exist and access ∈ {read, read_write}
// - Empty result for RequireRequired/RequireAll: returns error
func ResolveRequireIDs(require filesgate.RequireSpec, defFiles map[string]model.FileSpec) ([]string, error) {
	if require == nil {
		require = filesgate.RequireRequired{}
	}

	var ids []string

	switch spec := require.(type) {
	case filesgate.RequireRequired:
		ids = resolveRequired(defFiles)
	case filesgate.RequireAll:
		ids = resolveAll(defFiles)
	case filesgate.RequireList:
		var err error
		ids, err = resolveList(spec.IDs, defFiles)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown require spec type: %T", require)
	}

	if len(ids) == 0 {
		specName := "required"
		if _, ok := require.(filesgate.RequireAll); ok {
			specName = "all"
		}
		return nil, fmt.Errorf("files_gate: require: %s selected no files (command has no required reads or no reads at all)", specName)
	}

	// Sort for deterministic output.
	sort.Strings(ids)
	return ids, nil
}

func resolveRequired(defFiles map[string]model.FileSpec) []string {
	var ids []string
	for id, spec := range defFiles {
		// Include if:
		// 1. access == read && required == true
		// 2. access == read_write (implicitly existence-required)
		if (spec.Access == model.FileAccessRead && spec.Required) ||
			spec.Access == model.FileAccessReadWrite {
			ids = append(ids, id)
		}
	}
	return ids
}

func resolveAll(defFiles map[string]model.FileSpec) []string {
	var ids []string
	for id, spec := range defFiles {
		// Include if access is read or read_write.
		if spec.Access == model.FileAccessRead || spec.Access == model.FileAccessReadWrite {
			ids = append(ids, id)
		}
	}
	return ids
}

func resolveList(list []string, defFiles map[string]model.FileSpec) ([]string, error) {
	for _, id := range list {
		spec, exists := defFiles[id]
		if !exists {
			return nil, fmt.Errorf("files_gate: require: file-id %q does not exist in command's files", id)
		}
		// Reject write-only files.
		if spec.Access == model.FileAccessWrite {
			return nil, fmt.Errorf("files_gate: require: file-id %q has access=write (write-only files cannot be probed)", id)
		}
	}
	// Return a copy so sort.Strings in the caller does not mutate the original RequireList.IDs.
	result := make([]string, len(list))
	copy(result, list)
	return result, nil
}
