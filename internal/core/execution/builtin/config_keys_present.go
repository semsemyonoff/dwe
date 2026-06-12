package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// ConfigKeysPresent is the `config_keys_present` predicate builtin. It verifies
// that each requested dot-path resolves to a non-empty value in the merged DWE
// configuration (config.DweConfig.Raw).
//
// It mirrors env_keys_present but reads the in-memory merged config — where the
// workspace.yml / defaults.yml / local.yml layers are already merged — instead
// of an on-disk env-file. That makes it the right check for values the setup
// wizard writes into local.yml: the addressing is symmetric with the wizard's
// writes: dot-paths, and there is no dependency on whether a rendered .env has
// been materialised yet. Pair it with stages: [post-setup] so it runs at the
// final preflight (after the wizard, or right before deploy when no wizard
// runs) rather than the early pre-wizard gate.
type ConfigKeysPresent struct{}

// Validate checks that a non-empty keys list is provided.
func (ConfigKeysPresent) Validate(with map[string]any) error {
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
func (ConfigKeysPresent) Describe(with map[string]any) string {
	keys, _ := spec.GetStringSlice(with, "keys")
	return fmt.Sprintf("builtin: config_keys_present(keys=[%s])", strings.Join(keys, ","))
}

// Run resolves each dot-path against the merged config and reports any path
// that is absent or whose value is empty.
func (ConfigKeysPresent) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	keys, err := spec.GetStringSlice(with, "keys")
	if err != nil {
		return err
	}
	if ectx.Config == nil {
		return errors.New("merged config not available")
	}

	var missing []string
	for _, k := range keys {
		v, ok := config.ResolvePath(ectx.Config.Raw, k)
		if !ok || isEmptyConfigValue(v) {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing or empty keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

// isEmptyConfigValue treats nil and a value that renders to the empty string as
// "not set", mirroring env_keys_present's empty==missing semantics. Non-string
// scalars (numbers, bools) render via fmt and count as present.
func isEmptyConfigValue(v any) bool {
	if v == nil {
		return true
	}
	return fmt.Sprintf("%v", v) == ""
}
