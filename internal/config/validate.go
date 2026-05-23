package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/validate/diag"
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
	Checks []CheckEntry
}

// rawValidateConfig is the wire-level shape, decoded with strict KnownFields.
// Severity is a string on the wire and converted via parseSeverity.
type rawValidateConfig struct {
	Checks []rawCheckEntry `yaml:"checks"`
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
			warnings = append(warnings, diag.Diagnostic{
				Severity: diag.SeverityInfo,
				Domain:   "config",
				Target:   "validate",
				File:     diagFile,
				Line:     line,
				Message:  fmt.Sprintf("stage %q not bound to built-in hooks", stage),
				Hint:     "Reserved stages: deploy, run, stop, command. Custom stages are accepted but won't run automatically — invoke via `devbox validate --stage <name>`.",
			})
		}

		cfg.Checks = append(cfg.Checks, entry)
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
