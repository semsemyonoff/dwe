package snapshot

import "time"

// BuildSnapshotVars assembles the map exposed as ${snapshot.*} during workflow
// rendering. The map always contains every key; scope-gating happens in the
// tpl package via SnapshotScope, not by omitting keys here.
func BuildSnapshotVars(name, path, description, variant string, createdAt time.Time) map[string]any {
	return map[string]any{
		"name":        name,
		"path":        path,
		"description": description,
		"variant":     variant,
		"created_at":  createdAt.UTC().Format(time.RFC3339),
	}
}
