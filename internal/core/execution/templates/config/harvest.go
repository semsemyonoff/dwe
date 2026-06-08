package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// HarvestedField records the outcome of harvesting one declared generated field.
type HarvestedField struct {
	// Field is the generated field name (the ${generated.<name>} key).
	Field string
	// Value is the value captured from the on-disk file.
	Value string
	// Wrote is true when the value was newly written into the store (the field
	// was absent); false when a prior value was preserved (write-if-absent).
	Wrote bool
}

// HarvestResult describes the outcome of a HarvestGenerated pass for one service.
type HarvestResult struct {
	// Service is the service name.
	Service string
	// Fields lists each declared generated field that was processed, in
	// deterministic (sorted) order.
	Fields []HarvestedField
}

// HarvestGenerated iterates the service's declared generated: fields, reads each
// field's output file under the service hub dir (svc.Dir), extracts the value via
// the field's regex pattern (capture group 1), and write-if-absent stores it into
// store. When at least one field is newly written, the store is saved atomically
// to <projectRoot>/<generatedstore.DefaultRelPath>.
//
// "Harvest, not mint": DWE only reads a string the service itself generated. A
// missing file, a pattern that matches no line, a pattern with no capture group,
// or a pattern that captures an empty value are all surfaced as errors — never
// silently skipped — so a half-minted secret cannot pollute the store. A service
// that declares no generated: fields is a no-op (no store write).
func HarvestGenerated(projectRoot string, cfg *projectconfig.DweConfig, serviceName string, store *generatedstore.Store) (HarvestResult, error) {
	if cfg == nil {
		return HarvestResult{}, errors.New("config harvest: nil cfg")
	}
	if store == nil {
		return HarvestResult{}, errors.New("config harvest: nil store")
	}
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return HarvestResult{}, fmt.Errorf("config harvest: unknown service %q", serviceName)
	}
	if len(svc.Generated) == 0 {
		return HarvestResult{Service: serviceName}, nil
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return HarvestResult{}, fmt.Errorf("resolve project root: %w", err)
	}
	if filepath.Clean(svc.Dir) == "." || svc.Dir == "" {
		return HarvestResult{}, fmt.Errorf("config harvest: service %q has no dir to harvest from", serviceName)
	}
	absHubDir := filepath.Join(absRoot, svc.Dir)
	if _, err := pathsafe.ContainedRel(absRoot, absHubDir); err != nil {
		return HarvestResult{}, fmt.Errorf("config harvest: service dir %q escapes project root: %w", svc.Dir, err)
	}

	res := HarvestResult{Service: serviceName}
	wroteAny := false
	for _, field := range sortedGeneratedKeys(svc.Generated) {
		value, err := extractGenerated(absRoot, absHubDir, serviceName, field, svc.Generated[field])
		if err != nil {
			return HarvestResult{}, err
		}
		wrote := store.SetIfAbsent(serviceName, field, value)
		wroteAny = wroteAny || wrote
		res.Fields = append(res.Fields, HarvestedField{Field: field, Value: value, Wrote: wrote})
	}

	if wroteAny {
		storePath := filepath.Join(absRoot, generatedstore.DefaultRelPath)
		if err := generatedstore.Save(storePath, store); err != nil {
			return HarvestResult{}, fmt.Errorf("config harvest: save store: %w", err)
		}
	}
	return res, nil
}

// extractGenerated reads the field's output file and applies its regex pattern
// line by line, returning the first capture-group-1 value found. Anchored
// patterns like `^APP_KEY=(.*)$` therefore match per line without a (?m) flag.
func extractGenerated(absRoot, absHubDir, serviceName, field string, spec projectconfig.GeneratedField) (string, error) {
	if spec.File == "" {
		return "", fmt.Errorf("config harvest: service %q field %q has no file", serviceName, field)
	}
	if spec.Pattern == "" {
		return "", fmt.Errorf("config harvest: service %q field %q has no pattern", serviceName, field)
	}

	re, err := regexp.Compile(spec.Pattern)
	if err != nil {
		return "", fmt.Errorf("config harvest: service %q field %q: invalid pattern %q: %w", serviceName, field, spec.Pattern, err)
	}
	if re.NumSubexp() < 1 {
		return "", fmt.Errorf("config harvest: service %q field %q: pattern %q has no capture group", serviceName, field, spec.Pattern)
	}

	absFile := filepath.Join(absHubDir, spec.File)
	if _, err := pathsafe.ContainedRel(absHubDir, absFile); err != nil {
		return "", fmt.Errorf("config harvest: file %q escapes service dir: %w", spec.File, err)
	}
	if err := pathsafe.CheckNoSymlinks(absRoot, absFile, "generated file"); err != nil {
		return "", err
	}

	data, err := os.ReadFile(absFile)
	if err != nil {
		return "", fmt.Errorf("config harvest: read %s for field %q: %w", spec.File, field, err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value := m[1]
		if value == "" {
			return "", fmt.Errorf("config harvest: pattern %q matched but captured an empty value for field %q in %s", spec.Pattern, field, spec.File)
		}
		return value, nil
	}
	return "", fmt.Errorf("config harvest: pattern %q did not match any line in %s for field %q", spec.Pattern, field, spec.File)
}

// sortedGeneratedKeys returns the generated field names in deterministic order so
// store writes and HarvestResult.Fields are reproducible (never a map range).
func sortedGeneratedKeys(m map[string]projectconfig.GeneratedField) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
