package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/shared/secrets"
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

// LoadLayers reads the project config layers and decrypts every ENC[age:…]
// marker it can. It is the historical entry point and keeps its signature:
// callers that do not care where a secret came from (ResolveLayeredPath, the
// `dwe vars` browsers) keep using it and go through exactly the same decrypt
// pass as LoadConfig, so the two cannot drift.
//
// Use LoadRawLayers when the ciphertext as written is what you need (the
// `dwe secrets` CLI), and LoadLayersWithSecrets when you also need to know
// which markers resolved and which did not.
func LoadLayers(workspacePath string) ([]Layer, error) {
	layers, _, err := LoadLayersWithSecrets(workspacePath)
	return layers, err
}

// LoadRawLayers reads the project config layers in precedence order (lowest
// first): workspace.yml (required) then the optional workspace/defaults.yml and
// workspace/local.yml. Absent optional layers are skipped; a present-but-empty
// file yields an empty (non-nil) Data map. The returned slice always begins
// with the workspace.yml layer. Error wording matches LoadConfig's historical
// reads so the two stay byte-identical.
//
// Encrypted scalars are returned as the ENC[age:…] markers written on disk —
// no decryption happens here. This is what the `dwe secrets` commands read.
func LoadRawLayers(workspacePath string) ([]Layer, error) {
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

// LoadLayersWithSecrets reads the raw layers, validates their roots and
// decrypts every ENC[age:…] marker on a deep copy, reporting what resolved and
// what did not.
//
// The recipient comes from the FIRST layer only (workspace.yml), which
// ValidateLayerRoots has just pinned as the single legal home of the secrets:
// block — so the recipient the decrypt pass uses and the one
// cfg.SecretsState.Recipient reports are the same value by construction.
//
// A project without secrets never touches the filesystem for an identity: with
// no markers present the identity lookup is skipped entirely.
func LoadLayersWithSecrets(workspacePath string) ([]Layer, SecretsState, error) {
	raw, err := LoadRawLayers(workspacePath)
	if err != nil {
		return nil, SecretsState{}, err
	}
	if err := ValidateLayerRoots(raw); err != nil {
		return nil, SecretsState{}, err
	}

	state := SecretsState{Recipient: recipientFromLayers(raw)}

	// Deep-copy before decrypting: the caller's raw view must stay ciphertext
	// (LoadRawLayers is the documented way to read it back), and nothing may
	// mutate a map another loader still holds.
	layers := make([]Layer, len(raw))
	for i, l := range raw {
		data, _ := deepCopyValue(l.Data).(map[string]any)
		if data == nil {
			data = make(map[string]any)
		}
		layers[i] = Layer{Path: l.Path, Data: data}
	}

	if !layersHaveMarker(layers) {
		return layers, state, nil
	}

	// Load the identity once for the whole pass. A failure is not fatal: every
	// marker is recorded as unresolved with the reason, the project still
	// loads, and the secrets.unresolved validator blocks the commands that
	// would act on a missing value.
	var (
		id     secrets.Identity
		idErr  error
		source secrets.Source
	)
	id, source, idErr = secrets.LoadIdentity(state.Recipient)
	state.IdentitySource = string(source)

	for _, layer := range layers {
		decryptLayer(layer, id, idErr, &state)
	}
	return layers, state, nil
}

// recipientFromLayers reads secrets.recipient from the workspace.yml layer.
// ValidateLayerRoots has already rejected the block anywhere else and rejected
// a malformed value, so a non-empty result here always parses.
func recipientFromLayers(layers []Layer) string {
	if len(layers) == 0 {
		return ""
	}
	block, ok := layers[0].Data["secrets"].(map[string]any)
	if !ok {
		return ""
	}
	recipient, _ := block["recipient"].(string)
	return strings.TrimSpace(recipient)
}

func layersHaveMarker(layers []Layer) bool {
	found := false
	for _, layer := range layers {
		walkScalars(layer.Data, "", func(_ string, s string) (string, bool) {
			if secrets.IsMarker(s) {
				found = true
			}
			return s, false
		})
		if found {
			return true
		}
	}
	return false
}

// decryptLayer replaces every marker in one layer's data in place, recording
// each path in state. idErr is the (possibly nil) identity-load failure: when
// it is non-nil every marker is unresolved with the mapped reason, and the
// filesystem is not touched again.
func decryptLayer(layer Layer, id secrets.Identity, idErr error, state *SecretsState) {
	walkScalars(layer.Data, "", func(path, s string) (string, bool) {
		if !secrets.IsMarker(s) {
			return s, false
		}
		ref := SecretRef{Layer: layer.Path, Path: path}
		if idErr != nil {
			state.Unresolved = append(state.Unresolved, UnresolvedSecret{SecretRef: ref, Reason: unresolvedReason(idErr)})
			return s, false
		}
		plain, err := secrets.Decrypt(s, id)
		if err != nil {
			state.Unresolved = append(state.Unresolved, UnresolvedSecret{SecretRef: ref, Reason: unresolvedReason(err)})
			return s, false
		}
		state.Decrypted = append(state.Decrypted, ref)
		return plain, true
	})
}

// collectDecryptedValues re-walks the decrypted layers and returns the
// plaintext at every path SecretsState recorded as decrypted. It exists so the
// plaintexts never have to travel on SecretsState itself (which is handed to
// JSON output and to renderers), and it uses the same walk as the decrypt pass
// so sequence elements (`vars.tokens.0`) resolve too — ResolvePath does not
// index sequences.
func collectDecryptedValues(layers []Layer, state SecretsState) []string {
	if len(state.Decrypted) == 0 {
		return nil
	}
	want := make(map[SecretRef]struct{}, len(state.Decrypted))
	for _, ref := range state.Decrypted {
		want[ref] = struct{}{}
	}
	var values []string
	for _, layer := range layers {
		walkScalars(layer.Data, "", func(path, s string) (string, bool) {
			if _, ok := want[SecretRef{Layer: layer.Path, Path: path}]; ok && s != "" {
				values = append(values, s)
			}
			return s, false
		})
	}
	return values
}

// unresolvedReason maps a secrets sentinel error onto the stable reason string
// reported by SecretsState, `dwe secrets status` and the validators.
func unresolvedReason(err error) string {
	switch {
	case errors.Is(err, secrets.ErrNoIdentity):
		return ReasonNoIdentity
	case errors.Is(err, secrets.ErrWrongIdentity):
		return ReasonWrongIdentity
	default:
		return ReasonCorrupt
	}
}

// walkScalars visits every string scalar reachable from v, depth-first, with
// map keys sorted so the emitted paths (and therefore the SecretsState order
// and every golden built from it) are deterministic. fn returns the
// replacement value and whether to apply it; a sequence element's path carries
// its index (`vars.tokens.0`).
func walkScalars(v any, path string, fn func(path, s string) (string, bool)) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			child := k
			if path != "" {
				child = path + "." + k
			}
			if s, ok := t[k].(string); ok {
				if replacement, replace := fn(child, s); replace {
					t[k] = replacement
				}
				continue
			}
			walkScalars(t[k], child, fn)
		}
	case []any:
		for i, item := range t {
			child := path + "." + strconv.Itoa(i)
			if path == "" {
				child = strconv.Itoa(i)
			}
			if s, ok := item.(string); ok {
				if replacement, replace := fn(child, s); replace {
					t[i] = replacement
				}
				continue
			}
			walkScalars(item, child, fn)
		}
	}
}

