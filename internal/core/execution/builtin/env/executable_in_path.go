package env

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"devbox-cli/internal/core/execution/builtin/spec"
)

// ExecutableInPath verifies that a named binary is discoverable on PATH.
type ExecutableInPath struct{}

// Validate checks that the name param is present.
func (ExecutableInPath) Validate(with map[string]any) error {
	name := spec.GetStringParam(with, "name", "")
	if name == "" {
		return errors.New("missing required param 'name'")
	}
	return nil
}

// Describe returns a human-readable summary for plan display.
func (ExecutableInPath) Describe(with map[string]any) string {
	name := spec.GetStringParam(with, "name", "")
	return fmt.Sprintf("builtin: executable_in_path(name=%s)", name)
}

// Run reports whether the named binary is on PATH.
func (ExecutableInPath) Run(_ context.Context, with map[string]any, _ spec.ExecContext) error {
	name := spec.GetStringParam(with, "name", "")
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("not found in PATH: %s", name)
	}
	return nil
}
