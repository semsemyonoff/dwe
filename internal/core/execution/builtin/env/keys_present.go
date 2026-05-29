package env

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/execution/builtin/spec"
)

// KeysPresent verifies that the named env-file declares each requested key with a non-empty value.
type KeysPresent struct{}

// Validate checks that file and keys params are present.
func (KeysPresent) Validate(with map[string]any) error {
	file := spec.GetStringParam(with, "file", "")
	if file == "" {
		return errors.New("missing required param 'file'")
	}
	keys, err := spec.GetStringSlice(with, "keys")
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("missing required param 'keys'")
	}
	return nil
}

// Describe returns a human-readable summary for plan display.
func (KeysPresent) Describe(with map[string]any) string {
	file := spec.GetStringParam(with, "file", "")
	keys, _ := spec.GetStringSlice(with, "keys")
	return fmt.Sprintf("builtin: env_keys_present(file=%s, keys=[%s])", file, strings.Join(keys, ","))
}

// Run reads the env-file and reports any missing or empty keys.
func (KeysPresent) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	file := spec.GetStringParam(with, "file", "")
	keys, err := spec.GetStringSlice(with, "keys")
	if err != nil {
		return err
	}

	full := file
	if !filepath.IsAbs(full) {
		full = filepath.Join(ectx.ProjectRoot, file)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("file not found: %s", file)
		}
		return fmt.Errorf("read %s: %w", file, err)
	}

	entries := spec.ParseEnvEntries(data)
	var missing []string
	for _, k := range keys {
		if v, ok := entries[k]; !ok || v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing or empty keys: %s", strings.Join(missing, ", "))
	}
	return nil
}
