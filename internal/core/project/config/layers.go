package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Layer is one source file in the merged 3-layer project config
// (workspace.yml → workspace/defaults.yml → workspace/local.yml), lowest
// precedence first. The same loader feeds both LoadConfig's merge and the
// per-layer var inspection used by `dwe vars inspect`, so the two cannot drift
// on which files are read, optional-layer handling, or error wording.
type Layer struct {
	Path string
	Data map[string]any
}

// LoadLayers reads the project config layers in precedence order (lowest
// first): workspace.yml (required) then the optional workspace/defaults.yml and
// workspace/local.yml. Absent optional layers are skipped; a present-but-empty
// file yields a nil Data map. The returned slice always begins with the
// workspace.yml layer. Error wording matches LoadConfig's historical reads so
// the two stay byte-identical.
func LoadLayers(workspacePath string) ([]Layer, error) {
	baseDir := filepath.Dir(workspacePath)
	var layers []Layer

	// Layer 1: workspace.yml (required)
	base, err := loadRawYAML(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", workspacePath, err)
	}
	layers = append(layers, Layer{Path: workspacePath, Data: base})

	// Layer 2: workspace/defaults.yml (optional)
	defaultsPath := filepath.Join(baseDir, "workspace", "defaults.yml")
	if defaults, err := loadRawYAML(defaultsPath); err == nil {
		layers = append(layers, Layer{Path: defaultsPath, Data: defaults})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", defaultsPath, err)
	}

	// Layer 3: workspace/local.yml (optional)
	localPath := filepath.Join(baseDir, "workspace", "local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
		layers = append(layers, Layer{Path: localPath, Data: local})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	return layers, nil
}

// LocalLayerPath returns the conventional workspace/local.yml path for a given
// workspace.yml path. Used to identify which Layer is the local override.
func LocalLayerPath(workspacePath string) string {
	return filepath.Join(filepath.Dir(workspacePath), "workspace", "local.yml")
}

// LayeredValue describes a dot-path resolved at each config layer plus the file
// that supplies the effective value. Author is the merge of all non-local
// layers (workspace.yml + defaults.yml); Local is workspace/local.yml alone;
// Effective is the full 3-layer merge — what ${...} / ResolvePath see at
// runtime. The *OK fields report whether the path was present at that layer.
//
// An explicit null in local.yml is present-but-nil (LocalOK true, Local nil)
// and, per deepMerge's nil-skip, does NOT win the effective value — so Origin
// stays on the lower layer it failed to override.
type LayeredValue struct {
	Author      any
	AuthorOK    bool
	Local       any
	LocalOK     bool
	Effective   any
	EffectiveOK bool
	// Origin is the path of the highest-precedence layer whose value at the
	// resolved path is non-nil, or "" when the path is unresolved everywhere.
	Origin string
}

// ResolveLayeredPath resolves a dot-path across the three config layers,
// reporting the value at each layer and the source file that supplies the
// effective value. It reuses LoadLayers (so it cannot drift from
// LoadConfig's layer set), deepMerge (the runtime merge semantics, including
// nil-skip), and ResolvePath.
func ResolveLayeredPath(workspacePath, path string) (LayeredValue, error) {
	layers, err := LoadLayers(workspacePath)
	if err != nil {
		return LayeredValue{}, err
	}
	return resolveLayeredPath(layers, LocalLayerPath(workspacePath), path), nil
}

func resolveLayeredPath(layers []Layer, localPath, path string) LayeredValue {
	author := make(map[string]any)
	effective := make(map[string]any)
	var local map[string]any
	for _, l := range layers {
		// Deep-copy before merging: deepMerge shares nested-map references for
		// absent keys, so building author and effective from the same layers
		// would otherwise cross-contaminate (and mutate l.Data, which the Origin
		// scan below reads). Each merged view gets its own copy.
		effective = deepMergeCopy(effective, l.Data)
		if l.Path == localPath {
			local = l.Data
		} else {
			author = deepMergeCopy(author, l.Data)
		}
	}

	var lv LayeredValue
	lv.Author, lv.AuthorOK = ResolvePath(author, path)
	if local != nil {
		lv.Local, lv.LocalOK = ResolvePath(local, path)
	}
	lv.Effective, lv.EffectiveOK = ResolvePath(effective, path)

	// Origin: the highest-precedence layer (local last) whose value at path is
	// non-nil. The non-nil guard mirrors deepMerge's nil-skip so an explicit
	// null in local.yml does not claim origin over the layer it failed to
	// override.
	for _, l := range layers {
		if v, ok := ResolvePath(l.Data, path); ok && v != nil {
			lv.Origin = l.Path
		}
	}
	return lv
}

// deepMergeCopy deep-merges a deep copy of src into dst (mutating and returning
// dst) without sharing any nested references with src.
func deepMergeCopy(dst, src map[string]any) map[string]any {
	cp, _ := deepCopyValue(src).(map[string]any)
	deepMerge(dst, cp)
	return dst
}

// deepCopyValue returns a structural deep copy of a yaml-decoded value (maps and
// sequences cloned recursively; scalars returned as-is since they are
// immutable). A typed-nil map yields an empty map.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopyValue(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopyValue(val)
		}
		return s
	default:
		return v
	}
}
