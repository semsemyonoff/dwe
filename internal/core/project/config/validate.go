package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/validate/diag"
)

// ValidateConfigFileName is the filename of the project-level validate.yml,
// relative to the devbox/ directory.
const ValidateConfigFileName = "validate.yml"

// ValidateConfigPath returns the canonical path to validate.yml given a
// project base directory (the directory that contains the devbox/ folder).
func ValidateConfigPath(baseDir string) string {
	return filepath.Join(baseDir, "devbox", ValidateConfigFileName)
}

// reservedValidateStages is the set of stages bound to built-in preflight hooks.
// Stages outside this set are accepted (open enum) but produce an info-level
// load-time warning so users know they will not be invoked automatically.
var reservedValidateStages = map[string]struct{}{
	"deploy":  {},
	"run":     {},
	"stop":    {},
	"command": {},
}

// allowedCheckTypes is the set of legal "type:" values for a check entry.
var allowedCheckTypes = map[string]struct{}{
	"builtin": {},
	"command": {},
}

// allowedLinterTypes is the set of legal "type:" values for a linter entry.
var allowedLinterTypes = map[string]struct{}{
	"builtin": {},
	"generic": {},
}

// CheckEntry is a single entry from devbox/validate.yml. The struct stores the
// parsed-and-validated form: severity is the diag.Severity enum (validate.yml
// uses the string spelling on the wire; the loader converts and rejects
// unknown values at parse time).
type CheckEntry struct {
	ID          string
	Description string
	Stages      []string
	Severity    diag.Severity
	Hint        string
	Type        string
	Cmd         string
	With        map[string]any
	// SourceLine is the 1-based line number of the entry's first key in
	// validate.yml, captured during YAML node traversal. Diagnostics reference
	// this so users can jump straight to the offending entry.
	SourceLine int
}

// ValidateConfig is the top-level shape of devbox/validate.yml.
type ValidateConfig struct {
	Checks  []CheckEntry
	Linters []LinterEntry
}

// LinterEntry is a single entry under the linters: map in validate.yml.
// Pointer fields distinguish "absent in YAML" from "present and zero".
type LinterEntry struct {
	ID         string
	Type       string   // "builtin" (default) | "generic"
	Enabled    *bool    // nil → autodetect
	Bin        string   // empty → adapter default; must be a bare command name
	Paths      []string // empty → adapter default
	Extensions []string // empty → adapter default; each must start with "."
	Filenames  []string // empty → adapter default; no path separators
	Flags      []string
	Severity   *diag.Severity // nil → no clamp
	SourceLine int
}

// rawValidateConfig is the wire-level shape, decoded with strict KnownFields.
// Severity is a string on the wire and converted via parseSeverity.
type rawValidateConfig struct {
	Checks  []rawCheckEntry           `yaml:"checks"`
	Linters map[string]rawLinterEntry `yaml:"linters"`
}

type rawLinterEntry struct {
	Type       string   `yaml:"type"`
	Enabled    *bool    `yaml:"enabled"`
	Bin        string   `yaml:"bin"`
	Paths      []string `yaml:"paths"`
	Extensions []string `yaml:"extensions"`
	Filenames  []string `yaml:"filenames"`
	Flags      []string `yaml:"flags"`
	Severity   *string  `yaml:"severity"`
}

type rawCheckEntry struct {
	ID          string         `yaml:"id"`
	Description string         `yaml:"description"`
	Stages      []string       `yaml:"stages"`
	Severity    string         `yaml:"severity"`
	Hint        string         `yaml:"hint"`
	Type        string         `yaml:"type"`
	Cmd         string         `yaml:"cmd"`
	With        map[string]any `yaml:"with"`
}