// ValidateLayerRoots is the exported form of validateLayerRoots, for callers
// that stage a modified layer set before persisting it (`dwe secrets set`) and
// need the same acceptance rules the runtime loader applies.
func ValidateLayerRoots(layers []Layer) error { return validateLayerRoots(layers) }

// validateLayerRoots runs the strict-root + legacy-block rejection per layer,
// naming the source file in each error. deepMerge drops nil values, so a layer
// carrying ONLY a binaries:/tools: key never reaches the merged map — this
// per-layer pass is the only place that sees it. It is shared by LoadConfig and
// ResolveLayeredPath (dwe vars inspect) so value resolution cannot drift from
// the runtime loader on which top-level keys a config layer may carry: vars
// inspection must never resolve a value out of a layer LoadConfig would reject.
// The binaries:/tools: rejections come first so their migration messages win
// over the strict-root "unknown top-level key" message; keys are sorted for a
// deterministic error.
func validateLayerRoots(layers []Layer) error {
	for _, layer := range layers {
		if _, ok := layer.Data["binaries"]; ok {
			return fmt.Errorf("%s: binaries: moved to ~/.config/dwe/config — use binary_docker=/path, binary_git=/path, etc. See docs/reference/config/workspace.md", layer.Path)
		}
		if _, ok := layer.Data["tools"]; ok {
			return fmt.Errorf("%s: tools: no longer supported — define tool entries as services with type: tool in workspace/services/. See docs/reference/config/services/index.md", layer.Path)
		}
		keys := make([]string, 0, len(layer.Data))
		for k := range layer.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, ok := allowedRootKeySet[key]; ok {
				continue
			}
			return fmt.Errorf("%s: unknown top-level key %q — move custom values under \"vars:\" (e.g. vars.%s.*); allowed top-level keys: %s",
				layer.Path, key, key, strings.Join(allowedRootKeys, ", "))
		}
	}
	return validateSecretsBlock(layers)
}

