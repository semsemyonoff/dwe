// Package varsusage provides read-only helpers for the vars: sandbox used by
// `dwe vars`: leaf enumeration over the merged vars: tree, effective and
// per-layer value resolution, and pinned YAML scalar coercion for `vars set`.
// It is stdout-free — callers (internal/cli/vars, internal/core/ui/render)
// format the output.
package varsusage

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"

	"gopkg.in/yaml.v3"
)

// VarsPrefix is the dot-path head every var lives under.
const VarsPrefix = "vars"

// EnumerateVars walks the merged vars: tree and returns every leaf as a sorted
// dot-path (e.g. "vars.db.host"). Nested maps are namespaces (interior nodes),
// not leaves; a scalar, sequence, or empty map is a leaf. A nil/empty vars:
// block yields an empty slice. Ordering is deterministic (sorted) to keep
// callers and tests stable.
func EnumerateVars(cfg *config.DweConfig) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	walkVarLeaves(VarsPrefix, cfg.Vars, &out)
	sort.Strings(out)
	return out
}

func walkVarLeaves(prefix string, node any, out *[]string) {
	m, isMap := node.(map[string]any)
	if !isMap || len(m) == 0 {
		// A scalar/sequence (or an empty map) is a leaf. The top-level vars:
		// block itself is never a leaf — an empty vars: yields no entries.
		if prefix != VarsPrefix {
			*out = append(*out, prefix)
		}
		return
	}
	for k, v := range m {
		walkVarLeaves(prefix+"."+k, v, out)
	}
}

// ResolveVar returns the effective value at a vars dot-path — what ${...}
// resolves to at runtime — via config.ResolvePath over the merged Raw map. A
// namespace path returns its subtree (map[string]any); a leaf returns the
// scalar. The bool reports whether the path was found.
//
// Reads are confined to the vars.* sandbox: a path that is not "vars" itself or
// strictly beneath "vars." returns (nil, false) without touching Raw, so an
// unnormalized caller can never read non-vars config and break the sandbox
// contract (callers also normalize via normalizeVarPath, this is defense in
// depth).
func ResolveVar(cfg *config.DweConfig, path string) (any, bool) {
	if cfg == nil || !isVarsPath(path) {
		return nil, false
	}
	return config.ResolvePath(cfg.Raw, path)
}

// isVarsPath reports whether a dot-path lives in the vars.* sandbox: the bare
// "vars" namespace or any path whose head segment is "vars".
func isVarsPath(path string) bool {
	return path == VarsPrefix || strings.HasPrefix(path, VarsPrefix+".")
}

// CoerceScalar parses a raw CLI value argument into the typed Go value that
// `dwe vars set` writes into local.yml. The grammar is pinned because yaml.v3's
// interface{} decode is surprising for several forms:
//
//   - true / false        -> bool
//   - 42                  -> int (canonical base-10 only)
//   - 1.5                 -> float64
//   - null / ~            -> nil (YAML null)
//   - "" (empty arg)      -> nil (YAML null); pass a quoted "" literal for an empty string
//   - quoted ("x" / 'x')  -> string, verbatim
//   - yes / no / on / off -> string (YAML 1.2 core schema — NOT bools)
//   - 0755 / 01 / 00      -> string (leading-zero/octal kept verbatim, never reinterpreted)
//   - 0x1F / 0o17 / +3    -> string (non-canonical int forms kept verbatim)
//   - 1.2.3               -> string
//   - 2024-01-02          -> string (timestamps are not coerced to time.Time)
//
// Maps ({a: b}) and sequences ([a]) are rejected — a var is a leaf value.
func CoerceScalar(raw string) (any, error) {
	if raw == "" {
		// Empty arg → explicit YAML null. The shell strips the quotes from
		// `set x ""`, so an empty-string value must be passed as a quoted ""
		// literal (e.g. set x '""'), which reaches us as the 2-char string `""`.
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("parse value %q: %w", raw, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		// Comment-only / empty document — treat as null.
		return nil, nil
	}
	n := doc.Content[0]
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("value %q is not a scalar (maps and sequences are not allowed)", raw)
	}
	// Explicitly quoted input is always a string, regardless of its content.
	if n.Style == yaml.DoubleQuotedStyle || n.Style == yaml.SingleQuotedStyle {
		return n.Value, nil
	}
	switch n.Tag {
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return n.Value, nil
		}
		return b, nil
	case "!!int":
		var i int
		if err := n.Decode(&i); err == nil && strconv.Itoa(i) == n.Value {
			return i, nil
		}
		// Non-canonical int form (leading zero, octal/hex/binary radix, +sign):
		// keep the literal verbatim as a string so the writer round-trips it
		// faithfully instead of silently reinterpreting (e.g. 0755 → 493).
		return n.Value, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			// Non-finite (.inf/-.inf/.nan) is not JSON-representable and would
			// crash `--output json` reads (and poison stored values for bridge
			// clients, which always run non-interactively). Keep verbatim as a
			// string, consistent with the non-canonical-int handling above.
			return n.Value, nil
		}
		return f, nil
	case "!!null":
		return nil, nil
	default:
		// !!str, !!timestamp, and any other tag → keep verbatim as a string.
		return n.Value, nil
	}
}
