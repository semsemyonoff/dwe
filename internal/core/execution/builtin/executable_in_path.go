package builtin

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"devbox-cli/internal/core/execution/builtin/spec"
)

type executableInPathBuiltin struct{}

func (executableInPathBuiltin) Validate(with map[string]any) error {
	name := spec.GetStringParam(with, "name", "")
	if name == "" {
		return errors.New("missing required param 'name'")
	}
	return nil
}

func (executableInPathBuiltin) Describe(with map[string]any) string {
	name := spec.GetStringParam(with, "name", "")
	return fmt.Sprintf("builtin: executable_in_path(name=%s)", name)
}

func (executableInPathBuiltin) Run(_ context.Context, with map[string]any, _ spec.ExecContext) error {
	name := spec.GetStringParam(with, "name", "")
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("not found in PATH: %s", name)
	}
	return nil
}