// validateSecretsBlock pins the secrets: block to workspace.yml (the first
// layer) and validates its shape there, naming the offending file.
//
// The single-layer rule mirrors compose.extra's inverse restriction: the
// recipient identifies the project's key pair, so a per-developer local.yml
// override would silently decrypt a different half of the tree than the one
// `dwe secrets status` reports. Validating here rather than in LoadConfig
// means LoadConfig, ResolveLayeredPath (dwe vars inspect) and the staged-write
// check in `dwe secrets set` all reject the same files.
func validateSecretsBlock(layers []Layer) error {
	for i, layer := range layers {
		raw, ok := layer.Data["secrets"]
		if !ok {
			continue
		}
		if i != 0 {
			return fmt.Errorf("%s: secrets: is only valid in workspace.yml — the recipient identifies the project key pair and cannot be overridden per layer", layer.Path)
		}
		if raw == nil {
			continue
		}
		block, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: secrets: must be a mapping", layer.Path)
		}
		rawRecipient, present := block["recipient"]
		if !present || rawRecipient == nil {
			continue
		}
		recipient, ok := rawRecipient.(string)
		if !ok {
			return fmt.Errorf("%s: secrets.recipient must be a string (an age1… public recipient)", layer.Path)
		}
		if recipient = strings.TrimSpace(recipient); recipient == "" {
			continue
		}
		if err := secrets.ParseRecipient(recipient); err != nil {
			return fmt.Errorf("%s: secrets.recipient is malformed: %w", layer.Path, err)
		}
	}
	return nil
}

// LocalLayerPath returns the conventional workspace/local.yml path for a given
// workspace.yml path. Used to identify which Layer is the local override.
func LocalLayerPath(workspacePath string) string {
	return filepath.Join(filepath.Dir(workspacePath), "workspace", "local.yml")
}

// LayeredValue describes a dot-path resolved at each config layer plus the file
// that supplies the current value. Default is the merge of all non-local
// layers (workspace.yml + defaults.yml); Local is workspace/local.yml alone;
// Current is the full 3-layer merge — what ${...} / ResolvePath see at
// runtime. The *OK fields report whether the path was present at that layer.
//
// An explicit null in local.yml is present-but-nil (LocalOK true, Local nil)
// and, per deepMerge's nil-skip, does NOT win the current value — so Origin
// stays on the lower layer it failed to override.
type LayeredValue struct {
	Default   any
	DefaultOK bool
	Local     any
	LocalOK   bool
	Current   any
	CurrentOK bool
	// Origin is the path of the highest-precedence layer whose value at the
	// resolved path is non-nil, or "" when the path is unresolved everywhere.
	Origin string
}

// ResolveLayeredPath resolves a dot-path across the three config layers,
// reporting the value at each layer and the source file that supplies the
// current value. It reuses LoadLayers (so it cannot drift from
// LoadConfig's layer set), deepMerge (the runtime merge semantics, including
// nil-skip), and ResolvePath.
func ResolveLayeredPath(workspacePath, path string) (LayeredValue, error) {
	layers, err := LoadLayers(workspacePath)
	if err != nil {
		return LayeredValue{}, err
	}
	// Enforce the same per-layer strict-root / legacy-key validation LoadConfig
	// applies, so vars inspect never resolves a value out of a layer the runtime
	// loader would reject (unknown top-level key, legacy binaries:/tools:).
	if err := validateLayerRoots(layers); err != nil {
		return LayeredValue{}, err
	}
	return resolveLayeredPath(layers, LocalLayerPath(workspacePath), path), nil
}

func resolveLayeredPath(layers []Layer, localPath, path string) LayeredValue {
	defaults := make(map[string]any)
	current := make(map[string]any)
	var local map[string]any
	for _, l := range layers {
		// Deep-copy before merging: deepMerge shares nested-map references for
		// absent keys, so building defaults and current from the same layers
		// would otherwise cross-contaminate (and mutate l.Data, which the Origin
		// scan below reads). Each merged view gets its own copy.
		current = deepMergeCopy(current, l.Data)
		if l.Path == localPath {
			local = l.Data
		} else {
			defaults = deepMergeCopy(defaults, l.Data)
		}
	}

	var lv LayeredValue
	lv.Default, lv.DefaultOK = ResolvePath(defaults, path)
	if local != nil {
		lv.Local, lv.LocalOK = ResolvePath(local, path)
	}
	lv.Current, lv.CurrentOK = ResolvePath(current, path)

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
