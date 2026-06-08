// Package generatedstore provides an atomic-write API for the per-service
// generated-value store at <root>/.dwe/generated.yml. The store holds secrets
// minted by a service's own generator (e.g. Laravel APP_KEY, Magento crypt.key)
// that DWE harvests once and replays on every subsequent render via the
// ${generated.<name>} template namespace.
//
// This package is a leaf: it MUST NOT import anything from internal/core. It
// deals only in plain strings (service → field → value). Missing file is an
// empty store; a corrupt file surfaces as an error and is NEVER swallowed.
package generatedstore

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultRelPath is the relative path to the generated-value store file.
const DefaultRelPath = ".dwe/generated.yml"

// Store is the in-memory representation of the generated-value file: a mapping
// of service name → field name → harvested value.
type Store struct {
	// Services maps a service name to its harvested field/value pairs.
	Services map[string]map[string]string `yaml:"services,omitempty"`
}

// New returns an empty, ready-to-use store.
func New() *Store {
	return &Store{Services: make(map[string]map[string]string)}
}

// Load reads the store file from disk. A missing file yields an empty store
// (not an error); malformed YAML surfaces as an error and is NOT swallowed.
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("failed to read generated store: %w", err)
	}

	var s Store
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed to parse generated store: %w", err)
	}
	if s.Services == nil {
		s.Services = make(map[string]map[string]string)
	}
	return &s, nil
}

// Save writes the store atomically to disk using write-temp + rename. Ensures
// the parent directory exists (mode 0o755) and the file is mode 0o600 — the
// store holds service-minted secrets, so it matches the .env secret convention
// (see internal/shared/envfile.Write) rather than world-readable 0o644.
func Save(path string, s *Store) error {
	if s == nil {
		return fmt.Errorf("cannot save nil generated store")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal generated store: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".generated-*.yml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Clean up on error
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}

// Has reports whether the given service/field pair holds a value.
func (s *Store) Has(svc, field string) bool {
	if s == nil || s.Services == nil {
		return false
	}
	fields, ok := s.Services[svc]
	if !ok {
		return false
	}
	_, ok = fields[field]
	return ok
}

// Get returns the value for the given service/field, or "" when absent.
func (s *Store) Get(svc, field string) string {
	if s == nil || s.Services == nil {
		return ""
	}
	return s.Services[svc][field]
}

// Service returns a copy of the field/value map for the given service, or an
// empty (non-nil) map when the service is absent. Mutating the result does not
// affect the store.
func (s *Store) Service(svc string) map[string]string {
	out := make(map[string]string)
	if s == nil || s.Services == nil {
		return out
	}
	maps.Copy(out, s.Services[svc])
	return out
}

// SetIfAbsent stores val for the service/field only when no value is present.
// Returns true when it wrote (the field was absent), false when a value already
// existed and was preserved.
func (s *Store) SetIfAbsent(svc, field, val string) bool {
	if s.Services == nil {
		s.Services = make(map[string]map[string]string)
	}
	if s.Has(svc, field) {
		return false
	}
	if s.Services[svc] == nil {
		s.Services[svc] = make(map[string]string)
	}
	s.Services[svc][field] = val
	return true
}

// ClearService removes all values for a single service.
func (s *Store) ClearService(svc string) {
	if s == nil || s.Services == nil {
		return
	}
	delete(s.Services, svc)
}

// ClearAll removes every value from the store.
func (s *Store) ClearAll() {
	if s == nil {
		return
	}
	s.Services = make(map[string]map[string]string)
}

// IsEmpty reports whether the store holds no values at all.
func (s *Store) IsEmpty() bool {
	if s == nil || s.Services == nil {
		return true
	}
	for _, fields := range s.Services {
		if len(fields) > 0 {
			return false
		}
	}
	return true
}