// findClosestStage returns a known stage name if the input is within
// Levenshtein distance 2, else empty string.
func findClosestStage(input string) string {
	knownStages := []string{"deploy", "run", "stop", "command"}
	const maxDistance = 2

	var closest string
	closestDist := maxDistance + 1

	for _, known := range knownStages {
		d := levenshteinDistance(input, known)
		if d <= maxDistance && d < closestDist {
			closest = known
			closestDist = d
		}
	}

	return closest
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Use two rows for space efficiency.
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1]
			} else {
				curr[j] = 1 + min(prev[j], min(curr[j-1], prev[j-1]))
			}
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// LoadValidateConfig reads and strictly decodes devbox/validate.yml.
//
// Returns (cfg, warnings, nil) on success. Soft issues (unknown stages outside
// the reserved set) are reported as info-level diagnostics in warnings rather
// than failing the load. Hard issues (unknown fields, unknown type/severity,
// missing required fields, duplicate ids) return a non-nil error.
//
// When the file does not exist, returns (nil, nil, os.ErrNotExist) so callers
// can treat it as optional.
func LoadValidateConfig(path string) (*ValidateConfig, []diag.Diagnostic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var raw rawValidateConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Walk the document to capture per-entry source line numbers.
	lines, err := validateEntryLines(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Pad lines to match raw.Checks length; defensive against discrepancy.
	for len(lines) < len(raw.Checks) {
		lines = append(lines, 0)
	}

	cfg := &ValidateConfig{Checks: make([]CheckEntry, 0, len(raw.Checks))}
	var warnings []diag.Diagnostic
	seenIDs := make(map[string]int, len(raw.Checks))

	// Diagnostics always reference the canonical "devbox/validate.yml" path
	// regardless of how the file was located on disk — matches what Task 4/5
	// validators report so users see a consistent reference everywhere.
	const diagFile = "devbox/validate.yml"

	for i, r := range raw.Checks {
		line := lines[i]

		if r.ID == "" {
			return nil, nil, fmt.Errorf("parse %s: checks[%d]: id is required", path, i)
		}
		if r.Description == "" {
			return nil, nil, fmt.Errorf("parse %s: check %q: description is required", path, r.ID)
		}
		if r.Type == "" {
			return nil, nil, fmt.Errorf("parse %s: check %q: type is required", path, r.ID)
		}
		if _, ok := allowedCheckTypes[r.Type]; !ok {
			return nil, nil, fmt.Errorf("parse %s: check %q: unknown type %q (allowed: builtin, command)", path, r.ID, r.Type)
		}
		if r.Cmd == "" {
			return nil, nil, fmt.Errorf("parse %s: check %q: cmd is required", path, r.ID)
		}
		if len(r.Stages) == 0 {
			return nil, nil, fmt.Errorf("parse %s: check %q: stages is required and must be non-empty", path, r.ID)
		}
		sev, err := parseSeverity(r.Severity)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: check %q: %w", path, r.ID, err)
		}
		if prev, dup := seenIDs[r.ID]; dup {
			return nil, nil, fmt.Errorf("parse %s: check %q: duplicate id (previous occurrence at index %d)", path, r.ID, prev)
		}
		seenIDs[r.ID] = i

		entry := CheckEntry{
			ID:          r.ID,
			Description: r.Description,
			Stages:      append([]string(nil), r.Stages...),
			Severity:    sev,
			Hint:        r.Hint,
			Type:        r.Type,
			Cmd:         r.Cmd,
			With:        r.With,
			SourceLine:  line,
		}

		for _, stage := range r.Stages {
			if _, ok := reservedValidateStages[stage]; ok {
				continue
			}

			// Unknown stage: emit warning with suggestion if close to a known value.
			hint := "Known stages: deploy, run, stop, command"

			switch stage {
			case "restart":
				hint += "\n\nNote: restart is composite (stop + run, no separate preflight stage). If you need to check before stopping, use stages: [stop]. If you need to check before running, use stages: [run]."
			case "reset":
				hint += "\n\nNote: reset uses the stop preflight stage. If you need to check before reset, use stages: [stop]."
			default:
				// Check for typos via Levenshtein distance.
				if suggestion := findClosestStage(stage); suggestion != "" {
					hint += fmt.Sprintf("\n\nDid you mean %q?", suggestion)
				}
			}

			warnings = append(warnings, diag.Diagnostic{
				Severity: diag.SeverityWarning,
				Domain:   "config",
				Target:   "validate",
				File:     diagFile,
				Line:     line,
				Message:  fmt.Sprintf("check %q: stage %q is not a known preflight stage", r.ID, stage),
				Hint:     hint,
			})
		}

		cfg.Checks = append(cfg.Checks, entry)
	}

	// Linters: map → ordered slice with per-entry validation and source-line capture.
	linterLines, err := linterEntryLines(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	linterIDs := make([]string, 0, len(raw.Linters))
	for id := range raw.Linters {
		linterIDs = append(linterIDs, id)
	}
	sort.Strings(linterIDs)
	seenLinterIDs := make(map[string]struct{}, len(linterIDs))
	for _, id := range linterIDs {
		r := raw.Linters[id]
		line := linterLines[id]

		if id == "" {
			return nil, nil, fmt.Errorf("parse %s: linters: id (map key) must not be empty", path)
		}
		if strings.ContainsAny(id, `/\`) {
			return nil, nil, fmt.Errorf("parse %s: linters: id %q must be a bare name (no path separators)", path, id)
		}
		lowerID := strings.ToLower(id)
		if _, dup := seenLinterIDs[lowerID]; dup {
			return nil, nil, fmt.Errorf("parse %s: linter %q: duplicate id (case-insensitive collision)", path, id)
		}
		seenLinterIDs[lowerID] = struct{}{}

		ltype := r.Type
		if ltype == "" {
			ltype = "builtin"
		}
		if _, ok := allowedLinterTypes[ltype]; !ok {
			return nil, nil, fmt.Errorf("parse %s: linter %q: unknown type %q (allowed: builtin, generic)", path, id, ltype)
		}

		if strings.ContainsAny(r.Bin, `/\`) {
			return nil, nil, fmt.Errorf("parse %s: linter %q: bin %q must be a bare command name (no path separators); resolution happens via PATH", path, id, r.Bin)
		}

		paths := make([]string, 0, len(r.Paths))
		for _, p := range r.Paths {
			if p == "" {
				return nil, nil, fmt.Errorf("parse %s: linter %q: paths entry must not be empty", path, id)
			}
			if filepath.IsAbs(p) {
				return nil, nil, fmt.Errorf("parse %s: linter %q: paths entry %q must be relative to the project root", path, id, p)
			}
			if !filepath.IsLocal(p) && filepath.Clean(p) != "." {
				return nil, nil, fmt.Errorf("parse %s: linter %q: paths entry %q must not traverse outside the project root", path, id, p)
			}
			paths = append(paths, filepath.Clean(p))
		}

		exts := make([]string, 0, len(r.Extensions))
		for _, e := range r.Extensions {
			if e == "" {
				return nil, nil, fmt.Errorf("parse %s: linter %q: extensions entry must not be empty", path, id)
			}
			if !strings.HasPrefix(e, ".") {
				return nil, nil, fmt.Errorf("parse %s: linter %q: extensions entry %q must start with %q", path, id, e, ".")
			}
			if strings.ContainsAny(e, `/\`) {
				return nil, nil, fmt.Errorf("parse %s: linter %q: extensions entry %q must not contain path separators", path, id, e)
			}
			exts = append(exts, e)
		}

		filenames := make([]string, 0, len(r.Filenames))
		for _, f := range r.Filenames {
			if f == "" {
				return nil, nil, fmt.Errorf("parse %s: linter %q: filenames entry must not be empty", path, id)
			}
			if strings.ContainsAny(f, `/\`) {
				return nil, nil, fmt.Errorf("parse %s: linter %q: filenames entry %q must not contain path separators", path, id, f)
			}
			filenames = append(filenames, f)
		}

		var sevPtr *diag.Severity
		if r.Severity != nil {
			sev, err := parseLinterSeverity(*r.Severity)
			if err != nil {
				return nil, nil, fmt.Errorf("parse %s: linter %q: %w", path, id, err)
			}
			sevPtr = &sev
		}

		cfg.Linters = append(cfg.Linters, LinterEntry{
			ID:         id,
			Type:       ltype,
			Enabled:    r.Enabled,
			Bin:        r.Bin,
			Paths:      paths,
			Extensions: exts,
			Filenames:  filenames,
			Flags:      append([]string(nil), r.Flags...),
			Severity:   sevPtr,
			SourceLine: line,
		})
	}

	// Sort warnings deterministically (line, then message).
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Line != warnings[j].Line {
			return warnings[i].Line < warnings[j].Line
		}
		return warnings[i].Message < warnings[j].Message
	})

	return cfg, warnings, nil
}

// parseLinterSeverity is stricter than parseSeverity: empty string is rejected
// (callers gate on r.Severity != nil) and "ok" is rejected since clamping
// findings to OK would mute them in the table; users disable a linter with
// enabled: false instead.
func parseLinterSeverity(s string) (diag.Severity, error) {
	switch s {
	case "":
		return 0, fmt.Errorf("severity must be one of error|warning|info, not empty")
	case "error":
		return diag.SeverityError, nil
	case "warning":
		return diag.SeverityWarning, nil
	case "info":
		return diag.SeverityInfo, nil
	case "ok":
		return 0, fmt.Errorf("severity %q is not allowed for linters (use `enabled: false` to silence)", s)
	default:
		return 0, fmt.Errorf("unknown severity %q (allowed: error, warning, info)", s)
	}
}

// linterEntryLines walks the document and returns a map from linter ID
// (map-key value under the top-level linters: mapping) to the line of that
// key. Used for SourceLine capture in diagnostics.
func linterEntryLines(data []byte) (map[string]int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	out := map[string]int{}
	if root.Kind == 0 {
		return out, nil
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return out, nil
	}
	for i := 0; i < len(doc.Content)-1; i += 2 {
		key := doc.Content[i]
		if key.Value != "linters" {
			continue
		}
		val := doc.Content[i+1]
		if val.Kind != yaml.MappingNode {
			return out, nil
		}
		for j := 0; j < len(val.Content)-1; j += 2 {
			k := val.Content[j]
			out[k.Value] = k.Line
		}
		return out, nil
	}
	return out, nil
}

// parseSeverity converts the YAML "severity" string to a diag.Severity. Empty
// defaults to SeverityError. Unknown values are rejected.
func parseSeverity(s string) (diag.Severity, error) {
	switch s {
	case "", "error":
		return diag.SeverityError, nil
	case "warning":
		return diag.SeverityWarning, nil
	case "info":
		return diag.SeverityInfo, nil
	default:
		return 0, fmt.Errorf("unknown severity %q (allowed: error, warning, info)", s)
	}
}

// validateEntryLines walks the top-level document and returns the line number
// of each entry under checks: in source order. Returns an empty slice if the
// document has no checks: key (legal: zero-check config). Returns an error if
// the YAML cannot be parsed into a node tree (a strict decode would already
// have caught this — we re-parse defensively to keep the walk independent of
// the typed decode path).
func validateEntryLines(data []byte) ([]int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return nil, nil
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i < len(doc.Content)-1; i += 2 {
		key := doc.Content[i]
		if key.Value != "checks" {
			continue
		}
		val := doc.Content[i+1]
		if val.Kind != yaml.SequenceNode {
			return nil, nil
		}
		lines := make([]int, 0, len(val.Content))
		for _, entry := range val.Content {
			lines = append(lines, entry.Line)
		}
		return lines, nil
	}
	return nil, nil
}
